package httpx

import (
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

// trustedProxies is parsed once from TRUSTED_PROXIES: a comma-separated list of
// CIDRs or bare IPs that are allowed to set X-Forwarded-For.
//
// Empty means trust nothing, which is the safe default for a service exposed
// directly. Set it to your load balancer's subnet when you put one in front.
var (
	proxyOnce sync.Once
	proxyNets []*net.IPNet
)

func loadTrustedProxies() {
	for _, raw := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(raw); err == nil {
			proxyNets = append(proxyNets, n)
			continue
		}
		// A bare address is accepted as a single-host range, so the common
		// case does not require writing /32.
		if ip := net.ParseIP(raw); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			proxyNets = append(proxyNets, &net.IPNet{
				IP: ip, Mask: net.CIDRMask(bits, bits),
			})
		}
	}
}

// trustedProxy reports whether the direct peer is a proxy we configured.
func trustedProxy(addr string) bool {
	proxyOnce.Do(loadTrustedProxies)
	if len(proxyNets) == 0 {
		return false
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range proxyNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP is the caller's address, used for rate limiting and for keying an
// anonymous visitor's search history.
//
// X-Forwarded-For is honoured ONLY when the direct peer is a configured proxy.
// Anyone can send that header, so trusting it unconditionally let a single
// machine bypass the 5-per-hour registration limit by varying one string -
// verified against the running service before this was written.
//
// When the peer is trusted, the LAST entry is taken: that is the one our own
// proxy appended, and everything to its left was supplied by the client.
func ClientIP(r *http.Request) string {
	peer := peerIP(r)
	if !trustedProxy(peer) {
		return peer
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		hops := strings.Split(xff, ",")
		if last := strings.TrimSpace(hops[len(hops)-1]); last != "" {
			return last
		}
	}
	return peer
}

func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
