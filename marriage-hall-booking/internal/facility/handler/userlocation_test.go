package handler

import (
	"math"
	"net/http/httptest"
	"testing"
)

// A location only counts when both halves arrived and are sane. Everything
// else degrades to "no location" so the rest of the response still renders.
func TestParseUserLocation(t *testing.T) {
	cases := []struct {
		query string
		want  bool
		why   string
	}{
		{"lat=12.9716&lng=77.5946", true, "a normal Bengaluru fix"},
		{"", false, "nothing sent"},
		{"lat=12.9716", false, "half a location is a client bug, not a location"},
		{"lng=77.5946", false, "half a location the other way"},
		{"lat=abc&lng=77.5946", false, "unparseable"},
		{"lat=91&lng=77.5946", false, "latitude out of range"},
		{"lat=12.97&lng=181", false, "longitude out of range"},
		{"lat=0&lng=0", false, "Null Island is an uninitialised object, not a position"},
		{"lat=-33.86&lng=151.21", true, "southern and eastern hemispheres"},
		{"lat=NaN&lng=77.5946", false, "NaN passes every out-of-range test"},
		{"lat=12.97&lng=Inf", false, "infinity"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/?"+c.query, nil)
		if got := parseUserLocation(r); got.OK != c.want {
			t.Errorf("parseUserLocation(%q).OK = %v, want %v - %s",
				c.query, got.OK, c.want, c.why)
		}
	}
}

// nil means unknown. A zero would draw as "you are standing in the venue",
// and 35 of 52 facilities still have no coordinates.
func TestDistanceIsNilNotZeroWhenUnknown(t *testing.T) {
	lat, lng := 12.9716, 77.5946
	here := userLocation{Lat: lat, Lng: lng, OK: true}

	if got := here.distanceFrom(nil, nil); got != nil {
		t.Errorf("venue without coordinates gave %v, want nil", *got)
	}
	if got := here.distanceFrom(&lat, nil); got != nil {
		t.Errorf("venue with half a pin gave %v, want nil", *got)
	}
	if got := (userLocation{}).distanceFrom(&lat, &lng); got != nil {
		t.Errorf("no caller location gave %v, want nil", *got)
	}
	// Same point is a real zero, and must not be confused with unknown.
	got := here.distanceFrom(&lat, &lng)
	if got == nil || *got != 0 {
		t.Errorf("distance to itself = %v, want 0", got)
	}
}

// A known pair, so a bad refactor of the formula is caught rather than just
// "some number came back". Bengaluru to Chennai is ~290 km.
func TestHaversineAgainstAKnownDistance(t *testing.T) {
	got := haversineKm(12.9716, 77.5946, 13.0827, 80.2707)
	if math.Abs(got-290) > 10 {
		t.Errorf("Bengaluru->Chennai = %.2f km, want ~290", got)
	}
	// Symmetric: the distance does not depend on which end you start from.
	back := haversineKm(13.0827, 80.2707, 12.9716, 77.5946)
	if got != back {
		t.Errorf("not symmetric: %.2f vs %.2f", got, back)
	}
}
