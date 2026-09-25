package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
)

var ErrNotFound = errors.New("not found")

type Repo struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Repo { return &Repo{db: db} }

const userCols = `u.id, u.full_name, u.email, u.phone_number, u.password_hash,
	u.is_email_verified, u.is_phone_verified, u.status, u.failed_login_attempts,
	u.account_locked_until, u.last_login_at, u.is_deleted`

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.FullName, &u.Email, &u.PhoneNumber, &u.PasswordHash,
		&u.IsEmailVerified, &u.IsPhoneVerified, &u.Status, &u.FailedLoginAttempts,
		&u.AccountLockedUntil, &u.LastLoginAt, &u.IsDeleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// FindByIdentifier matches the Java findByEmailIgnoreCase().or(findByPhoneNumber()).
func (r *Repo) FindByIdentifier(ctx context.Context, identifier string) (*domain.User, error) {
	u, err := scanUser(r.db.QueryRow(ctx,
		`SELECT `+userCols+` FROM users u
		 WHERE LOWER(u.email) = LOWER($1) OR u.phone_number = $1
		 ORDER BY (LOWER(u.email) = LOWER($1)) DESC LIMIT 1`, identifier))
	if err != nil {
		return nil, err
	}
	return r.withRoles(ctx, u)
}

func (r *Repo) FindByID(ctx context.Context, id int64) (*domain.User, error) {
	u, err := scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users u WHERE u.id = $1`, id))
	if err != nil {
		return nil, err
	}
	return r.withRoles(ctx, u)
}

func (r *Repo) withRoles(ctx context.Context, u *domain.User) (*domain.User, error) {
	rows, err := r.db.Query(ctx,
		`SELECT r.role_name FROM user_roles ur
		 JOIN roles r ON r.id = ur.role_id WHERE ur.user_id = $1 ORDER BY r.id`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		u.Roles = append(u.Roles, name)
	}
	return u, rows.Err()
}

func (r *Repo) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(email) = LOWER($1))`, email).Scan(&ok)
	return ok, err
}

func (r *Repo) ExistsByPhone(ctx context.Context, phone string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE phone_number = $1)`, phone).Scan(&ok)
	return ok, err
}

// CreateUser inserts the user and its role in one transaction, so a user can
// never exist without the role that authorizes it.
func (r *Repo) CreateUser(ctx context.Context, u *domain.User, roleName string) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO users (full_name, email, phone_number, password_hash, status)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		u.FullName, u.Email, u.PhoneNumber, u.PasswordHash, u.Status).Scan(&id)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO user_roles (user_id, role_id)
		 SELECT $1, id FROM roles WHERE role_name = $2`, id, roleName); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

func (r *Repo) MarkVerified(ctx context.Context, userID int64, isEmail bool) error {
	col := "is_phone_verified"
	if isEmail {
		col = "is_email_verified"
	}
	_, err := r.db.Exec(ctx,
		`UPDATE users SET `+col+` = TRUE,
		    status = CASE WHEN status = 'PENDING_VERIFICATION' THEN 'ACTIVE' ELSE status END,
		    updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1`, userID)
	return err
}

// UpdatePassword also clears any lockout. Resetting the password is the
// intended recovery path for a locked-out user, so leaving the lock in place
// would strand them for the full lockout window even after proving ownership
// of the account via the emailed reset token.
func (r *Repo) UpdatePassword(ctx context.Context, userID int64, hash string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET password_hash = $2, failed_login_attempts = 0,
		    account_locked_until = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1`, userID, hash)
	return err
}

// RecordFailedAttempt logs the attempt and increments the counter, locking the
// account once it reaches maxAttempts. Done in one statement so two concurrent
// failed logins can't both read the same counter and lose an increment.
func (r *Repo) RecordFailedAttempt(ctx context.Context, identifier, ip, reason string,
	maxAttempts int, lockFor time.Duration) error {

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO login_attempts (identifier, ip_address, success, failure_reason)
		 VALUES ($1, $2, FALSE, $3)`, identifier, ip, reason); err != nil {
		return err
	}
	// Only sets the lock when the threshold is first crossed. Without the
	// "IS NULL OR expired" guard, every failed attempt against an already-locked
	// account would push account_locked_until further out, letting an attacker
	// keep a victim locked out indefinitely just by guessing wrong on purpose.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET
		    failed_login_attempts = failed_login_attempts + 1,
		    account_locked_until = CASE
		        WHEN failed_login_attempts + 1 >= $2
		         AND (account_locked_until IS NULL OR account_locked_until <= CURRENT_TIMESTAMP)
		        THEN CURRENT_TIMESTAMP + $3::interval
		        ELSE account_locked_until END,
		    updated_at = CURRENT_TIMESTAMP
		 WHERE LOWER(email) = LOWER($1) OR phone_number = $1`,
		identifier, maxAttempts, lockFor.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repo) RecordSuccessfulAttempt(ctx context.Context, identifier, ip string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO login_attempts (identifier, ip_address, success) VALUES ($1, $2, TRUE)`,
		identifier, ip); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET failed_login_attempts = 0, account_locked_until = NULL,
		    last_login_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		 WHERE LOWER(email) = LOWER($1) OR phone_number = $1`, identifier); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ClearExpiredLock resets the counter once a lock has elapsed, matching the
// Java isAccountLocked() side effect.
func (r *Repo) ClearExpiredLock(ctx context.Context, userID int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET failed_login_attempts = 0, account_locked_until = NULL
		 WHERE id = $1 AND account_locked_until IS NOT NULL
		   AND account_locked_until <= CURRENT_TIMESTAMP`, userID)
	return err
}
