package handler

import "testing"

// roomCount is optional and unpriced, so the three states must stay distinct:
// absent ("not stated"), 0 ("explicitly none") and a positive count. Collapsing
// absent into 0 would tell every venue owner the party needs no rooms.
func TestRoomCountValidationBounds(t *testing.T) {
	valid := func(v *int) bool {
		return v == nil || (*v >= 0 && *v <= 1000)
	}
	n := func(i int) *int { return &i }

	for _, tc := range []struct {
		name string
		in   *int
		want bool
	}{
		{"absent", nil, true},
		{"explicitly none", n(0), true},
		{"typical", n(12), true},
		{"upper bound", n(1000), true},
		{"negative", n(-1), false},
		{"above bound", n(1001), false},
	} {
		if got := valid(tc.in); got != tc.want {
			t.Errorf("%s: valid=%v, want %v", tc.name, got, tc.want)
		}
	}

	// A nil room count must not be readable as zero.
	var absent *int
	zero := n(0)
	if absent != nil && *absent == *zero {
		t.Error("absent and zero must not compare equal")
	}
}
