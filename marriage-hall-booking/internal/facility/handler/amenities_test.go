package handler

import "testing"

// amenityRefs has to cope with both shapes a form sends: the field repeated
// once per checkbox, and one field carrying a comma-separated list.
func TestAmenityRefsAcceptsBothFormShapes(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want int
	}{
		{"repeated field", []string{"PARKING", "WIFI", "STAGE"}, 3},
		{"comma separated", []string{"PARKING,WIFI,STAGE"}, 3},
		{"mixed with spaces", []string{"PARKING, WIFI", "STAGE"}, 3},
		{"empty values dropped", []string{"", "PARKING", "  "}, 1},
		{"nothing", nil, 0},
	}
	for _, c := range cases {
		if got := len(amenityRefs(c.in)); got != c.want {
			t.Errorf("%s: got %d refs, want %d", c.name, got, c.want)
		}
	}
}

// upperAll/lowerAll are what let a caller pass either an amenity CODE or a row
// id: codes match upper-cased, UUIDs lower-cased. Comparing a UUID against the
// upper-cased list matched nothing, and every attach-by-id 400ed.
func TestCaseHelpersCoverBothLookups(t *testing.T) {
	in := []string{"Parking", "3136CCAD-00D1-4A11-86C6-2C39B145A276"}
	if got := upperAll(in)[0]; got != "PARKING" {
		t.Errorf("code should upper-case for the code lookup, got %q", got)
	}
	if got := lowerAll(in)[1]; got != "3136ccad-00d1-4a11-86c6-2c39b145a276" {
		t.Errorf("uuid should lower-case for the id lookup, got %q", got)
	}
}
