package config

import (
	"fmt"
	"os"
	"strings"
)

// IsProduction reports whether this process is meant to serve real users.
// Anything other than an explicit production marker is treated as development,
// so a missing APP_ENV never silently relaxes a check.
func IsProduction() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) {
	case "prod", "production":
		return true
	}
	return false
}

// Validate refuses to start on a configuration that would appear to work and
// then fail in ways nobody sees until a customer does.
//
// Every check here corresponds to something that is correct in development and
// wrong in production: a localhost URL baked into an SMS link, a dev signing
// key, an unencrypted database connection. In development they are warnings, so
// a laptop still starts with defaults.
func (c Config) Validate() []string {
	var problems []string

	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	// PUBLIC_BASE_URL is embedded in owner acknowledge links and in every
	// uploaded media URL. Shipped as localhost, every one of those is dead on
	// arrival - and nothing fails at startup to tell you.
	base := strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/")
	switch {
	case base == "":
		add("PUBLIC_BASE_URL is not set: acknowledge links and media URLs will be relative and unusable")
	case strings.Contains(base, "localhost"), strings.Contains(base, "127.0.0.1"):
		add("PUBLIC_BASE_URL is %q: acknowledge links sent by SMS and every uploaded media URL would point at the server itself", base)
	case !strings.HasPrefix(base, "https://"):
		add("PUBLIC_BASE_URL is %q: acknowledge tokens would travel over plain HTTP", base)
	}

	if len(c.JWTSecret) < 32 {
		add("JWT_SECRET is %d bytes: under 32 is brute-forceable for a token that grants account access", len(c.JWTSecret))
	}
	if c.WebhookSecret == "" {
		add("PAYMENT_WEBHOOK_SECRET is empty: payment webhooks cannot be authenticated, so anyone who finds the URL can mark a booking paid")
	}

	// sslmode=disable sends the database password and every row in clear text.
	// Fine over a loopback socket, not over a VPC.
	if strings.Contains(c.DatabaseURL, "sslmode=disable") &&
		!strings.Contains(c.DatabaseURL, "@localhost") &&
		!strings.Contains(c.DatabaseURL, "@127.0.0.1") {
		add("DATABASE sslmode=disable against a non-local host: credentials and data cross the network unencrypted")
	}

	if os.Getenv("OTP_FIXED_CODE") != "" {
		add("OTP_FIXED_CODE is set: every OTP would be the same known value")
	}
	if c.LogOtpCodes {
		add("LOG_OTP_CODES is true: one-time codes would be written to the log file")
	}

	for _, o := range c.CORSOrigins {
		o = strings.TrimSpace(o)
		if o == "*" {
			add("CORS_ORIGINS contains '*': any site could call this API with a user's credentials")
		}
		if strings.Contains(o, "localhost") {
			add("CORS_ORIGINS contains %q, a development origin", o)
		}
	}

	// Trusting X-Forwarded-For from anyone lets a single machine bypass the
	// rate limiter by varying a header. Empty is the safe default, but behind a
	// load balancer it means every user shares one bucket.
	if os.Getenv("TRUSTED_PROXIES") == "" {
		add("TRUSTED_PROXIES is not set: if this service runs behind a load balancer, every request will appear to come from it and the rate limiter will treat all users as one caller")
	}

	return problems
}
