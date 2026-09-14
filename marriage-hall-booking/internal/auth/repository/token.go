package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
)

// --- OTP ---

func (r *Repo) LatestOtp(ctx context.Context, target string, t domain.OtpType) (*domain.Otp, error) {
	var o domain.Otp
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, otp_code, otp_type, target, expires_at, verified, attempt_count, created_at
		 FROM otp_verification WHERE target = $1 AND otp_type = $2
		 ORDER BY id DESC LIMIT 1`, target, t).
		Scan(&o.ID, &o.UserID, &o.OtpCode, &o.OtpType, &o.Target,
			&o.ExpiresAt, &o.Verified, &o.AttemptCount, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &o, err
}

func (r *Repo) CountOtpSince(ctx context.Context, target string, t domain.OtpType, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM otp_verification
		 WHERE target = $1 AND otp_type = $2 AND created_at > $3`, target, t, since).Scan(&n)
	return n, err
}

// CreateOtp invalidates any outstanding unverified OTP for this target/type and
// inserts the new one, so only the newest code is ever usable.
func (r *Repo) CreateOtp(ctx context.Context, o *domain.Otp, ip string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE otp_verification SET verified = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE target = $1 AND otp_type = $2 AND verified = FALSE`, o.Target, o.OtpType); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO otp_verification (user_id, otp_code, otp_type, target, expires_at, ip_address)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		o.UserID, o.OtpCode, o.OtpType, o.Target, o.ExpiresAt, ip); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repo) BumpOtpAttempt(ctx context.Context, id int64, verified bool) error {
	_, err := r.db.Exec(ctx,
		`UPDATE otp_verification SET attempt_count = attempt_count + 1, verified = $2,
		    updated_at = CURRENT_TIMESTAMP WHERE id = $1`, id, verified)
	return err
}

// --- Refresh tokens ---

func (r *Repo) CreateRefreshToken(ctx context.Context, userID int64, hash, ip, device string, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, device_info, ip_address, expires_at)
		 VALUES ($1, $2, $3, $4, $5)`, userID, hash, device, ip, expiresAt)
	return err
}

func (r *Repo) FindRefreshToken(ctx context.Context, hash string) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at, revoked FROM refresh_tokens
		 WHERE token_hash = $1`, hash).
		Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.Revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

// RevokeRefreshToken flips revoked only if it is currently false, and reports
// whether it actually did. Two concurrent refreshes with the same token then
// have exactly one winner - the loser sees false and is treated as reuse.
func (r *Repo) RevokeRefreshToken(ctx context.Context, id int64) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND revoked = FALSE`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// RevokeRefreshTokenByHash revokes one token and returns whose it was, so the
// caller can also kill that user's access tokens. Returns 0 if no such token.
func (r *Repo) RevokeRefreshTokenByHash(ctx context.Context, hash string) (int64, error) {
	var userID int64
	err := r.db.QueryRow(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE token_hash = $1 RETURNING user_id`, hash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return userID, err
}

func (r *Repo) RevokeAllUserTokens(ctx context.Context, userID int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE user_id = $1 AND revoked = FALSE`, userID)
	return err
}
