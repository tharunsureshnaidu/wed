package jwt

import (
	"strings"
	"testing"
	"time"
)

const secret = "K3p9vL7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3"

func newSigner(t *testing.T, ttl time.Duration) *Signer {
	t.Helper()
	s, err := NewSigner(secret, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRoundTrip(t *testing.T) {
	s := newSigner(t, time.Minute)
	tok, err := s.Generate("user@example.com", "42", "ROLE_ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "user@example.com" || c.UserID != "42" || c.Role != "ROLE_ADMIN" {
		t.Fatalf("claims round-tripped wrong: %+v", c)
	}
}

// A token whose payload is edited must fail: otherwise anyone could promote
// themselves to ROLE_ADMIN by rewriting the middle segment.
func TestTamperedPayloadRejected(t *testing.T) {
	s := newSigner(t, time.Minute)
	tok, _ := s.Generate("user@example.com", "42", "ROLE_CUSTOMER")

	forged, _ := s.Generate("user@example.com", "42", "ROLE_ADMIN")
	parts, forgedParts := strings.Split(tok, "."), strings.Split(forged, ".")
	spliced := parts[0] + "." + forgedParts[1] + "." + parts[2]

	if _, err := s.Parse(spliced); err == nil {
		t.Fatal("tampered payload was accepted")
	}
}

func TestWrongSecretRejected(t *testing.T) {
	tok, _ := newSigner(t, time.Minute).Generate("a@b.com", "1", "ROLE_CUSTOMER")
	other, err := NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Parse(tok); err == nil {
		t.Fatal("token signed with a different secret was accepted")
	}
}

func TestExpiredRejected(t *testing.T) {
	s := newSigner(t, -time.Second) // already expired
	tok, _ := s.Generate("a@b.com", "1", "ROLE_CUSTOMER")
	if _, err := s.Parse(tok); err != ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestMalformedRejected(t *testing.T) {
	s := newSigner(t, time.Minute)
	for _, bad := range []string{"", "a.b", "a.b.c", "....", "not-a-token"} {
		if _, err := s.Parse(bad); err == nil {
			t.Fatalf("malformed token %q was accepted", bad)
		}
	}
}

func TestShortSecretRejected(t *testing.T) {
	if _, err := NewSigner("tooshort", time.Minute); err == nil {
		t.Fatal("a secret under 32 bytes should be rejected")
	}
}
