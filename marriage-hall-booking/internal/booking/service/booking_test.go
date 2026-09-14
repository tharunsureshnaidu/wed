package service

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tripfcatory/marriage-hall-booking/internal/booking/repository"
	"github.com/tripfcatory/marriage-hall-booking/pkg/apperr"
	"github.com/tripfcatory/marriage-hall-booking/pkg/database"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		p, err := database.New(context.Background(), url)
		if err == nil {
			pool = p
			defer p.Close()
		}
	}
	os.Exit(m.Run())
}

func setup(t *testing.T) *Service {
	t.Helper()
	if pool == nil {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	return New(repository.New(pool), pool)
}

// fixture creates an owner, a customer and a hall, and removes them afterwards.
func fixture(t *testing.T) (ownerID, custID int64, hallID string) {
	t.Helper()
	ctx := context.Background()
	suffix := t.Name()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx,
		`INSERT INTO users (full_name, email, password_hash, status)
		 VALUES ('Owner', $1, 'x', 'ACTIVE') RETURNING id`,
		"owner-"+suffix+"@test.local").Scan(&ownerID))
	must(pool.QueryRow(ctx,
		`INSERT INTO users (full_name, email, password_hash, status)
		 VALUES ('Cust', $1, 'x', 'ACTIVE') RETURNING id`,
		"cust-"+suffix+"@test.local").Scan(&custID))
	must(pool.QueryRow(ctx,
		`INSERT INTO facilities (owner_id, name, type, base_price_per_day, status)
		 VALUES ($1, 'Test Hall', 'MARRIAGE_HALL', 100000, 'APPROVED') RETURNING id`,
		ownerID).Scan(&hallID))

	// Delete children before parents: bookings reference users, so deleting the
	// user first is refused by the FK and leaves fixtures behind for the next run.
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM bookings WHERE target_id = $1`, hallID)
		pool.Exec(ctx, `DELETE FROM facilities WHERE id = $1`, hallID)
		pool.Exec(ctx, `DELETE FROM bookings WHERE user_id = ANY($1)`, []int64{ownerID, custID})
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{ownerID, custID})
	})
	return ownerID, custID, hallID
}

func eventDate() time.Time {
	return time.Now().AddDate(0, 0, 90).Truncate(24 * time.Hour)
}

func code(err error) string {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

// The core guarantee of the whole system: many people trying to book the same
// hall, date and slot at the same instant must produce exactly one booking.
func TestConcurrentHallBookingsProduceExactlyOneWinner(t *testing.T) {
	svc := setup(t)
	_, custID, hallID := fixture(t)
	ctx := context.Background()
	date := eventDate()

	const racers = 20
	var wg sync.WaitGroup
	errs := make([]error, racers)
	start := make(chan struct{})

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = svc.CreateHallBooking(ctx, custID, HallBookingRequest{
				FacilityID: hallID, EventDate: date, SlotType: "FULL_DAY",
				// Distinct keys: every racer is a genuinely separate request,
				// so idempotency cannot be what collapses them.
				IdempotentKey: t.Name() + "-" + itoa(i),
			})
		}(i)
	}
	close(start)
	wg.Wait()

	wins, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case code(err) == "SLOT_UNAVAILABLE":
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("want exactly 1 winner, got %d (and %d conflicts)", wins, conflicts)
	}
	if conflicts != racers-1 {
		t.Fatalf("want %d clean conflicts, got %d", racers-1, conflicts)
	}

	// And the database must agree: one BOOKED slot, one booking row.
	var slots, booked int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM hall_availability
		 WHERE facility_id = $1 AND date = $2 AND status = 'BOOKED'`, hallID, date).Scan(&slots)
	pool.QueryRow(ctx,
		`SELECT count(*) FROM bookings WHERE target_id = $1 AND check_in = $2`,
		hallID, date).Scan(&booked)
	if slots != 1 {
		t.Fatalf("want 1 booked slot row, got %d", slots)
	}
	if booked != 1 {
		t.Fatalf("want 1 booking row, got %d", booked)
	}
}

// Different slots on the same day are independent inventory.
func TestDifferentSlotsSameDayBothSucceed(t *testing.T) {
	svc := setup(t)
	_, custID, hallID := fixture(t)
	ctx := context.Background()
	date := eventDate()

	for _, slot := range []string{"MORNING", "EVENING"} {
		if _, err := svc.CreateHallBooking(ctx, custID, HallBookingRequest{
			FacilityID: hallID, EventDate: date, SlotType: slot,
			IdempotentKey: t.Name() + "-" + slot,
		}); err != nil {
			t.Fatalf("%s should be bookable: %v", slot, err)
		}
	}
}

