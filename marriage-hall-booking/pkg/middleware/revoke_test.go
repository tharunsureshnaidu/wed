package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Logout must end the session immediately, and must not prevent the user from
// logging back in. Both halves have been broken here before: a cutoff stamped
// a second into the future rejected the next login's token, and a
// second-resolution cutoff let a token minted in the same second as the logout
// survive it.
func TestRevokeThenReLogin(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("redis unavailable")
	}
	const uid = -424242 // negative: cannot collide with a real user id
	t.Cleanup(func() { rdb.Del(ctx, key(uid)) })

	v := NewRevoker(rdb, 15*time.Minute)

	issued := time.Now().UnixMilli()
	if v.Revoked(ctx, uid, issued) {
		t.Fatal("token revoked before any logout")
	}

	if err := v.RevokeAll(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if !v.Revoked(ctx, uid, issued) {
		t.Error("token issued before logout is still accepted")
	}

	// A new session clears the cutoff; its token must be accepted even though
	// it is issued in the same millisecond range as the logout.
	if err := v.ClearFor(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if v.Revoked(ctx, uid, time.Now().UnixMilli()) {
		t.Error("token from a fresh login rejected - user cannot log back in")
	}
}

// One user's logout must never touch another's session.
func TestRevokeIsPerUser(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("redis unavailable")
	}
	const a, b = -424243, -424244
	t.Cleanup(func() { rdb.Del(ctx, key(a), key(b)) })

	v := NewRevoker(rdb, 15*time.Minute)
	issued := time.Now().UnixMilli()
	if err := v.RevokeAll(ctx, a); err != nil {
		t.Fatal(err)
	}
	if v.Revoked(ctx, b, issued) {
		t.Error("logging out one user revoked another user's token")
	}
}

// Redis being down must not lock everyone out of the API.
func TestRevokeFailsOpen(t *testing.T) {
	v := NewRevoker(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), time.Minute)
	if v.Revoked(context.Background(), 1, time.Now().UnixMilli()) {
		t.Error("unreachable redis rejected a valid token")
	}
	var nilRevoker *Revoker
	if nilRevoker.Revoked(context.Background(), 1, 0) {
		t.Error("nil revoker rejected a valid token")
	}
}
