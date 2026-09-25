package middleware

import (
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
)

type bucket struct {
	prefix string
	max    int
	window time.Duration
}

// Limits from the original specification, per client IP.
var buckets = []struct {
	path string
	bucket
}{
	{"/api/v1/auth/register", bucket{"register", 5, time.Hour}},
	{"/api/v1/auth/login", bucket{"login", 10, 15 * time.Minute}},
	{"/api/v1/auth/otp/resend", bucket{"otp_resend", 3, 15 * time.Minute}},
	{"/api/v1/auth/forgot-password", bucket{"forgot_password", 3, time.Hour}},
}

var global = bucket{"global", 100, time.Minute}

// RateLimitDisabled turns the limiter off entirely. Only for running the full
// API collection against a local server, where 124 requests in a few seconds
// legitimately exceeds the global 100/min budget. Set RATE_LIMIT_DISABLED=true.
var RateLimitDisabled = os.Getenv("RATE_LIMIT_DISABLED") == "true"

// RateLimit counts requests per IP in Redis. If Redis is unreachable the request
// is allowed through: an outage should degrade the limiter, not the whole API.
func RateLimit(rdb *redis.Client) func(http.Handler) http.Handler {
	if RateLimitDisabled {
		logger.Warn("rate limiting is OFF (RATE_LIMIT_DISABLED=true) - do not use in production")
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := global
			for _, candidate := range buckets {
				if strings.HasPrefix(r.URL.Path, candidate.path) {
					b = candidate.bucket
					break
				}
			}

			key := "rate_limit:" + b.prefix + ":" + clientIP(r)
			ctx := r.Context()

			count, err := rdb.Incr(ctx, key).Result()
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if count == 1 {
				rdb.Expire(ctx, key, b.window)
			}
			if count > int64(b.max) {
				w.Header().Set("Retry-After", strconv.Itoa(int(b.window.Seconds())))
				response.Error(w, http.StatusTooManyRequests, "Too many requests", "RATE_LIMIT_EXCEEDED")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP defers to httpx.ClientIP, which honours X-Forwarded-For only when
// the direct peer is a configured trusted proxy.
//
// The previous version trusted the last XFF entry unconditionally. With no
// proxy in front the client supplies the entire header, so one machine could
// bypass the 5-per-hour registration limit by varying a string - confirmed
// against the running service.
func clientIP(r *http.Request) string { return httpx.ClientIP(r) }
