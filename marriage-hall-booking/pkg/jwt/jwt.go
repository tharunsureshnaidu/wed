// Package jwt issues and verifies HS256 access tokens, wire-compatible with the
// Java JwtService (same claims, same base64-decoded secret, same HS256 alg).
//
// ponytail: hand-rolled rather than pulling in golang-jwt. HS256 + the three
// claims this app uses is ~60 lines of stdlib crypto; a library buys nothing
// here. Swap to golang-jwt if RS256, JWKS, or other algs are ever needed.
package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalid = errors.New("invalid JWT token")
	ErrExpired = errors.New("JWT token is expired")
)

type Claims struct {
	Subject string `json:"sub"`
	UserID  string `json:"userId"`
	// Role is the primary role, kept for compatibility with the Java service's
	// single-role claim (the gateway lifts it into X-User-Role).
	Role string `json:"role"`
	// Roles carries EVERY role the user holds. A user can be both a hall owner
	// and an admin; with only the primary role in the token their other roles
	// are unusable and authorization silently denies them.
	Roles  []string `json:"roles"`
	Issued int64    `json:"iat"`
	// IssuedMs is iat at millisecond resolution. Logout compares against it:
	// whole-second iat cannot tell a token minted just before a logout from one
	// minted just after, and both happen within the same second constantly.
	IssuedMs int64 `json:"iatMs"`
	Expires  int64 `json:"exp"`
}

// HasRole reports whether the token grants a role.
func (c *Claims) HasRole(want string) bool {
	if c.Role == want {
		return true
	}
	for _, r := range c.Roles {
		if r == want {
			return true
		}
	}
	return false
}

type Signer struct {
	key []byte
	ttl time.Duration
}

// NewSigner decodes the base64 secret exactly as the Java Decoders.BASE64 does.
func NewSigner(secret string, ttl time.Duration) (*Signer, error) {
	key, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		// The Java default secret is raw ASCII that happens not to be valid
		// base64 padding-wise; fall back to raw bytes like hmacShaKeyFor would.
		key = []byte(secret)
	}
	if len(key) < 32 {
		return nil, errors.New("jwt secret must be at least 32 bytes")
	}
	return &Signer{key: key, ttl: ttl}, nil
}

func (s *Signer) TTL() time.Duration { return s.ttl }

func (s *Signer) Generate(subject, userID string, roles ...string) (string, error) {
	now := time.Now()
	primary := ""
	if len(roles) > 0 {
		primary = roles[0]
	}
	claims := Claims{
		Subject:  subject,
		UserID:   userID,
		Role:     primary,
		Roles:    roles,
		Issued:   now.Unix(),
		IssuedMs: now.UnixMilli(),
		Expires:  now.Add(s.ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	header := enc([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := header + "." + enc(payload)
	return body + "." + enc(s.sign(body)), nil
}

func (s *Signer) Parse(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalid
	}
	body := parts[0] + "." + parts[1]
	sig, err := dec(parts[2])
	if err != nil {
		return nil, ErrInvalid
	}
	// hmac.Equal is constant-time; a plain == would leak signature bytes by timing.
	if !hmac.Equal(sig, s.sign(body)) {
		return nil, ErrInvalid
	}
	raw, err := dec(parts[1])
	if err != nil {
		return nil, ErrInvalid
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, ErrInvalid
	}
	if time.Now().Unix() >= c.Expires {
		return nil, ErrExpired
	}
	return &c, nil
}

func (s *Signer) sign(body string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(body))
	return m.Sum(nil)
}

func enc(b []byte) string          { return base64.RawURLEncoding.EncodeToString(b) }
func dec(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
