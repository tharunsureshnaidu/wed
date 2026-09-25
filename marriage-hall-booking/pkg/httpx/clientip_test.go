package httpx

import (
	"net/http/httptest"
	"sync"
	"testing"
)

// Resets the parsed-once proxy list so each case can set its own env.
func withProxies(t *testing.T, v string) {
	t.Helper()
	t.Setenv("TRUSTED_PROXIES", v)
	proxyOnce = sync.Once{}
	proxyNets = nil
}

func TestClientIPIgnoresForgedHeaderFromUntrustedPeer(t *testing.T) {
	withProxies(t, "")

	r := httptest.NewRequest("POST", "/api/v1/auth/register", nil)
	r.RemoteAddr = "203.0.113.9:44321"
	// What an attacker sends to get a fresh rate-limit bucket per request.
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("ClientIP = %q, want the real peer 203.0.113.9 - a forged "+
			"X-Forwarded-For was trusted", got)
	}
}

func TestClientIPHonoursHeaderFromTrustedProxy(t *testing.T) {
	withProxies(t, "10.0.0.0/8")

	r := httptest.NewRequest("GET", "/api/v1/halls", nil)
	r.RemoteAddr = "10.0.1.5:9000" // the load balancer
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.1.5")

	// The LAST hop is what our own proxy appended; everything left of it came
	// from the client.
	if got := ClientIP(r); got != "10.0.1.5" {
		t.Fatalf("ClientIP = %q, want 10.0.1.5", got)
	}
}

func TestClientIPAcceptsBareProxyAddress(t *testing.T) {
	withProxies(t, "192.0.2.10")

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.10:7000"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")

	if got := ClientIP(r); got != "198.51.100.1" {
		t.Fatalf("ClientIP = %q, want 198.51.100.1 from a bare trusted IP", got)
	}
}

func TestClientIPFallsBackWhenHeaderAbsent(t *testing.T) {
	withProxies(t, "10.0.0.0/8")

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.1.5:9000"

	if got := ClientIP(r); got != "10.0.1.5" {
		t.Fatalf("ClientIP = %q, want the peer when no header is sent", got)
	}
}
