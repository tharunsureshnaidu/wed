package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
)

type TokenService struct {
	repo       *repository.Repo
	signer     *jwt.Signer
	refreshTTL time.Duration
	revoker    *middleware.Revoker
}

func NewTokenService(repo *repository.Repo, signer *jwt.Signer, refreshTTL time.Duration, revoker *middleware.Revoker) *TokenService {
	return &TokenService{repo: repo, signer: signer, refreshTTL: refreshTTL, revoker: revoker}
}

// Issue returns a fresh access/refresh pair. The refresh token is returned raw
// to the caller but only its SHA-256 hash is stored, so a database leak alone
// does not yield usable tokens.
func (s *TokenService) Issue(ctx context.Context, u *domain.User, ip, device string) (access, refresh string, err error) {
	// Every session starts here - login, OTP verification and refresh alike -
	// so this is where an earlier logout's cutoff must be lifted. Without it a
	// user who logs out can never log back in: their new token's iat would
	// still sit at or before the cutoff.
	if s.revoker != nil {
		if err := s.revoker.ClearFor(ctx, u.ID); err != nil {
			logger.Warn("could not clear revocation cutoff", "userId", u.ID, logger.Err(err))
		}
	}

	access, err = s.signer.Generate(u.Username(), strconv.FormatInt(u.ID, 10), u.AllRoles()...)
	if err != nil {
		return "", "", err
	}

	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	refresh = base64.RawURLEncoding.EncodeToString(buf)

	err = s.repo.CreateRefreshToken(ctx, u.ID, hashCode(refresh), ip, device,
		time.Now().Add(s.refreshTTL))
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// Rotate validates a refresh token and burns it, returning the owning user id.
//
// A token presented after it was already revoked means the same token was used
// twice: either it was stolen, or the legitimate holder's rotation was replayed.
// Either way every session for that user is revoked, per the original design's
// reuse-detection requirement.
func (s *TokenService) Rotate(ctx context.Context, raw string) (int64, error) {
	t, err := s.repo.FindRefreshToken(ctx, hashCode(raw))
	if errors.Is(err, repository.ErrNotFound) {
		return 0, apperr.Unauthorized("INVALID_REFRESH_TOKEN", "Invalid refresh token")
	}
	if err != nil {
		return 0, err
	}
	if time.Now().After(t.ExpiresAt) {
		return 0, apperr.Unauthorized("REFRESH_TOKEN_EXPIRED", "Refresh token is expired")
	}

	// Claim the token. The UPDATE only matches while revoked is still false, so
	// of two concurrent refreshes exactly one wins; the other falls through to
	// reuse detection rather than both minting new sessions.
	claimed, err := s.repo.RevokeRefreshToken(ctx, t.ID)
	if err != nil {
		return 0, err
	}
	if !claimed {
		// RevokeAll, not just the refresh rows: whoever replayed the token may
		// already hold an access token from it, and that must die now rather
		// than at expiry.
		if err := s.RevokeAll(ctx, t.UserID); err != nil {
			return 0, err
		}
		return 0, apperr.Unauthorized("TOKEN_REUSE_DETECTED",
			"Token reuse detected. All sessions revoked. Please login again.")
	}
	return t.UserID, nil
}

// Revoke ends the session the refresh token belongs to - the access tokens
// included. Revoking only the refresh token would leave the caller logged in
// until their current access token expired.
func (s *TokenService) Revoke(ctx context.Context, raw string) error {
	userID, err := s.repo.RevokeRefreshTokenByHash(ctx, hashCode(raw))
	if err != nil {
		return err
	}
	if userID == 0 {
		return nil // unknown token: nothing to end, and nothing to leak either
	}
	return s.killAccessTokens(ctx, userID)
}

func (s *TokenService) RevokeAll(ctx context.Context, userID int64) error {
	if err := s.repo.RevokeAllUserTokens(ctx, userID); err != nil {
		return err
	}
	return s.killAccessTokens(ctx, userID)
}

// killAccessTokens is best-effort: the refresh token is already revoked in
// Postgres, so a Redis outage degrades logout to its previous behaviour rather
// than failing the request and leaving the user unsure whether they logged out.
func (s *TokenService) killAccessTokens(ctx context.Context, userID int64) error {
	if s.revoker == nil {
		return nil
	}
	if err := s.revoker.RevokeAll(ctx, userID); err != nil {
		logger.Warn("could not revoke access tokens", "userId", userID, logger.Err(err))
	}
	return nil
}
