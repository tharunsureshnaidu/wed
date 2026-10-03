package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type fakeDB struct {
	args []any
	err  error
	n    int
}

func (f *fakeDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	f.n++
	f.args = args
	return pgconn.CommandTag{}, f.err
}

// The reason an admin typed is the whole point of the audit row - a rejection
// with no recorded why is what this package exists to stop.
func TestReasonAndEventTypeAreRecorded(t *testing.T) {
	db := &fakeDB{}
	Record(context.Background(), db, Decision{
		Actor: 7, Action: "CANCEL_BOOKING", Entity: EntityBooking,
		EntityID: "b1", Status: "CANCELLED",
		Reason: "double booked", EventType: "SANGEET",
	})
	var got map[string]any
	if err := json.Unmarshal(db.args[4].([]byte), &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"status": "CANCELLED", "reason": "double booked", "eventType": "SANGEET",
	} {
		if got[k] != want {
			t.Errorf("new_values[%q] = %v, want %q", k, got[k], want)
		}
	}
}

// An absent reason must leave the key out, not store "". The analytics count
// rows "with a reason", and an empty string would inflate that count.
func TestEmptyFieldsAreOmitted(t *testing.T) {
	db := &fakeDB{}
	Record(context.Background(), db, Decision{
		Action: "SET_FACILITY_STATUS_APPROVED", Entity: EntityFacility,
		EntityID: "f1", Status: "APPROVED",
	})
	var got map[string]any
	if err := json.Unmarshal(db.args[4].([]byte), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["reason"]; ok {
		t.Error("an absent reason was stored anyway")
	}
	if _, ok := got["eventType"]; ok {
		t.Error("an absent eventType was stored anyway")
	}
}

// A system action has no user. 0 is not a real user id and the FK would
// reject it, taking the decision down with it.
func TestSystemActorIsNullNotZero(t *testing.T) {
	db := &fakeDB{}
	Record(context.Background(), db, Decision{
		Action: "EXPIRE_BOOKING", Entity: EntityBooking, EntityID: "b2", Status: "EXPIRED",
	})
	if db.args[0] != nil {
		t.Errorf("actor = %v, want nil for a system action", db.args[0])
	}

	db = &fakeDB{}
	Record(context.Background(), db, Decision{
		Actor: 42, Action: "X", Entity: EntityUser, EntityID: "u1", Status: "ACTIVE",
	})
	if db.args[0] != int64(42) {
		t.Errorf("actor = %v, want 42", db.args[0])
	}
}

// Auditing is observability. A failed log write must never fail the decision
// that was just made - rolling back an approval over a log row would turn this
// into an outage.
func TestAWriteFailureDoesNotPanic(t *testing.T) {
	db := &fakeDB{err: errors.New("table is gone")}
	Record(context.Background(), db, Decision{
		Action: "SET_QUOTE_REJECTED", Entity: EntityQuote, EntityID: "q1", Status: "REJECTED",
	})
	if db.n != 1 {
		t.Errorf("exec called %d times, want 1", db.n)
	}
}
