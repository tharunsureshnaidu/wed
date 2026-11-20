package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A coordinate only counts when it is usable. Anything else is treated as "no
// location", which shows every live offer rather than hiding them - a filter
// that silently empties the screen looks like a broken app.
func TestOptLocation(t *testing.T) {
	cases := []struct {
		query string
		want  bool
		why   string
	}{
		{"lat=12.9716&lng=77.5946", true, "a normal fix"},
		{"lat=37.77&lng=-122.4", true, "western hemisphere"},
		{"lat=0&lng=77.6", true, "the equator is a real place"},
		{"", false, "nothing sent"},
		{"lat=12.9716", false, "half a location is a client bug"},
		{"lat=abc&lng=77.5946", false, "unparseable"},
		{"lat=91&lng=77.5946", false, "latitude out of range"},
		{"lat=12.9&lng=181", false, "longitude out of range"},
		{"lat=0&lng=0", false, "Null Island, not a position"},
		{"lat=NaN&lng=77.6", false, "NaN fails every comparison"},
		{"lat=12.9&lng=Inf", false, "infinity"},
	}
	for _, c := range cases {
		lat, lng := optLocation(httptest.NewRequest("GET", "/?"+c.query, nil))
		if got := lat != nil && lng != nil; got != c.want {
			t.Errorf("optLocation(%q) usable=%v, want %v - %s", c.query, got, c.want, c.why)
		}
	}
}

// The radius cap must be enforced, and enforced loudly: falling back to 50 km
// would answer a question the caller did not ask.
func TestGeoRadiusCap(t *testing.T) {
	if maxGeoRadiusKm*1000 <= defaultGeoRadiusMetres {
		t.Error("the cap must be above the default, or no caller can widen the search")
	}
	if defaultGeoRadiusMetres != 50000.0 {
		t.Errorf("default radius = %v, want 50km - the geo announcements use the same figure",
			defaultGeoRadiusMetres)
	}
}

func TestCouponsAvailableRouteIsRegistered(t *testing.T) {
	mux := http.NewServeMux()
	h := &Handler{}
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/v1/coupons/available?lat=12.9716&lng=77.5946&radiusKm=50", nil)
	handler, pattern := mux.Handler(req)
	if pattern != "GET /api/v1/coupons/available" {
		t.Fatalf("expected pattern 'GET /api/v1/coupons/available', got %q", pattern)
	}
	if handler == nil {
		t.Fatal("expected handler to not be nil")
	}
}
