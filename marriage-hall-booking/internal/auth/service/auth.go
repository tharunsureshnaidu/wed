package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
)

const (
	bcryptCost         = 12 // matches the Java BCryptPasswordEncoder(12)
	maxFailedAttempts  = 5
	lockoutDuration    = 15 * time.Minute
	invalidCredentials = "Invalid credentials"
)

// decoyHash is compared against on the user-not-found login path so that path
// costs the same bcrypt work as a real one. Without it, "no such user" returns
// in microseconds while "wrong password" takes ~100ms, and that timing gap alone
// tells an attacker which identifiers are registered.
var decoyHash []byte

func init() {
	decoyHash, _ = bcrypt.GenerateFromPassword([]byte("decoy-password"), bcryptCost)
}

type AuthService struct {
	repo     *repository.Repo
	otp      *OtpService
	tokens   *TokenService
	resetURL string
	// OnUserCreated creates the matching user_profiles row. A func rather than a
	// package dependency so auth does not import the user module.
	OnUserCreated func(ctx context.Context, userID int64, firstName string, lastName *string) error
}

func NewAuthService(repo *repository.Repo, otp *OtpService, tokens *TokenService, resetURL string) *AuthService {
	return &AuthService{repo: repo, otp: otp, tokens: tokens, resetURL: resetURL}
}

type AuthResult struct {
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken"`
	User         UserView `json:"user"`
}

type UserView struct {
	ID          int64    `json:"id"`
	FullName    string   `json:"fullName"`
	Email       *string  `json:"email"`
	PhoneNumber *string  `json:"phoneNumber"`
	Status      string   `json:"status"`
	Roles       []string `json:"roles"`
}

type RegisterInput struct {
	FullName    string
	Email       *string
	PhoneNumber *string
	Password    string
}

func (s *AuthService) Register(ctx context.Context, in RegisterInput, ip string) error {
	return s.createAccount(ctx, in, ip, domain.RoleCustomer)
}

func (s *AuthService) RegisterVendor(ctx context.Context, in RegisterInput, ip string) error {
	return s.createAccount(ctx, in, ip, domain.RoleHallOwner)
}

func (s *AuthService) createAccount(ctx context.Context, in RegisterInput, ip, role string) error {
	if in.Email != nil {
		exists, err := s.repo.ExistsByEmail(ctx, *in.Email)
		if err != nil {
			return err
		}
		if exists {
			return apperr.Conflict("EMAIL_EXISTS", "Email already exists")
		}
	}
	if in.PhoneNumber != nil {
		exists, err := s.repo.ExistsByPhone(ctx, *in.PhoneNumber)
		if err != nil {
			return err
		}
		if exists {
			return apperr.Conflict("PHONE_EXISTS", "Phone number already exists")
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
	if err != nil {
		return err
	}

	id, err := s.repo.CreateUser(ctx, &domain.User{
		FullName:     in.FullName,
		Email:        in.Email,
		PhoneNumber:  in.PhoneNumber,
		PasswordHash: string(hash),
		Status:       domain.StatusPendingVerification,
	}, role)
	if err != nil {
		return err
	}

	if s.OnUserCreated != nil {
		first, last := splitName(in.FullName)
		if err := s.OnUserCreated(ctx, id, first, last); err != nil {
			logger.Error("create profile", "userId", id, logger.Err(err))
		}
	}

	// Best-effort: a failed OTP send must not roll back a created account -
	// the user can always hit /otp/resend.
	if in.Email != nil {
		if _, err := s.otp.Generate(ctx, &id, *in.Email, domain.OtpEmailVerification, ip); err != nil {
			logger.Warn("email OTP not sent", "userId", id, logger.Err(err))
		}
	}
	if in.PhoneNumber != nil {
		if _, err := s.otp.Generate(ctx, &id, *in.PhoneNumber, domain.OtpPhoneVerification, ip); err != nil {
			logger.Warn("phone OTP not sent", "userId", id, logger.Err(err))
		}
	}
	return nil
}

// Login checks the password before any account-state check, so that lock,
// suspension and verification states are only learnable by someone who already
// proved they know the password.
func (s *AuthService) Login(ctx context.Context, identifier, password, ip, device string) (*AuthResult, error) {
	u, err := s.repo.FindByIdentifier(ctx, identifier)
	if errors.Is(err, repository.ErrNotFound) {
		bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
		s.recordFailure(ctx, identifier, ip, "User not found")
		return nil, apperr.Unauthorized("INVALID_CREDENTIALS", invalidCredentials)
	}
	if err != nil {
		return nil, err
	}

	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		s.recordFailure(ctx, identifier, ip, "Invalid password")
		return nil, apperr.Unauthorized("INVALID_CREDENTIALS", invalidCredentials)
	}

	if u.AccountLockedUntil != nil {
		if u.AccountLockedUntil.After(time.Now()) {
			return nil, apperr.New(423, "ACCOUNT_LOCKED", "Account is locked")
		}
		// Lock elapsed - clear it so the counter starts fresh.
		if err := s.repo.ClearExpiredLock(ctx, u.ID); err != nil {
			return nil, err
		}
	}
	if u.IsDeleted {
		return nil, apperr.Forbidden("ACCOUNT_DELETED", "Account has been deleted")
	}
	if u.Status == domain.StatusSuspended {
		return nil, apperr.Forbidden("ACCOUNT_SUSPENDED", "Account is suspended")
	}
	if u.Status == domain.StatusPendingVerification {
		return nil, apperr.Forbidden("UNVERIFIED_ACCOUNT", "Account not verified")
	}

	if err := s.repo.RecordSuccessfulAttempt(ctx, identifier, ip); err != nil {
		return nil, err
	}
	return s.issue(ctx, u, ip, device)
}

func (s *AuthService) recordFailure(ctx context.Context, identifier, ip, reason string) {
	if err := s.repo.RecordFailedAttempt(ctx, identifier, ip, reason, maxFailedAttempts, lockoutDuration); err != nil {
		logger.Error("record failed login attempt", logger.Err(err))
	}
}

// VerifyOtp verifies an email or phone code and auto-logs the user in, matching
// the Java /register/verify-email behaviour.
func (s *AuthService) VerifyOtp(ctx context.Context, target, code, ip, device string) (*AuthResult, error) {
	isEmail := strings.Contains(target, "@")
	otpType := domain.OtpPhoneVerification
	if isEmail {
		otpType = domain.OtpEmailVerification
	}
	if err := s.otp.Verify(ctx, target, otpType, code); err != nil {
		return nil, err
	}

	u, err := s.repo.FindByIdentifier(ctx, target)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, apperr.BadRequest("USER_NOT_FOUND", "No account for that target")
	}
	if err != nil {
		return nil, err
	}
	if err := s.repo.MarkVerified(ctx, u.ID, isEmail); err != nil {
		return nil, err
	}
	// Re-read so the response carries the post-verification status.
	if u, err = s.repo.FindByID(ctx, u.ID); err != nil {
		return nil, err
	}
	return s.issue(ctx, u, ip, device)
}

