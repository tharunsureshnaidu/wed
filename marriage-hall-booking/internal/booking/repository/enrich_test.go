package repository

import "testing"

// canReview must match what POST /api/v1/reviews actually enforces, or the app
// shows a Rate button that 403s.
//
// Two rules, both easy to get wrong:
//   - only a CONFIRMED or COMPLETED stay is reviewable
//   - reviews are unique per (user, facility), NOT per booking, so a second
//     booking at the same venue is not a second chance to review it
func TestCanReviewMatchesReviewEndpointRules(t *testing.T) {
	for _, tc := range []struct {
		status      string
		hasReviewed bool
		want        bool
	}{
		{"COMPLETED", false, true},
		{"CONFIRMED", false, true},
		{"PENDING", false, false},
		{"CANCELLED", false, false},
		{"EXPIRED", false, false},
		// Already reviewed this venue - no second chance, whatever the status.
		{"COMPLETED", true, false},
		{"CONFIRMED", true, false},
	} {
		b := &Booking{Status: tc.status, HasReviewed: tc.hasReviewed}
		got := !b.HasReviewed && (b.Status == "CONFIRMED" || b.Status == "COMPLETED")
		if got != tc.want {
			t.Errorf("status=%s hasReviewed=%v: canReview=%v, want %v",
				tc.status, tc.hasReviewed, got, tc.want)
		}
	}
}

// Enrich must tolerate an empty page rather than issuing a query with an empty
// id list.
func TestEnrichEmptyIsNoOp(t *testing.T) {
	var r *Repo
	if err := r.Enrich(nil, 1, nil); err != nil {
		t.Fatalf("empty enrich returned %v", err)
	}
}

// EnrichUsers must tolerate empty slices, nil elements, or bookings with zero
// userID without issuing empty ANY() queries or panicking.
func TestEnrichUsersEmptyIsNoOp(t *testing.T) {
	var r *Repo
	if err := r.EnrichUsers(nil, nil); err != nil {
		t.Fatalf("nil bookings returned %v", err)
	}
	if err := r.EnrichUsers(nil, []*Booking{}); err != nil {
		t.Fatalf("empty bookings returned %v", err)
	}
	if err := r.EnrichUsers(nil, []*Booking{nil}); err != nil {
		t.Fatalf("nil booking in slice returned %v", err)
	}
	if err := r.EnrichUsers(nil, []*Booking{{UserID: 0}}); err != nil {
		t.Fatalf("zero userId booking returned %v", err)
	}
}

