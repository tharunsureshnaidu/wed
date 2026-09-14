// Package security holds black-box tests that hit the running API over HTTP,
// checking the boundaries a unit test cannot see: authorization between real
// users, and what an unauthenticated caller can reach.
//
// Set API_URL (and have the server running) to enable them.
package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

var base string

func TestMain(m *testing.M) {
	base = os.Getenv("API_URL")
	os.Exit(m.Run())
}

func skipUnlessLive(t *testing.T) {
	t.Helper()
	if base == "" {
		t.Skip("set API_URL to run (e.g. http://localhost:8080)")
	}
}

type resp struct {
	Success   bool            `json:"success"`
	Message   string          `json:"message"`
	ErrorCode string          `json:"errorCode"`
	Data      json.RawMessage `json:"data"`
}

func do(t *testing.T, method, path, token string, body any) (int, resp) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest(method, base+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var r resp
	json.NewDecoder(res.Body).Decode(&r)
	return res.StatusCode, r
}

// Every route that exposes user data must reject an anonymous caller.
func TestProtectedRoutesRejectAnonymous(t *testing.T) {
	skipUnlessLive(t)
	for _, route := range []struct {
		method, path string
	}{
		{"GET", "/api/v1/auth/me"},
		{"GET", "/api/v1/users/me"},
		{"PUT", "/api/v1/users/me"},
		{"DELETE", "/api/v1/users/me"},
		{"GET", "/api/v1/bookings"},
		{"POST", "/api/v1/bookings/halls"},
		{"POST", "/api/v1/payments/create"},
		{"GET", "/api/v1/admin/dashboard"},
		{"GET", "/api/v1/admin/users"},
		{"GET", "/api/v1/vendors/me"},
		{"POST", "/api/v1/facilities"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			status, _ := do(t, route.method, route.path, "", map[string]any{})
			if status != http.StatusUnauthorized {
				t.Fatalf("want 401 without a token, got %d", status)
			}
		})
	}
}

// A forged or tampered token must never be accepted.
func TestForgedTokensRejected(t *testing.T) {
	skipUnlessLive(t)
	// A syntactically valid JWT signed with the wrong key, claiming admin.
	forged := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiJhQGIuY29tIiwidXNlcklkIjoiMSIsInJvbGUiOiJST0xFX0FETUlOIiwiZXhwIjo5OTk5OTk5OTk5fQ." +
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, tok := range []string{forged, "not-a-token", "", "Bearer", "a.b.c"} {
		status, _ := do(t, "GET", "/api/v1/admin/dashboard", tok, nil)
		if status != http.StatusUnauthorized {
			t.Fatalf("forged token %q got %d, want 401", tok, status)
		}
	}
}

// The webhook must not be callable without a valid signature - it moves money.
func TestWebhookRequiresSignature(t *testing.T) {
	skipUnlessLive(t)
	status, r := do(t, "POST", "/api/v1/payments/webhook", "", map[string]any{
		"eventId": "forged-1", "orderId": "order_whatever", "status": "SUCCESS", "amount": 1,
	})
	if status != http.StatusUnauthorized || r.ErrorCode != "INVALID_SIGNATURE" {
		t.Fatalf("want 401 INVALID_SIGNATURE, got %d %s", status, r.ErrorCode)
	}
}

// Login must not reveal whether an identifier is registered.
func TestLoginDoesNotEnumerate(t *testing.T) {
	skipUnlessLive(t)
	_, known := do(t, "POST", "/api/v1/auth/login", "", map[string]any{
		"identifier": "c@ex.com", "password": "definitely-wrong",
	})
	_, unknown := do(t, "POST", "/api/v1/auth/login", "", map[string]any{
		"identifier": fmt.Sprintf("nobody-%d@nowhere.test", time.Now().UnixNano()),
		"password":   "definitely-wrong",
	})
	// Both may be rate limited; only compare when both got a real answer.
	if known.ErrorCode == "RATE_LIMIT_EXCEEDED" || unknown.ErrorCode == "RATE_LIMIT_EXCEEDED" {
		t.Skip("rate limited; rerun after the window resets")
	}
	if known.ErrorCode != unknown.ErrorCode || known.Message != unknown.Message {
		t.Fatalf("responses differ: known=%q/%q unknown=%q/%q",
			known.ErrorCode, known.Message, unknown.ErrorCode, unknown.Message)
	}
}

// SQL metacharacters in a search term must be treated as data, not SQL.
func TestSearchIsNotSQLInjectable(t *testing.T) {
	skipUnlessLive(t)
	for _, payload := range []string{
		"'; DROP TABLE facilities; --",
		"' OR '1'='1",
		"%' UNION SELECT NULL--",
	} {
		// url.QueryEscape, or the client rejects the malformed URL locally and
		// the payload never actually reaches the server.
		status, _ := do(t, "GET", "/api/v1/facilities?search="+url.QueryEscape(payload), "", nil)
		if status != http.StatusOK {
			t.Fatalf("injection payload %q produced %d", payload, status)
		}
	}
	// The table must still exist afterwards.
	if status, _ := do(t, "GET", "/api/v1/facilities", "", nil); status != http.StatusOK {
		t.Fatal("facilities listing broke after injection attempts")
	}
}

