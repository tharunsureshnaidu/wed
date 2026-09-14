package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/internal/auth/repository"
	"github.com/tripfcatory/marriage-hall-booking/pkg/apperr"
	"github.com/tripfcatory/marriage-hall-booking/pkg/database"
	"github.com/tripfcatory/marriage-hall-booking/pkg/jwt"
)

const testSecret = "K3p9vL7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3"

// Runs against the real local Postgres; skipped if TEST_DATABASE_URL is unset.
func setup(t *testing.T) (*AuthService, *repository.Repo, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx := context.Background()
	pool, err := database.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	repo := repository.New(pool)
	signer, err := jwt.NewSigner(testSecret, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	otp := NewOtpService(repo, false)
	// nil revoker: these tests exercise the Postgres side of auth, and a nil
	// Revoker is the documented no-op (see pkg/middleware/revoke.go).
	tokens := NewTokenService(repo, signer, 7*24*time.Hour, nil)
	return NewAuthService(repo, otp, tokens, "http://localhost:3000/reset-password"), repo, pool
}

// Each test gets its own identifier so tests never collide, and its rows are
// removed afterwards - no TRUNCATE, so a developer's own data survives.
func uniqueEmail(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	email := strings.ToLower(t.Name()) + "@test.local"
	del := func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, email)
		pool.Exec(context.Background(), `DELETE FROM otp_verification WHERE target = $1`, email)
		pool.Exec(context.Background(), `DELETE FROM login_attempts WHERE identifier = $1`, email)
	}
	del()
	t.Cleanup(del)
	return email
}

func str(s string) *string { return &s }

func code(t *testing.T, err error) string {
	t.Helper()
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	if err != nil {
		t.Fatalf("expected an apperr, got %v", err)
	}
	return ""
}

