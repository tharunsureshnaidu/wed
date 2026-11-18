package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound        = errors.New("privacy policy not found")
	ErrVersionConflict = errors.New("privacy policy version already exists")
	ErrNoActivePolicy  = errors.New("cannot deactivate the only active privacy policy")
)

// PrivacyPolicy represents the database entity for a privacy policy version.
type PrivacyPolicy struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	Content       string     `json:"content"`
	Version       string     `json:"version"`
	Status        string     `json:"status"`
	EffectiveFrom *time.Time `json:"effectiveFrom,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	CreatedBy     *int64     `json:"createdBy,omitempty"`
	UpdatedBy     *int64     `json:"updatedBy,omitempty"`
}

type Repository interface {
	Create(ctx context.Context, p *PrivacyPolicy) error
	GetActive(ctx context.Context) (*PrivacyPolicy, error)
	GetByID(ctx context.Context, id int64) (*PrivacyPolicy, error)
	GetAll(ctx context.Context) ([]PrivacyPolicy, error)
	Update(ctx context.Context, id int64, title, content, version string, updatedBy *int64) (*PrivacyPolicy, error)
	Activate(ctx context.Context, id int64, updatedBy *int64, effectiveFrom time.Time) (*PrivacyPolicy, error)
	Deactivate(ctx context.Context, id int64, updatedBy *int64) (*PrivacyPolicy, error)
	VersionExists(ctx context.Context, version string, excludeID int64) (bool, error)
}

type Repo struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

func (r *Repo) VersionExists(ctx context.Context, version string, excludeID int64) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM privacy_policies WHERE LOWER(version) = LOWER($1) AND id != $2)`
	err := r.db.QueryRow(ctx, query, strings.TrimSpace(version), excludeID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check version exists: %w", err)
	}
	return exists, nil
}

func (r *Repo) Create(ctx context.Context, p *PrivacyPolicy) error {
	query := `
		INSERT INTO privacy_policies (title, content, version, status, created_by, updated_by)
		VALUES ($1, $2, $3, 'INACTIVE', $4, $5)
		RETURNING id, status, effective_from, created_at, updated_at
	`
	err := r.db.QueryRow(ctx, query, p.Title, p.Content, p.Version, p.CreatedBy, p.UpdatedBy).
		Scan(&p.ID, &p.Status, &p.EffectiveFrom, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create privacy policy: %w", err)
	}
	return nil
}

func (r *Repo) GetActive(ctx context.Context) (*PrivacyPolicy, error) {
	query := `
		SELECT id, title, content, version, status, effective_from, created_at, updated_at, created_by, updated_by
		FROM privacy_policies
		WHERE status = 'ACTIVE'
		ORDER BY effective_from DESC NULLS LAST, id DESC
		LIMIT 1
	`
	var p PrivacyPolicy
	err := r.db.QueryRow(ctx, query).Scan(
		&p.ID, &p.Title, &p.Content, &p.Version, &p.Status, &p.EffectiveFrom,
		&p.CreatedAt, &p.UpdatedAt, &p.CreatedBy, &p.UpdatedBy,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get active privacy policy: %w", err)
	}
	return &p, nil
}

func (r *Repo) GetByID(ctx context.Context, id int64) (*PrivacyPolicy, error) {
	query := `
		SELECT id, title, content, version, status, effective_from, created_at, updated_at, created_by, updated_by
		FROM privacy_policies
		WHERE id = $1
	`
	var p PrivacyPolicy
	err := r.db.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.Title, &p.Content, &p.Version, &p.Status, &p.EffectiveFrom,
		&p.CreatedAt, &p.UpdatedAt, &p.CreatedBy, &p.UpdatedBy,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get privacy policy by id: %w", err)
	}
	return &p, nil
}

func (r *Repo) GetAll(ctx context.Context) ([]PrivacyPolicy, error) {
	query := `
		SELECT id, title, content, version, status, effective_from, created_at, updated_at, created_by, updated_by
		FROM privacy_policies
		ORDER BY created_at DESC, id DESC
	`
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get all privacy policies: %w", err)
	}
	defer rows.Close()

	var list []PrivacyPolicy
	for rows.Next() {
		var p PrivacyPolicy
		if err := rows.Scan(
			&p.ID, &p.Title, &p.Content, &p.Version, &p.Status, &p.EffectiveFrom,
			&p.CreatedAt, &p.UpdatedAt, &p.CreatedBy, &p.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("scan privacy policy: %w", err)
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	if list == nil {
		list = []PrivacyPolicy{}
	}
	return list, nil
}

func (r *Repo) Update(ctx context.Context, id int64, title, content, version string, updatedBy *int64) (*PrivacyPolicy, error) {
	query := `
		UPDATE privacy_policies
		SET title = $1, content = $2, version = $3, updated_by = $4, updated_at = CURRENT_TIMESTAMP
		WHERE id = $5
		RETURNING id, title, content, version, status, effective_from, created_at, updated_at, created_by, updated_by
	`
	var p PrivacyPolicy
	err := r.db.QueryRow(ctx, query, title, content, version, updatedBy, id).Scan(
		&p.ID, &p.Title, &p.Content, &p.Version, &p.Status, &p.EffectiveFrom,
		&p.CreatedAt, &p.UpdatedAt, &p.CreatedBy, &p.UpdatedBy,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update privacy policy: %w", err)
	}
	return &p, nil
}

func (r *Repo) Activate(ctx context.Context, id int64, updatedBy *int64, effectiveFrom time.Time) (*PrivacyPolicy, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Verify the target policy exists
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM privacy_policies WHERE id = $1)`, id).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check target policy exists: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}

	// Deactivate any currently active policy (except target, which will be updated next)
	_, err = tx.Exec(ctx, `
		UPDATE privacy_policies
		SET status = 'INACTIVE', updated_at = CURRENT_TIMESTAMP, updated_by = $1
		WHERE status = 'ACTIVE' AND id != $2
	`, updatedBy, id)
	if err != nil {
		return nil, fmt.Errorf("deactivate existing active policy: %w", err)
	}

	// Activate target policy and set effective_from
	var p PrivacyPolicy
	err = tx.QueryRow(ctx, `
		UPDATE privacy_policies
		SET status = 'ACTIVE', effective_from = $1, updated_at = CURRENT_TIMESTAMP, updated_by = $2
		WHERE id = $3
		RETURNING id, title, content, version, status, effective_from, created_at, updated_at, created_by, updated_by
	`, effectiveFrom, updatedBy, id).Scan(
		&p.ID, &p.Title, &p.Content, &p.Version, &p.Status, &p.EffectiveFrom,
		&p.CreatedAt, &p.UpdatedAt, &p.CreatedBy, &p.UpdatedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("activate policy: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit activation transaction: %w", err)
	}
	return &p, nil
}

func (r *Repo) Deactivate(ctx context.Context, id int64, updatedBy *int64) (*PrivacyPolicy, error) {
	query := `
		UPDATE privacy_policies
		SET status = 'INACTIVE', updated_at = CURRENT_TIMESTAMP, updated_by = $1
		WHERE id = $2
		RETURNING id, title, content, version, status, effective_from, created_at, updated_at, created_by, updated_by
	`
	var p PrivacyPolicy
	err := r.db.QueryRow(ctx, query, updatedBy, id).Scan(
		&p.ID, &p.Title, &p.Content, &p.Version, &p.Status, &p.EffectiveFrom,
		&p.CreatedAt, &p.UpdatedAt, &p.CreatedBy, &p.UpdatedBy,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("deactivate privacy policy: %w", err)
	}
	return &p, nil
}
