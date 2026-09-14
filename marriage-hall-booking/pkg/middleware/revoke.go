package middleware

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
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
// next login is admitted by ClearFor, not by leaving a gap here.
func (v *Revoker) RevokeAll(ctx context.Context, userID int64) error {
	if v == nil || v.rdb == nil {
		return nil
	}
	return v.rdb.Set(ctx, key(userID), time.Now().UnixMilli(), v.ttl).Err()
}

// ClearFor drops the cutoff, so tokens issued from this moment are accepted.
// Called when a session is deliberately created - a successful login proves the
// password and must not be undone by an earlier logout.
func (v *Revoker) ClearFor(ctx context.Context, userID int64) error {
	if v == nil || v.rdb == nil {
		return nil
	}
	return v.rdb.Del(ctx, key(userID)).Err()
}

// Revoked reports whether a token issued at iatMs has since been logged out.
//
// Fails open: if Redis is unreachable the request proceeds on its signature
// alone, as it did before this existed. An outage must not lock every user out
// of the API, and the refresh token - which is checked against Postgres - still
// gives real revocation.
func (v *Revoker) Revoked(ctx context.Context, userID, iatMs int64) bool {
	if v == nil || v.rdb == nil {
		return false
	}
	cutoff, err := v.rdb.Get(ctx, key(userID)).Int64()
	if err != nil {
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
