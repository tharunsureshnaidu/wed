package handler

import (
	"testing"
	"time"
)

func TestOnlyTheOtherPartyAccepts(t *testing.T) {
	cases := []struct {
		status                       string
		customer, owner, admin, want bool
	}{
		{"REPLIED", true, false, false, true},    // owner's price, customer takes it
		{"REPLIED", false, true, false, false},   // owner cannot accept their own reply
		{"COUNTERED", true, false, false, false}, // customer cannot accept their own counter
		{"COUNTERED", false, true, false, true},
		{"COUNTERED", false, false, true, true},
		{"REQUESTED", true, true, true, false}, // nothing priced yet
	}
	for _, c := range cases {
		if got := canAccept(c.status, c.customer, c.owner, c.admin); got != c.want {
			t.Errorf("canAccept(%s, cust=%v owner=%v admin=%v) = %v", c.status, c.customer, c.owner, c.admin, got)
		}
	}
}

func TestExpiredIsInclusiveInIST(t *testing.T) {
	d := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC) // as pgx scans a DATE
	// 23:00 IST on the 10th: still valid.
	if expired(&d, time.Date(2026, 10, 10, 17, 30, 0, 0, time.UTC)) {
		t.Fatal("offer valid until the 10th expired on the 10th")
	}
	// 00:30 IST on the 11th (still the 10th in UTC): expired.
	if !expired(&d, time.Date(2026, 10, 10, 19, 0, 0, 0, time.UTC)) {
		t.Fatal("offer valid until the 10th still open on the 11th IST")
	}
	if expired(nil, time.Now()) {
		t.Fatal("no valid_until means no expiry")
	}
}
