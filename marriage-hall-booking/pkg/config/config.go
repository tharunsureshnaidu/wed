package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServerPort   string
	DatabaseURL  string
	RedisAddr    string
	KafkaBrokers []string

	JWTSecret        string
	JWTExpiry        time.Duration
	JWTRefreshExpiry time.Duration
	ResetPasswordURL string
	LogOtpCodes      bool
	WebhookSecret    string
	CORSOrigins      []string
}

// Load reads .env (if present) into the process environment, then reads config
// from environment variables.
//
// .env deliberately OVERRIDES pre-existing env vars. This machine exports
// MySQL-era DB_* vars globally (DB_PORT=3306, DB_NAME=booking_db) left over
// from the Java service; with the usual "real env wins" precedence the app
// silently dials a dead MySQL instead of the Postgres named in .env.
func Load() Config {
	loadDotEnv(".env")

	return Config{
		ServerPort: env("SERVER_PORT", "8080"),
		DatabaseURL: fmt.Sprintf(
			"postgres://%s:%s@%s:%s/%s?sslmode=disable",
			env("DB_USERNAME", "postgres"),
			env("DB_PASSWORD", "postgres"),
			env("DB_HOST", "localhost"),
			env("DB_PORT", "5432"),
			env("DB_NAME", "venue"),
		),
		RedisAddr:    env("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers: strings.Split(env("KAFKA_BROKERS", "localhost:9092"), ","),

		JWTSecret:        env("JWT_SECRET", ""),
		JWTExpiry:        ms(env("JWT_EXPIRATION_MS", "900000")),            // 15 min
		JWTRefreshExpiry: ms(env("JWT_REFRESH_EXPIRATION_MS", "604800000")), // 7 days
		ResetPasswordURL: env("FRONTEND_RESET_PASSWORD_URL", "http://localhost:3000/reset-password"),
		LogOtpCodes:      env("LOG_OTP_CODES", "true") == "true",
		WebhookSecret:    env("PAYMENT_WEBHOOK_SECRET", ""),
		CORSOrigins:      strings.Split(env("CORS_ORIGINS", "http://localhost:3000"), ","),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ponytail: 20-line parser instead of godotenv. No quoting/multiline support;
// swap in github.com/joho/godotenv if .env ever needs those.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		os.Setenv(strings.TrimSpace(k), strings.TrimSpace(v))
	}
}

func ms(v string) time.Duration {
	n, err := strconv.Atoi(v)
	if err != nil {
		panic("invalid duration (want milliseconds): " + v)
	}
	return time.Duration(n) * time.Millisecond
}
