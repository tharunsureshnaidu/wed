package domain

import "time"

type UserStatus string

const (
	StatusActive              UserStatus = "ACTIVE"
	StatusInactive            UserStatus = "INACTIVE"
	StatusSuspended           UserStatus = "SUSPENDED"
	StatusPendingVerification UserStatus = "PENDING_VERIFICATION"
)

type OtpType string

const (
	OtpEmailVerification OtpType = "EMAIL_VERIFICATION"
	OtpPhoneVerification OtpType = "PHONE_VERIFICATION"
	OtpPasswordReset     OtpType = "PASSWORD_RESET"
	OtpLogin2FA          OtpType = "LOGIN_2FA"
)

func (t OtpType) Valid() bool {
	switch t {
	case OtpEmailVerification, OtpPhoneVerification, OtpPasswordReset, OtpLogin2FA:
		return true
	}
	return false
}

const (
	RoleCustomer  = "ROLE_CUSTOMER"
	RoleHallOwner = "ROLE_HALL_OWNER"
	RoleAdmin     = "ROLE_ADMIN"
	RoleStaff     = "ROLE_STAFF"
)

type User struct {
	ID                  int64
	FullName            string
	Email               *string
	PhoneNumber         *string
	PasswordHash        string
	IsEmailVerified     bool
	IsPhoneVerified     bool
	Status              UserStatus
	FailedLoginAttempts int
	AccountLockedUntil  *time.Time
	LastLoginAt         *time.Time
	IsDeleted           bool
	Roles               []string
}

// Username is the JWT subject: email if present, else phone. Mirrors the Java
// UserDetails principal so tokens issued by either service mean the same thing.
func (u *User) Username() string {
	if u.Email != nil && *u.Email != "" {
		return *u.Email
	}
	if u.PhoneNumber != nil {
		return *u.PhoneNumber
	}
	return ""
}

func (u *User) PrimaryRole() string {
	if len(u.Roles) == 0 {
		return RoleCustomer
	}
	return u.Roles[0]
}

// AllRoles is what goes into the token. A user with no roles still gets
// ROLE_CUSTOMER so authorization always has something concrete to check.
func (u *User) AllRoles() []string {
	if len(u.Roles) == 0 {
		return []string{RoleCustomer}
	}
	return u.Roles
}

type Otp struct {
	ID           int64
	UserID       *int64
	OtpCode      string // SHA-256 hash, never the raw code
	OtpType      OtpType
	Target       string
	ExpiresAt    time.Time
	Verified     bool
	AttemptCount int
	CreatedAt    time.Time
}

type RefreshToken struct {
	ID        int64
	UserID    int64
	TokenHash string
	ExpiresAt time.Time
	Revoked   bool
}