// ResendOtp is deliberately silent about whether the identifier exists.
func (s *AuthService) ResendOtp(ctx context.Context, target string, t domain.OtpType, ip string) error {
	u, err := s.repo.FindByIdentifier(ctx, target)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.otp.Generate(ctx, &u.ID, target, t, ip)
	return err
}

func (s *AuthService) Refresh(ctx context.Context, raw, ip, device string) (*AuthResult, error) {
	userID, err := s.tokens.Rotate(ctx, raw)
	if err != nil {
		return nil, err
	}
	u, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.Status != domain.StatusActive || u.IsDeleted {
		return nil, apperr.Forbidden("USER_INACTIVE", "User account is inactive or disabled")
	}
	return s.issue(ctx, u, ip, device)
}

func (s *AuthService) Logout(ctx context.Context, raw string) error {
	return s.tokens.Revoke(ctx, raw)
}

func (s *AuthService) LogoutAllDevices(ctx context.Context, userID int64) error {
	return s.tokens.RevokeAll(ctx, userID)
}

// ForgotPassword always reports success to the caller; only a real account
// actually gets a link, so this cannot be used to enumerate identifiers.
func (s *AuthService) ForgotPassword(ctx context.Context, identifier, ip string) error {
	u, err := s.repo.FindByIdentifier(ctx, identifier)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	token, err := s.otp.GenerateResetToken(ctx, &u.ID, identifier, ip)
	if err != nil {
		// Cooldown/limit errors must not leak either - the caller still sees success.
		logger.Warn("password reset token not issued", "identifier", identifier, logger.Err(err))
		return nil
	}
	logger.Warn("password reset link logged instead of emailed",
		"identifier", identifier,
		"link", fmt.Sprintf("%s?identifier=%s&token=%s", s.resetURL, url.QueryEscape(identifier), token))
	return nil
}

// ResetPassword collapses every failure into one generic error: which of
// expired/used/not-found applies is information an attacker should not get.
func (s *AuthService) ResetPassword(ctx context.Context, identifier, token, newPassword string) error {
	invalid := apperr.BadRequest("INVALID_RESET_TOKEN", "Invalid or expired reset link")

	if err := s.otp.Verify(ctx, identifier, domain.OtpPasswordReset, token); err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return invalid
		}
		return err
	}
	u, err := s.repo.FindByIdentifier(ctx, identifier)
	if errors.Is(err, repository.ErrNotFound) {
		return invalid
	}
	if err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return err
	}
	if err := s.repo.UpdatePassword(ctx, u.ID, string(hash)); err != nil {
		return err
	}
	// A password reset ends every existing session, not just future logins.
	return s.tokens.RevokeAll(ctx, u.ID)
}

func (s *AuthService) Me(ctx context.Context, userID int64) (*UserView, error) {
	u, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	v := view(u)
	return &v, nil
}

func (s *AuthService) issue(ctx context.Context, u *domain.User, ip, device string) (*AuthResult, error) {
	access, refresh, err := s.tokens.Issue(ctx, u, ip, device)
	if err != nil {
		return nil, err
	}
	return &AuthResult{AccessToken: access, RefreshToken: refresh, User: view(u)}, nil
}

func view(u *domain.User) UserView {
	roles := u.Roles
	if roles == nil {
		roles = []string{}
	}
	return UserView{
		ID: u.ID, FullName: u.FullName, Email: u.Email,
		PhoneNumber: u.PhoneNumber, Status: string(u.Status), Roles: roles,
	}
}

// splitName splits the single fullName field on the first space so the profile
// row starts with a sensible first/last name rather than everything in first.
func splitName(fullName string) (string, *string) {
	trimmed := strings.TrimSpace(fullName)
	first, rest, found := strings.Cut(trimmed, " ")
	if !found {
		return trimmed, nil
	}
	rest = strings.TrimSpace(rest)
	return first, &rest
}
