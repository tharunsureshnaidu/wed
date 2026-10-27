package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A client-supplied X-Request-Id lands in every log line, so anything but a
// short token is replaced rather than echoed.
func TestRequestIDRejectsForgedValues(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for in, keep := range map[string]bool{
		"abc-123_X":               true,
		"":                        false,
		"x\nlevel=ERROR forged":   false,
		strings.Repeat("a", 65):   false,
		"spaces not allowed here": false,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Request-Id", in)
		h.ServeHTTP(rec, req)
		got := rec.Header().Get("X-Request-Id")
		if (got == in) != keep || got == "" {
			t.Errorf("X-Request-Id %q -> %q, keep=%v", in, got, keep)
		}
	}
}
