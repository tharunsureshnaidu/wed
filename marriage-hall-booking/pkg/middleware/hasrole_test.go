package middleware

import (
	"context"
	"testing"
)

// Authorization must consult every role in the token, not just the primary
// one. An admin whose token lists ROLE_ADMIN second was refused access to
// facilities they are entitled to edit (requireOwner used Role, not HasRole).
func TestHasRoleChecksAllRoles(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxRole, "ROLE_HALL_OWNER")
	ctx = context.WithValue(ctx, ctxRoles, []string{"ROLE_HALL_OWNER", "ROLE_ADMIN"})

	if !HasRole(ctx, "ROLE_ADMIN") {
		t.Error("HasRole missed a non-primary role")
	}
	if !HasRole(ctx, "ROLE_HALL_OWNER") {
		t.Error("HasRole missed the primary role")
	}
	if HasRole(ctx, "ROLE_STAFF") {
		t.Error("HasRole matched a role the caller does not hold")
	}
	// Role() alone is the trap this test guards: it sees only the first.
	if Role(ctx) == "ROLE_ADMIN" {
		t.Error("test setup wrong - primary should not be ROLE_ADMIN")
	}
}
