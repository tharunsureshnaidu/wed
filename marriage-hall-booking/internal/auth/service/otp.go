package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/internal/auth/repository"
	"github.com/tripfcatory/marriage-hall-booking/pkg/apperr"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
)

const (
	otpExpiry         = 10 * time.Minute
	otpMaxAttempts    = 3
	otpCooldown       = 60 * time.Second
	otpMaxResendsHour = 5
)

// hashCode stores OTPs and reset tokens as SHA-256. The Java service used BCrypt
// here, which is wasted work: these are already high-entropy random values, not
// user-chosen passwords, so there is no dictionary to slow down. SHA-256 keeps
// verification constant-time (subtle.ConstantTimeCompare below) and cheap.
func hashCode(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.StdEncoding.EncodeToString(sum[:])
}

type OtpService struct {
	repo *repository.Repo
	// LogCodes prints generated codes to the log. True until a notification
	// service exists to actually deliver them.
	LogCodes bool
	// FixedCode, when non-empty, is issued instead of a random code. It exists
	// so local testing does not need a trip to the log for every signup.
	//
	// This is a backdoor: anyone who knows the value can verify any account and
	// complete a password reset. It is opt-in through OTP_FIXED_CODE, refused
	// outside development by NewOtpService, and must never be set in an
	// environment with real users.
	FixedCode string
}

func NewOtpService(repo *repository.Repo, logCodes bool) *OtpService {
	s := &OtpService{repo: repo, LogCodes: logCodes}
	if code := os.Getenv("OTP_FIXED_CODE"); code != "" {
		// A fixed code with real users is an account takeover, so refuse to
		// start rather than quietly ignoring the setting - a silent downgrade
		// would leave someone believing testing was set up when it was not.
		if env := os.Getenv("APP_ENV"); env != "" && env != "dev" && env != "development" && env != "local" {
			logger.Fatal("OTP_FIXED_CODE is set outside development - refusing to start",
				"APP_ENV", env)
		}
		if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
			logger.Fatal("OTP_FIXED_CODE must be exactly 6 digits", "value", code)
		}
		s.FixedCode = code
		logger.Warn("OTP IS FIXED - every OTP is this value, anyone who knows it "+
			"can verify any account. Development only.", "code", code)
	}
	return s
}

// Generate issues a 6-digit numeric OTP.
//
// The Java version hardcoded "000000" unconditionally, which is an account
// takeover anywhere it reaches real users. Here the default is a real random
// code; a fixed one has to be asked for explicitly (OTP_FIXED_CODE) and is
// rejected outside development.
func (s *OtpService) Generate(ctx context.Context, userID *int64, target string, t domain.OtpType, ip string) (string, error) {
	if s.FixedCode != "" {
		return s.store(ctx, userID, target, t, ip, s.FixedCode)
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	return s.store(ctx, userID, target, t, ip, code)
}

// GenerateResetToken issues a long URL-safe token instead of a 6-digit code:
// it travels in a reset link rather than being typed, so there is no reason to
// keep it short and brute-forceable.
func (s *OtpService) GenerateResetToken(ctx context.Context, userID *int64, target, ip string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	return s.store(ctx, userID, target, domain.OtpPasswordReset, ip, token)
}

func (s *OtpService) store(ctx context.Context, userID *int64, target string, t domain.OtpType, ip, raw string) (string, error) {
	last, err := s.repo.LatestOtp(ctx, target, t)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return "", err
	}
	if last != nil && time.Since(last.CreatedAt) < otpCooldown {
		return "", apperr.TooMany("OTP_COOLDOWN_ACTIVE", "Please wait before requesting a new OTP.")
	}

	count, err := s.repo.CountOtpSince(ctx, target, t, time.Now().Add(-time.Hour))
	if err != nil {
		return "", err
	}
	if count >= otpMaxResendsHour {
		return "", apperr.TooMany("OTP_RESEND_LIMIT_EXCEEDED", "Maximum OTP requests exceeded for this hour.")
	}

	if err := s.repo.CreateOtp(ctx, &domain.Otp{
		UserID:    userID,
		OtpCode:   hashCode(raw),
		OtpType:   t,
		Target:    target,
		ExpiresAt: time.Now().Add(otpExpiry),
	}, ip); err != nil {
		return "", err
	}

	if s.LogCodes {
		logger.Warn("OTP logged instead of sent (LOG_OTP_CODES=true)", "type", string(t), "target", target, "code", raw)
	}
	return raw, nil
}

// Verify consumes the newest OTP for target/type. Every failure path still
// increments attempt_count, so a wrong guess always costs an attempt.
func (s *OtpService) Verify(ctx context.Context, target string, t domain.OtpType, raw string) error {
	otp, err := s.repo.LatestOtp(ctx, target, t)
	if errors.Is(err, repository.ErrNotFound) {
		return apperr.BadRequest("OTP_NOT_FOUND", "No OTP found.")
	}
	if err != nil {
		return err
	}
	if otp.Verified {
		return apperr.BadRequest("OTP_ALREADY_USED", "OTP has already been used or invalidated.")
	}
	if time.Now().After(otp.ExpiresAt) {
		return apperr.BadRequest("OTP_EXPIRED", "OTP has expired.")
	}
	if otp.AttemptCount >= otpMaxAttempts {
		return apperr.TooMany("OTP_MAX_ATTEMPTS_EXCEEDED", "Maximum OTP attempts exceeded.")
	}

	match := subtle.ConstantTimeCompare([]byte(hashCode(raw)), []byte(otp.OtpCode)) == 1
	if err := s.repo.BumpOtpAttempt(ctx, otp.ID, match); err != nil {
		return err
	}
	if !match {
		return apperr.BadRequest("INVALID_OTP", "Invalid OTP")
	}
	return nil
}
