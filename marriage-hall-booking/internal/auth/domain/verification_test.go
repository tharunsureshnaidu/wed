package domain

import "testing"

// Verification gates vendors, not customers. A customer locked out of the
// account they just created is a customer lost; a vendor publishes a contact
// real customers rely on, so theirs is confirmed first.
func TestOnlyVendorsNeedVerifying(t *testing.T) {
	cases := []struct {
		name    string
		roles   []string
		email   bool
		phone   bool
		blocked bool
	}{
		{"unverified customer logs in", []string{RoleCustomer}, false, false, false},
		{"verified customer logs in", []string{RoleCustomer}, true, false, false},
		{"unverified vendor is blocked", []string{RoleHallOwner}, false, false, true},
		{"vendor verified by email", []string{RoleHallOwner}, true, false, false},
		// A vendor who signed up by phone has no email to confirm; demanding
		// both would lock them out of their own listing forever.
		{"vendor verified by phone", []string{RoleHallOwner}, false, true, false},
		{"no roles defaults to customer", nil, false, false, false},
	}
	for _, c := range cases {
		u := &User{Roles: c.roles, IsEmailVerified: c.email, IsPhoneVerified: c.phone}
		blocked := u.HasRole(RoleHallOwner) && !u.IsVerified()
		if blocked != c.blocked {
			t.Errorf("%s: blocked = %v, want %v", c.name, blocked, c.blocked)
		}
	}
}

func TestHasRole(t *testing.T) {
	u := &User{Roles: []string{RoleAdmin, RoleCustomer}}
	for _, r := range []string{RoleAdmin, RoleCustomer} {
		if !u.HasRole(r) {
			t.Errorf("HasRole(%q) = false, want true", r)
		}
	}
	if u.HasRole(RoleHallOwner) {
		t.Error("HasRole(ROLE_HALL_OWNER) = true, want false")
	}
	// An account with no roles is treated as a customer, so authorization
	// always has something concrete to check.
	if !(&User{}).HasRole(RoleCustomer) {
		t.Error("a roleless account should read as a customer")
	}
}
