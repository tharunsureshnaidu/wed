package service

import "testing"

// The ack token is the only credential on a public endpoint, so it must be
// unique and long enough not to be guessable.
func TestAckTokensAreUniqueAndUrlSafe(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		tok, err := newAckToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) < 32 {
			t.Fatalf("token too short (%d chars): %q", len(tok), tok)
		}
		if seen[tok] {
			t.Fatalf("duplicate token generated: %q", tok)
		}
		seen[tok] = true
		for _, c := range tok {
			ok := c == '-' || c == '_' ||
				(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
			if !ok {
				// A token that needs escaping breaks the link in a plain-text SMS.
				t.Fatalf("token has non-URL-safe char %q in %q", c, tok)
			}
		}
	}
}

// Only OWNER and ADMIN are chased; the customer's copy is informational.
func TestChasedRoles(t *testing.T) {
	for role, want := range map[string]bool{"OWNER": true, "ADMIN": true, "CUSTOMER": false} {
		if got := chased(role); got != want {
			t.Errorf("chased(%q) = %v, want %v", role, got, want)
		}
	}
}
