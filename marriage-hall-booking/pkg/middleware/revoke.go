package middleware

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
)

// Revoker makes logout apply to access tokens too.
//
// An access token is verified by its signature alone - no database read - so
// nothing about it can be cancelled once issued. Without this, "log out" only
// stopped new tokens being issued and the one already in the client's hands
// kept working until it expired (with JWT_EXPIRATION_MS at 7 days, for a week).
//
// ponytail: one "not before" timestamp per user rather than a blocklist of
// individual tokens. Logout stamps the current time; any token issued earlier
// is refused. That is a single Redis GET on the authenticated path and needs no
// per-token bookkeeping. The cost is that it logs the user out everywhere at
// once, which is exactly the intended behaviour here.
type Revoker struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewRevoker(rdb *redis.Client, accessTTL time.Duration) *Revoker {
	// The stamp only has to outlive the longest-lived token that could still
	// present itself; after that every such token has expired on its own.
	return &Revoker{rdb: rdb, ttl: accessTTL + time.Hour}
}

func key(userID int64) string { return "revoked_before:" + strconv.FormatInt(userID, 10) }

// RevokeAll invalidates every access token issued to this user up to now.
//
// Milliseconds, not seconds: a logout and the login that follows it routinely
// land in the same second, and a second-resolution cutoff cannot separate them
// - it either lets the logged-out token live or kills the new one. Revoked
// compares with <=, so a token minted in the same millisecond dies too; the
// next login is admitted by WaitPast, not by leaving a gap here.
func (v *Revoker) RevokeAll(ctx context.Context, userID int64) error {
	if v == nil || v.rdb == nil {
		return nil
	}
	return v.rdb.Set(ctx, key(userID), time.Now().UnixMilli(), v.ttl).Err()
}

// maxWait bounds WaitPast. The cutoff is stamped with time.Now on some API
// host, so normally it is at most a millisecond ahead; more means clock skew
// between hosts, and a login must not hang on it.
const maxWait = time.Second

// WaitPast returns once the clock is strictly past this user's cutoff, so a
// token minted next is accepted while every token minted at or before the
// cutoff stays dead.
//
// The cutoff is never deleted: an earlier version cleared it on every login,
// which brought every stolen, unexpired token back to life the moment the
// victim signed in again after a password reset or a theft-detection revoke.
// The key expires on its own once no token from before it can still be valid.
func (v *Revoker) WaitPast(ctx context.Context, userID int64) {
	if v == nil || v.rdb == nil {
		return
	}
	cutoff, err := v.rdb.Get(ctx, key(userID)).Int64()
	if err != nil {
		return // no cutoff, or Redis down: Revoked fails open the same way
	}
	if d := time.Duration(cutoff+1-time.Now().UnixMilli()) * time.Millisecond; d > 0 {
		time.Sleep(min(d, maxWait))
	}
}

// Revoked reports whether a token issued at iatMs has since been logged out.
//
// Fails open: if Redis is unreachable the request proceeds on its signature
// alone, as it did before this existed. An outage must not lock every user out
// of the API, and the refresh token - which is checked against Postgres - still
// gives real revocation. It is logged at Error, because while it lasts a
// logged-out or blocked user's access token works again.
func (v *Revoker) Revoked(ctx context.Context, userID, iatMs int64) bool {
	if v == nil || v.rdb == nil {
		return false
	}
	cutoff, err := v.rdb.Get(ctx, key(userID)).Int64()
	if errors.Is(err, redis.Nil) {
		return false
	}
	if err != nil {
		logger.Error("revocation check failed open", "userId", userID, logger.Err(err))
		return false
	}
	return iatMs <= cutoff
}

// RevokeAccessTokens ends every session for a user through the revoker that was
// installed at start-up.
//
// Blocking an account has to cut off the tokens already issued, not only stop
// new ones - otherwise a suspended user carries on working until their access
// token expires. Package-level because the admin handler has no reason to hold
// a Revoker of its own.
func RevokeAccessTokens(ctx context.Context, userID int64) {
	if revoker != nil {
		_ = revoker.RevokeAll(ctx, userID)
	}
}
