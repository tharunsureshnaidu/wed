package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Repo struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Repo { return &Repo{db: db} }

type Profile struct {
	ID        int64   `json:"id"`
	FirstName string  `json:"firstName"`
	LastName  *string `json:"lastName"`
	AvatarURL *string `json:"avatarUrl"`
	KycStatus string  `json:"kycStatus"`
	Bio       *string `json:"bio"`

	// Account-level fields, joined from users so /me is a single call.
	Email         *string    `json:"email"`
	PhoneNumber   *string    `json:"phoneNumber"`
	Status        string     `json:"status"`
	EmailVerified bool       `json:"emailVerified"`
	PhoneVerified bool       `json:"phoneVerified"`
	MemberSince   *time.Time `json:"memberSince"`
	Roles         []string   `json:"roles"`
	Addresses     []Address  `json:"addresses"`
}

type Address struct {
	ID      int64   `json:"id"`
	Street  *string `json:"street"`
	City    *string `json:"city"`
	State   *string `json:"state"`
	ZipCode *string `json:"zipCode"`
	Country *string `json:"country"`
}

// EnsureProfile creates the profile row if it is missing. Called at registration,
// and again on first read so an account created before this existed still works.
func (r *Repo) EnsureProfile(ctx context.Context, userID int64, firstName string, lastName *string) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO user_profiles (id, first_name, last_name) VALUES ($1, $2, $3)
		 ON CONFLICT (id) DO NOTHING`, userID, firstName, lastName)
	return err
}

func (r *Repo) Get(ctx context.Context, userID int64) (*Profile, error) {
	var p Profile
	err := r.db.QueryRow(ctx,
		`SELECT p.id, p.first_name, p.last_name, p.avatar_url, p.kyc_status, p.bio,
		        u.email, u.phone_number, u.status,
		        u.is_email_verified, u.is_phone_verified, u.created_at
		 FROM user_profiles p JOIN users u ON u.id = p.id
		 WHERE p.id = $1 AND p.is_deleted = FALSE`, userID).
		Scan(&p.ID, &p.FirstName, &p.LastName, &p.AvatarURL, &p.KycStatus, &p.Bio,
			&p.Email, &p.PhoneNumber, &p.Status,
			&p.EmailVerified, &p.PhoneVerified, &p.MemberSince)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	rows, err := r.db.Query(ctx,
		`SELECT r.role_name FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		 WHERE ur.user_id = $1 ORDER BY r.id`, userID)
	if err != nil {
		return nil, err
	}
	p.Roles = []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, err
		}
		p.Roles = append(p.Roles, s)
	}
	rows.Close()

	arows, err := r.db.Query(ctx,
		`SELECT id, street, city, state, zip_code, country FROM addresses
		 WHERE user_profile_id = $1 AND is_deleted = FALSE ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	p.Addresses = []Address{}
	for arows.Next() {
		var a Address
		if err := arows.Scan(&a.ID, &a.Street, &a.City, &a.State, &a.ZipCode, &a.Country); err != nil {
			return nil, err
		}
		p.Addresses = append(p.Addresses, a)
	}
	return &p, arows.Err()
}

func (r *Repo) Update(ctx context.Context, userID int64, firstName string, lastName, bio *string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE user_profiles SET first_name = $2, last_name = $3, bio = $4,
		    updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE`, userID, firstName, lastName, bio)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDelete marks the profile and the account deleted. Rows are kept because
// bookings and payments reference this user and must stay auditable.
func (r *Repo) SoftDelete(ctx context.Context, userID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE user_profiles SET is_deleted = TRUE, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET is_deleted = TRUE, status = 'INACTIVE', updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1`, userID); err != nil {
		return err
	}
	// Deleting the account must also end every live session.
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE user_id = $1 AND revoked = FALSE`,
		userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- favourites ---

func (r *Repo) AddFavourite(ctx context.Context, userID int64, facilityID string) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO favourite_facilities (user_profile_id, facility_id) VALUES ($1, $2)
		 ON CONFLICT (user_profile_id, facility_id) DO NOTHING`, userID, facilityID)
	return err
}

func (r *Repo) RemoveFavourite(ctx context.Context, userID int64, facilityID string) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM favourite_facilities WHERE user_profile_id = $1 AND facility_id = $2`,
		userID, facilityID)
	return err
}

type FavouriteFacility struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	City      *string `json:"city"`
	AvgRating float64 `json:"avgRating"`
}

func (r *Repo) ListFavourites(ctx context.Context, userID int64, typeFilter string) ([]FavouriteFacility, error) {
	rows, err := r.db.Query(ctx,
		`SELECT f.id, f.name, f.type, f.city, COALESCE(f.avg_rating, 0)
		 FROM favourite_facilities ff JOIN facilities f ON f.id = ff.facility_id
		 WHERE ff.user_profile_id = $1 AND f.is_deleted = FALSE
		   AND ($2 = '' OR f.type = $2)
		 ORDER BY ff.created_at DESC`, userID, typeFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []FavouriteFacility{}
	for rows.Next() {
		var f FavouriteFacility
		if err := rows.Scan(&f.ID, &f.Name, &f.Type, &f.City, &f.AvgRating); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