// registerAndVerify puts an account into the ACTIVE state the way a real user
// would: register, read the OTP back from the DB, verify it.
func registerAndVerify(t *testing.T, svc *AuthService, repo *repository.Repo, email, password string) {
	t.Helper()
	ctx := context.Background()
	if err := svc.Register(ctx, RegisterInput{
		FullName: "Test User", Email: str(email), Password: password,
	}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	raw := mintOtp(t, svc, repo, email, domain.OtpEmailVerification)
	if _, err := svc.VerifyOtp(ctx, email, raw, "127.0.0.1", "test"); err != nil {
		t.Fatal(err)
	}
}

// mintOtp replaces the stored OTP hash with one for a known code. The raw code
// is never persisted, so a test cannot read it back any other way.
func mintOtp(t *testing.T, svc *AuthService, repo *repository.Repo, target string, typ domain.OtpType) string {
	t.Helper()
	const known = "424242"
	o, err := repo.LatestOtp(context.Background(), target, typ)
	if err != nil {
		t.Fatal(err)
	}
	_, err = poolOf(t).Exec(context.Background(),
		`UPDATE otp_verification SET otp_code = $2, attempt_count = 0, verified = FALSE WHERE id = $1`,
		o.ID, hashCode(known))
	if err != nil {
		t.Fatal(err)
	}
	return known
}

var testPool *pgxpool.Pool

func poolOf(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testPool == nil {
		t.Fatal("pool not initialised")
	}
	return testPool
}

func TestMain(m *testing.M) {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		p, err := database.New(context.Background(), url)
		if err == nil {
			testPool = p
			defer p.Close()
		}
	}
	os.Exit(m.Run())
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	svc, _, pool := setup(t)
	email := uniqueEmail(t, pool)
	in := RegisterInput{FullName: "A", Email: str(email), Password: "Passw0rd!!"}

	if err := svc.Register(context.Background(), in, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if got := code(t, svc.Register(context.Background(), in, "127.0.0.1")); got != "EMAIL_EXISTS" {
		t.Fatalf("want EMAIL_EXISTS, got %q", got)
	}
}

func TestLoginBlockedUntilVerified(t *testing.T) {
	svc, _, pool := setup(t)
	email := uniqueEmail(t, pool)
	if err := svc.Register(context.Background(), RegisterInput{
		FullName: "A", Email: str(email), Password: "Passw0rd!!",
	}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Login(context.Background(), email, "Passw0rd!!", "127.0.0.1", "test")
	if got := code(t, err); got != "UNVERIFIED_ACCOUNT" {
		t.Fatalf("want UNVERIFIED_ACCOUNT, got %q", got)
	}
}

// A wrong password and an unknown identifier must be indistinguishable.
func TestLoginDoesNotRevealWhetherAccountExists(t *testing.T) {
	svc, repo, pool := setup(t)
	email := uniqueEmail(t, pool)
	registerAndVerify(t, svc, repo, email, "Passw0rd!!")

	_, errReal := svc.Login(context.Background(), email, "wrong-password", "127.0.0.1", "t")
	_, errFake := svc.Login(context.Background(), "no-such-user@test.local", "wrong-password", "127.0.0.1", "t")

	if code(t, errReal) != code(t, errFake) {
		t.Fatalf("error codes differ: %q vs %q", code(t, errReal), code(t, errFake))
	}
	if errReal.Error() != errFake.Error() {
		t.Fatalf("messages differ: %q vs %q", errReal, errFake)
	}
}

func TestAccountLocksAfterFiveFailures(t *testing.T) {
	svc, repo, pool := setup(t)
	email := uniqueEmail(t, pool)
	registerAndVerify(t, svc, repo, email, "Passw0rd!!")

	for i := 0; i < maxFailedAttempts; i++ {
		svc.Login(context.Background(), email, "wrong", "127.0.0.1", "t")
	}
	// The correct password must now be refused.
	_, err := svc.Login(context.Background(), email, "Passw0rd!!", "127.0.0.1", "t")
	if got := code(t, err); got != "ACCOUNT_LOCKED" {
		t.Fatalf("want ACCOUNT_LOCKED, got %q", got)
	}
}

// Regression: failed attempts against an already-locked account used to push
// account_locked_until further out on every try, letting an attacker keep a
// victim locked out forever.
func TestLockIsNotExtendedByFurtherFailures(t *testing.T) {
	svc, repo, pool := setup(t)
	email := uniqueEmail(t, pool)
	registerAndVerify(t, svc, repo, email, "Passw0rd!!")
	ctx := context.Background()

	for i := 0; i < maxFailedAttempts; i++ {
		svc.Login(ctx, email, "wrong", "127.0.0.1", "t")
	}
	u, err := repo.FindByIdentifier(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	first := *u.AccountLockedUntil

	for i := 0; i < 3; i++ {
		svc.Login(ctx, email, "wrong", "127.0.0.1", "t")
	}
	if u, err = repo.FindByIdentifier(ctx, email); err != nil {
		t.Fatal(err)
	}
	if !u.AccountLockedUntil.Equal(first) {
		t.Fatalf("lock was extended: %v -> %v", first, *u.AccountLockedUntil)
	}
}

// Regression: resetting the password is the recovery path for a locked-out
// user, so it must clear the lock. It previously left it in place, stranding
// the user for the full lockout window even with the new password.
func TestPasswordResetClearsLockout(t *testing.T) {
	svc, repo, pool := setup(t)
	email := uniqueEmail(t, pool)
	registerAndVerify(t, svc, repo, email, "Passw0rd!!")
	ctx := context.Background()

	for i := 0; i < maxFailedAttempts; i++ {
		svc.Login(ctx, email, "wrong", "127.0.0.1", "t")
	}
	if err := svc.ForgotPassword(ctx, email, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	token := mintOtp(t, svc, repo, email, domain.OtpPasswordReset)
	if err := svc.ResetPassword(ctx, email, token, "BrandNewPass1!"); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Login(ctx, email, "BrandNewPass1!", "127.0.0.1", "t"); err != nil {
		t.Fatalf("login after reset should succeed, got %v", err)
	}
}

func TestPasswordResetRevokesExistingSessions(t *testing.T) {
	svc, repo, pool := setup(t)
	email := uniqueEmail(t, pool)
	registerAndVerify(t, svc, repo, email, "Passw0rd!!")
	ctx := context.Background()

	res, err := svc.Login(ctx, email, "Passw0rd!!", "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.ForgotPassword(ctx, email, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	token := mintOtp(t, svc, repo, email, domain.OtpPasswordReset)
	if err := svc.ResetPassword(ctx, email, token, "BrandNewPass1!"); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Refresh(ctx, res.RefreshToken, "127.0.0.1", "t"); err == nil {
		t.Fatal("refresh token issued before the reset still works")
	}
}

func TestRefreshRotatesAndDetectsReuse(t *testing.T) {
	svc, repo, pool := setup(t)
	email := uniqueEmail(t, pool)
	registerAndVerify(t, svc, repo, email, "Passw0rd!!")
	ctx := context.Background()

	first, err := svc.Login(ctx, email, "Passw0rd!!", "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Refresh(ctx, first.RefreshToken, "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}

	// Replaying the burnt token is the theft signal.
	_, err = svc.Refresh(ctx, first.RefreshToken, "127.0.0.1", "t")
	if got := code(t, err); got != "TOKEN_REUSE_DETECTED" {
		t.Fatalf("want TOKEN_REUSE_DETECTED, got %q", got)
	}
	// ...and it must take the whole family down with it.
	if _, err := svc.Refresh(ctx, second.RefreshToken, "127.0.0.1", "t"); err == nil {
		t.Fatal("sessions were not revoked after reuse was detected")
	}
}

// Two concurrent refreshes with the same token must not both succeed, or a
// stolen token could be used alongside the victim's own session indefinitely.
func TestConcurrentRefreshHasExactlyOneWinner(t *testing.T) {
	svc, repo, pool := setup(t)
	email := uniqueEmail(t, pool)
	registerAndVerify(t, svc, repo, email, "Passw0rd!!")
	ctx := context.Background()

	res, err := svc.Login(ctx, email, "Passw0rd!!", "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}

	const racers = 8
	var wg sync.WaitGroup
	results := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = svc.Refresh(ctx, res.RefreshToken, "127.0.0.1", "t")
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("want exactly 1 successful refresh, got %d", wins)
	}
}

func TestOtpMaxAttemptsExceeded(t *testing.T) {
	svc, repo, pool := setup(t)
	email := uniqueEmail(t, pool)
	ctx := context.Background()
	if err := svc.Register(ctx, RegisterInput{
		FullName: "A", Email: str(email), Password: "Passw0rd!!",
	}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	mintOtp(t, svc, repo, email, domain.OtpEmailVerification)

	for i := 0; i < otpMaxAttempts; i++ {
		if _, err := svc.VerifyOtp(ctx, email, "000000", "127.0.0.1", "t"); err == nil {
			t.Fatal("a wrong OTP was accepted")
		}
	}
	_, err := svc.VerifyOtp(ctx, email, "424242", "127.0.0.1", "t")
	if got := code(t, err); got != "OTP_MAX_ATTEMPTS_EXCEEDED" {
		t.Fatalf("want OTP_MAX_ATTEMPTS_EXCEEDED, got %q", got)
	}
}

// forgot-password must look identical whether or not the account exists.
func TestForgotPasswordDoesNotRevealExistence(t *testing.T) {
	svc, _, pool := setup(t)
	_ = uniqueEmail(t, pool)
	if err := svc.ForgotPassword(context.Background(), "definitely-not-here@test.local", "127.0.0.1"); err != nil {
		t.Fatalf("want silent success for an unknown identifier, got %v", err)
	}
}