// A retried request with the same idempotency key must return the SAME booking,
// not create a second one - otherwise a flaky network double-books a customer.
func TestIdempotentRetryReturnsSameBooking(t *testing.T) {
	svc := setup(t)
	_, custID, hallID := fixture(t)
	ctx := context.Background()
	req := HallBookingRequest{
		FacilityID: hallID, EventDate: eventDate(), SlotType: "FULL_DAY",
		IdempotentKey: t.Name(),
	}

	first, err := svc.CreateHallBooking(ctx, custID, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateHallBooking(ctx, custID, req)
	if err != nil {
		t.Fatalf("retry should succeed, got %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry created a second booking: %s vs %s", first.ID, second.ID)
	}
}

// Concurrent retries of the same key must also collapse to one booking.
func TestConcurrentIdempotentRetriesCollapse(t *testing.T) {
	svc := setup(t)
	_, custID, hallID := fixture(t)
	ctx := context.Background()
	req := HallBookingRequest{
		FacilityID: hallID, EventDate: eventDate(), SlotType: "FULL_DAY",
		IdempotentKey: t.Name(),
	}

	const racers = 10
	var wg sync.WaitGroup
	ids := make([]string, racers)
	errs := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			b, err := svc.CreateHallBooking(ctx, custID, req)
			errs[i] = err
			if b != nil {
				ids[i] = b.ID
			}
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			continue
		}
		seen[ids[i]] = true
	}
	if len(seen) != 1 {
		t.Fatalf("want all retries to resolve to one booking, got %d distinct: %v", len(seen), seen)
	}

	var n int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM bookings WHERE idempotent_key = $1`, req.IdempotentKey).Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 booking row for the key, got %d", n)
	}
}

// Another user must not be able to hijack someone else's idempotency key.
func TestIdempotencyKeyIsScopedToItsOwner(t *testing.T) {
	svc := setup(t)
	_, custID, hallID := fixture(t)
	ctx := context.Background()

	var otherID int64
	pool.QueryRow(ctx,
		`INSERT INTO users (full_name, email, password_hash, status)
		 VALUES ('Other', $1, 'x', 'ACTIVE') RETURNING id`,
		"other-"+t.Name()+"@test.local").Scan(&otherID)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM bookings WHERE user_id = $1`, otherID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherID)
	})

	req := HallBookingRequest{
		FacilityID: hallID, EventDate: eventDate(), SlotType: "FULL_DAY",
		IdempotentKey: t.Name(),
	}
	if _, err := svc.CreateHallBooking(ctx, custID, req); err != nil {
		t.Fatal(err)
	}
	_, err := svc.CreateHallBooking(ctx, otherID, req)
	if got := code(err); got != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Fatalf("want IDEMPOTENCY_KEY_CONFLICT, got %q (%v)", got, err)
	}
}

// Cancelling frees the slot so it can be sold again.
func TestCancelReleasesSlot(t *testing.T) {
	svc := setup(t)
	_, custID, hallID := fixture(t)
	ctx := context.Background()
	date := eventDate()

	b, err := svc.CreateHallBooking(ctx, custID, HallBookingRequest{
		FacilityID: hallID, EventDate: date, SlotType: "FULL_DAY",
		IdempotentKey: t.Name() + "-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(ctx, b.ID, custID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateHallBooking(ctx, custID, HallBookingRequest{
		FacilityID: hallID, EventDate: date, SlotType: "FULL_DAY",
		IdempotentKey: t.Name() + "-2",
	}); err != nil {
		t.Fatalf("slot should be free after cancellation: %v", err)
	}
}

// A booking's price must come from the database, never the request.
func TestPriceIsComputedServerSide(t *testing.T) {
	svc := setup(t)
	_, custID, hallID := fixture(t)

	b, err := svc.CreateHallBooking(context.Background(), custID, HallBookingRequest{
		FacilityID: hallID, EventDate: eventDate(), SlotType: "FULL_DAY",
		IdempotentKey: t.Name(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.TotalAmount != 100000 {
		t.Fatalf("want the hall's own base price 100000, got %v", b.TotalAmount)
	}
}

func TestRejectsPastDates(t *testing.T) {
	svc := setup(t)
	_, custID, hallID := fixture(t)

	_, err := svc.CreateHallBooking(context.Background(), custID, HallBookingRequest{
		FacilityID: hallID, EventDate: time.Now().AddDate(0, 0, -1), SlotType: "FULL_DAY",
		IdempotentKey: t.Name(),
	})
	if got := code(err); got != "INVALID_DATE" {
		t.Fatalf("want INVALID_DATE, got %q", got)
	}
}

// An unpaid hold must be swept so the date does not stay blocked forever.
func TestExpiredHoldsReleaseTheSlot(t *testing.T) {
	svc := setup(t)
	_, custID, hallID := fixture(t)
	ctx := context.Background()
	date := eventDate()

	b, err := svc.CreateHallBooking(ctx, custID, HallBookingRequest{
		FacilityID: hallID, EventDate: date, SlotType: "FULL_DAY",
		IdempotentKey: t.Name() + "-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the hold window having elapsed.
	if _, err := pool.Exec(ctx,
		`UPDATE bookings SET expires_at = CURRENT_TIMESTAMP - interval '1 minute' WHERE id = $1`,
		b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExpireHolds(ctx); err != nil {
		t.Fatal(err)
	}

	var status string
	pool.QueryRow(ctx, `SELECT status FROM bookings WHERE id = $1`, b.ID).Scan(&status)
	if status != "EXPIRED" {
		t.Fatalf("want EXPIRED, got %s", status)
	}
	if _, err := svc.CreateHallBooking(ctx, custID, HallBookingRequest{
		FacilityID: hallID, EventDate: date, SlotType: "FULL_DAY",
		IdempotentKey: t.Name() + "-2",
	}); err != nil {
		t.Fatalf("slot should be free after the hold expired: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