// Security headers should be present on every response.
func TestSecurityHeaders(t *testing.T) {
	skipUnlessLive(t)
	res, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	} {
		if got := res.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if res.Header.Get("X-Request-Id") == "" {
		t.Error("X-Request-Id missing")
	}
}

// An origin that is not on the allowlist must not be reflected back.
func TestCORSDoesNotReflectArbitraryOrigins(t *testing.T) {
	skipUnlessLive(t)
	req, _ := http.NewRequest("GET", base+"/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("reflected a non-allowlisted origin: %q", got)
	}
}

// Regression: an unfiltered search returned zero rows because the amenities
// clause bound a nil slice as SQL NULL, and `cardinality(NULL) = 0` is NULL
// rather than true - which made the whole WHERE clause NULL and excluded every
// venue. A search with no filters must return everything visible.
func TestUnfilteredSearchReturnsVenues(t *testing.T) {
	skipUnlessLive(t)
	_, r := do(t, "GET", "/api/v1/search/venues", "", nil)
	if !r.Success {
		t.Fatalf("search failed: %s", r.Message)
	}
	var page struct {
		Total int64 `json:"totalElements"`
	}
	if err := json.Unmarshal(r.Data, &page); err != nil {
		t.Fatal(err)
	}
	if page.Total == 0 {
		t.Fatal("unfiltered search returned 0 venues; expected every visible venue")
	}
}

// A filter for an amenity nothing has must return nothing - proving the clause
// actually filters rather than being ignored.
func TestAmenityFilterActuallyFilters(t *testing.T) {
	skipUnlessLive(t)
	_, r := do(t, "GET", "/api/v1/search/venues?amenities="+url.QueryEscape("No Such Amenity"), "", nil)
	var page struct {
		Total int64 `json:"totalElements"`
	}
	json.Unmarshal(r.Data, &page)
	if page.Total != 0 {
		t.Fatalf("filtering on a nonexistent amenity returned %d venues", page.Total)
	}
}

// Blocking an account must cut off the tokens already issued, and must be
// reversible. Both halves have been wrong here: the endpoint could only ever
// suspend (there was no way back), and a suspended user kept working until
// their access token expired.
func TestBlockTogglesAndEndsSessions(t *testing.T) {
	skipUnlessLive(t)

	code, r := do(t, "POST", "/api/v1/auth/login", "",
		map[string]any{"identifier": "admin@example.com", "password": "Admin@123"})
	if code != 200 {
		t.Skip("seeded admin unavailable")
	}
	var adminAuth struct {
		AccessToken string `json:"accessToken"`
	}
	json.Unmarshal(r.Data, &adminAuth)

	// A throwaway account to block.
	email := fmt.Sprintf("blocktest%d@example.com", time.Now().UnixNano())
	do(t, "POST", "/api/v1/auth/register", "",
		map[string]any{"fullName": "Block Test", "email": email, "password": "SecurePass@123"})
	code, r = do(t, "POST", "/api/v1/auth/register/verify-email", "",
		map[string]any{"target": email, "otpCode": "000000"})
	if code != 200 {
		t.Skip("registration needs OTP_FIXED_CODE=000000 to run unattended")
	}
	var auth struct {
		AccessToken string `json:"accessToken"`
		User        struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	json.Unmarshal(r.Data, &auth)

	if code, _ := do(t, "GET", "/api/v1/auth/me", auth.AccessToken, nil); code != 200 {
		t.Fatalf("a fresh token should work, got %d", code)
	}

	block := map[string]any{"userId": auth.User.ID, "type": "user"}
	if code, _ := do(t, "POST", "/api/v1/admin/block", adminAuth.AccessToken, block); code != 200 {
		t.Fatalf("block failed: %d", code)
	}
	if code, _ := do(t, "GET", "/api/v1/auth/me", auth.AccessToken, nil); code != 401 {
		t.Errorf("a blocked user's access token still works (got %d)", code)
	}
	if code, _ := do(t, "POST", "/api/v1/auth/login", "",
		map[string]any{"identifier": email, "password": "SecurePass@123"}); code == 200 {
		t.Error("a blocked user can still log in")
	}

	// The same call again unblocks: Java's /block is a toggle.
	if code, _ := do(t, "POST", "/api/v1/admin/block", adminAuth.AccessToken, block); code != 200 {
		t.Fatalf("unblock failed: %d", code)
	}
	if code, _ := do(t, "POST", "/api/v1/auth/login", "",
		map[string]any{"identifier": email, "password": "SecurePass@123"}); code != 200 {
		t.Errorf("an unblocked user cannot log back in (got %d)", code)
	}
}
