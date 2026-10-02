package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("privacy policy not found")
	ErrAlreadyExists = errors.New("privacy policy already exists")
)

// PrivacyPolicy is the domain model. The internal id is not exposed in JSON
// because there is only one Privacy Policy record in the system.
type PrivacyPolicy struct {
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Repository interface {
	Create(ctx context.Context, p *PrivacyPolicy) error
	Get(ctx context.Context) (*PrivacyPolicy, error)
	Update(ctx context.Context, p *PrivacyPolicy) error
	Delete(ctx context.Context) error
}

type Repo struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

func (r *Repo) Create(ctx context.Context, p *PrivacyPolicy) error {
	var count int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(1) FROM privacy_policies`).Scan(&count); err != nil {
		return fmt.Errorf("check existing privacy policy: %w", err)
	}
	if count > 0 {
		return ErrAlreadyExists
	}

	query := `
		INSERT INTO privacy_policies (title, content)
		VALUES ($1, $2)
		RETURNING created_at, updated_at
	`
	err := r.db.QueryRow(ctx, query, p.Title, p.Content).Scan(&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create privacy policy: %w", err)
	}
	return nil
}

func (r *Repo) Get(ctx context.Context) (*PrivacyPolicy, error) {
	query := `
		SELECT title, content, created_at, updated_at
		FROM privacy_policies
		ORDER BY id ASC
		LIMIT 1
	`
	var p PrivacyPolicy
	err := r.db.QueryRow(ctx, query).Scan(&p.Title, &p.Content, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get privacy policy: %w", err)
	}
	return &p, nil
}

func (r *Repo) Update(ctx context.Context, p *PrivacyPolicy) error {
	query := `
		UPDATE privacy_policies
		SET title = $1, content = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = (SELECT id FROM privacy_policies ORDER BY id ASC LIMIT 1)
		RETURNING created_at, updated_at
	`
	err := r.db.QueryRow(ctx, query, p.Title, p.Content).Scan(&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("update privacy policy: %w", err)
	}
	return nil
}

func (r *Repo) Delete(ctx context.Context) error {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM privacy_policies WHERE id = (SELECT id FROM privacy_policies ORDER BY id ASC LIMIT 1)`)
	if err != nil {
		return fmt.Errorf("delete privacy policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
