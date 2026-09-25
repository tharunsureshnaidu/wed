package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

const secret = "K3p9vL7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3"

func call(t *testing.T, signer *jwt.Signer, roles []string, required ...string) int {
	t.Helper()
	tok, err := signer.Generate("u@example.com", "1", roles...)
	if err != nil {
		t.Fatal(err)
	}
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), RequireAuth(signer), RequireRole(required...))

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// Regression: a user who is both a hall owner and an admin was denied admin
// routes, because only the first role reached the token.
func TestSecondaryRoleIsHonoured(t *testing.T) {
	s, err := jwt.NewSigner(secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got := call(t, s, []string{"ROLE_HALL_OWNER", "ROLE_ADMIN"}, "ROLE_ADMIN"); got != http.StatusOK {
		t.Fatalf("a user holding ROLE_ADMIN as a secondary role was denied: got %d", got)
	}
}

func TestPrimaryRoleIsHonoured(t *testing.T) {
	s, _ := jwt.NewSigner(secret, time.Minute)
	if got := call(t, s, []string{"ROLE_ADMIN"}, "ROLE_ADMIN"); got != http.StatusOK {
		t.Fatalf("want 200, got %d", got)
	}
}

// The important half: extra roles must not become a way in.
func TestRoleNotHeldIsDenied(t *testing.T) {
	s, _ := jwt.NewSigner(secret, time.Minute)
	if got := call(t, s, []string{"ROLE_CUSTOMER"}, "ROLE_ADMIN"); got != http.StatusForbidden {
		t.Fatalf("want 403 for a customer hitting an admin route, got %d", got)
	}
	if got := call(t, s, []string{"ROLE_CUSTOMER", "ROLE_STAFF"}, "ROLE_ADMIN"); got != http.StatusForbidden {
		t.Fatalf("want 403, got %d", got)
	}
}

func TestMissingTokenDenied(t *testing.T) {
	s, _ := jwt.NewSigner(secret, time.Minute)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), RequireAuth(s), RequireRole("ROLE_ADMIN"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without a token, got %d", rec.Code)
	}
}
