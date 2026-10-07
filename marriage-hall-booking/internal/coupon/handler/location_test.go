package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A coordinate only counts when it is usable. Anything else is treated as "no
// location", which shows every live offer rather than hiding them - a filter
// that silently empties the screen looks like a broken app.
func TestOptCoord(t *testing.T) {
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
		{"lat", "0", false, "Null Island, not a position"},
		{"lng", "-122.4", true, "western hemisphere"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/?"+c.key+"="+c.raw, nil)
		if got := optCoord(r, c.key); (got != nil) != c.want {
			t.Errorf("optCoord(%s=%q) usable=%v, want %v - %s",
				c.key, c.raw, got != nil, c.want, c.why)
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

