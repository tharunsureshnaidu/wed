package handler

import "testing"

// An unknown ?status= must be a 400, not an empty page: a typo that returns
// "no bookings" sends an owner looking for lost data.
func TestValidBookingStatus(t *testing.T) {
	for _, s := range bookingStatuses {
		if !validBookingStatus(s) {
			t.Errorf("validBookingStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "pending", "NOPE", "DELETED"} {
		if validBookingStatus(s) {
			t.Errorf("validBookingStatus(%q) = true, want false", s)
		}
	}
}

// REJECTED must be a status an owner can filter by. It is a distinct outcome
// from CANCELLED - a refusal the customer did not ask for - and the decision
// analytics counts them separately.
func TestRejectedIsAFilterableStatus(t *testing.T) {
	if !validBookingStatus("REJECTED") {
		t.Error("REJECTED is not filterable, so an owner cannot list what they turned down")
	}
	for _, s := range []string{"PENDING", "CONFIRMED", "CANCELLED"} {
		if !validBookingStatus(s) {
			t.Errorf("%s missing from the filter list", s)
		}
	}
}
