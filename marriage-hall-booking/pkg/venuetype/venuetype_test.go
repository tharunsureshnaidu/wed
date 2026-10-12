package venuetype

import "testing"

// The API must speak one word for a hall. The bug this package fixes was a
// bookings response carrying "targetType": "HALL" beside a nested
// "type": "MARRIAGE_HALL" for the same venue.
func TestAPISpeaksHall(t *testing.T) {
	if got := API(StoredHall); got != Hall {
		t.Errorf("API(%q) = %q, want %q", StoredHall, got, Hall)
	}
	// Anything else passes through, so a type added later needs no change here.
	for _, v := range []string{Hotel, "RESORT", ""} {
		if got := API(v); got != v {
			t.Errorf("API(%q) = %q, want it unchanged", v, got)
		}
	}
}

// Both words must be accepted on input: saved Postman requests, the import
// script and any deployed client still send MARRIAGE_HALL.
func TestStoredAcceptsBothWords(t *testing.T) {
	for _, in := range []string{Hall, StoredHall} {
		if got := Stored(in); got != StoredHall {
			t.Errorf("Stored(%q) = %q, want %q", in, got, StoredHall)
		}
	}
	if got := Stored(Hotel); got != Hotel {
		t.Errorf("Stored(%q) = %q, want it unchanged", Hotel, got)
	}
}

// Round-tripping must not corrupt a value: what the API returns must map back
// to the same stored value, or a client echoing a venue's type back on an
// update would write something else.
func TestRoundTrip(t *testing.T) {
	for _, stored := range []string{StoredHall, Hotel} {
		if got := Stored(API(stored)); got != stored {
			t.Errorf("round trip of %q gave %q", stored, got)
		}
	}
}

func TestValid(t *testing.T) {
	for _, v := range []string{Hall, StoredHall, Hotel} {
		if !Valid(v) {
			t.Errorf("Valid(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "hall", "BANQUET"} {
		if Valid(v) {
			t.Errorf("Valid(%q) = true, want false", v)
		}
	}
}

func TestLiveSQLUsesAlias(t *testing.T) {
	want := "f.is_deleted = FALSE AND COALESCE(f.status, 'APPROVED') NOT IN ('BLOCKED', 'REJECTED')"
	if got := LiveSQL("f"); got != want {
		t.Fatalf("LiveSQL(f) = %q", got)
	}
}
