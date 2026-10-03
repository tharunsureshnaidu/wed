package handler

import (
	"net/http/httptest"
	"testing"
)

// A location only counts when both halves arrived and are usable. Anything
// else degrades to "no location" so the dashboard still renders - a denied
// GPS permission must not blank the screen.
func TestOptFloatRejectsUnusableCoordinates(t *testing.T) {
	cases := []struct {
		key, raw string
		want     bool
		why      string
	}{
		{"lat", "12.9716", true, "a normal fix"},
		{"lng", "77.5946", true, "a normal fix"},
		{"lat", "", false, "nothing sent"},
		{"lat", "abc", false, "unparseable"},
		{"lat", "91", false, "latitude out of range"},
		{"lng", "181", false, "longitude out of range"},
		{"lat", "0", false, "Null Island is an uninitialised object, not a position"},
		{"lat", "-33.86", true, "southern hemisphere"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/?"+c.key+"="+c.raw, nil)
		got := optFloat(r, c.key)
		if (got != nil) != c.want {
			t.Errorf("optFloat(%s=%q) = %v, want usable=%v - %s",
				c.key, c.raw, got, c.want, c.why)
		}
	}
}
