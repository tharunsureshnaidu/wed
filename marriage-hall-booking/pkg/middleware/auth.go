package middleware

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/tripfcatory/marriage-hall-booking/pkg/jwt"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
)

type ctxKey int

const (
	ctxUserID ctxKey = iota
	ctxRole
	ctxRoles
	ctxUsername
)

func UserID(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(ctxUserID).(int64)
	return v, ok
}

func Role(ctx context.Context) string {
	v, _ := ctx.Value(ctxRole).(string)
	return v
}

// HasRole reports whether the caller holds a role. Authorization must consult
// every role in the token, not only the primary one.
func HasRole(ctx context.Context, want string) bool {
	if Role(ctx) == want {
		return true
	}
	roles, _ := ctx.Value(ctxRoles).([]string)
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

func Username(ctx context.Context) string {
	v, _ := ctx.Value(ctxUsername).(string)
	return v
}

// revoker is set once at start-up by SetRevoker. It is a package-level value
// so RequireAuth keeps its signature - every module already calls it, and
// threading a new argument through all ten of them buys nothing.
var revoker *Revoker

// SetRevoker installs the logout check. Called once from main; without it
// RequireAuth behaves exactly as it did before.
func SetRevoker(v *Revoker) { revoker = v }

// RequireAuth rejects anything without a valid, unexpired Bearer token.
func RequireAuth(signer *jwt.Signer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
				return
			}
			claims, err := signer.Parse(strings.TrimPrefix(header, "Bearer "))
			if err != nil {
				response.Error(w, http.StatusUnauthorized, "Invalid or expired token", "INVALID_TOKEN")
				return
			}
			id, err := strconv.ParseInt(claims.UserID, 10, 64)
			if err != nil {
				response.Error(w, http.StatusUnauthorized, "Invalid token subject", "INVALID_TOKEN")
				return
			}
			// A token minted before this user last logged out is dead, even
			// though its signature and expiry are still good.
			if revoker.Revoked(r.Context(), id, claims.IssuedMs) {
				response.Error(w, http.StatusUnauthorized, "Session ended, please log in again", "TOKEN_REVOKED")
				return
			}
			ctx := context.WithValue(r.Context(), ctxUserID, id)
			ctx = context.WithValue(ctx, ctxRole, claims.Role)
			ctx = context.WithValue(ctx, ctxRoles, claims.Roles)
			ctx = context.WithValue(ctx, ctxUsername, claims.Subject)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole is the @PreAuthorize equivalent. Must be chained after RequireAuth.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			granted := false
			for role := range allowed {
				if HasRole(r.Context(), role) {
					granted = true
					break
				}
			}
			if !granted {
				response.Error(w, http.StatusForbidden, "Insufficient permissions", "FORBIDDEN")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Chain applies middleware left-to-right: Chain(h, a, b) runs a, then b, then h.
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}
