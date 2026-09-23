package handler

import (
	"net/http/httptest"
	"testing"
)

// Anonymous callers must not share one search-history bucket.
//
// They used to: viewer() returned the literal "anonymous" for everyone without
// a token, so one logged-out visitor's recent searches were served to the next
// person who opened the app. Confirmed live before the fix - a search for
// "divorce party venue" came back in a different caller's history.
func TestAnonymousViewersAreNotPooled(t *testing.T) {
	h := &Handler{}

	req := func(ip, ua string) string {
		r := httptest.NewRequest("GET", "/api/v1/search/recent", nil)
		r.RemoteAddr = ip + ":54321"
		r.Header.Set("User-Agent", ua)
		return h.viewer(r)
	}

	a := req("203.0.113.10", "Mozilla/5.0 BrowserA")
	b := req("203.0.113.11", "Mozilla/5.0 BrowserB")

	if a == b {
		t.Fatalf("two different anonymous callers share the key %q - history leaks between them", a)
	}
	if a == "anonymous" || b == "anonymous" {
		t.Error(`viewer() returned the shared "anonymous" key`)
	}
	// Stable for the same caller, or the list would reset on every request.
	if again := req("203.0.113.10", "Mozilla/5.0 BrowserA"); again != a {
		t.Errorf("same caller got two keys: %q then %q", a, again)
	}
	// The key is a hash, not the raw address - it is stored in Redis and
	// should not carry a readable IP around.
	if a == "203.0.113.10" || len(a) < 8 {
		t.Errorf("anonymous key %q looks like raw caller data", a)
	}
}
