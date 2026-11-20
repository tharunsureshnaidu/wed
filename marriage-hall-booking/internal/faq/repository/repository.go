package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
)

var (
	ErrNotFound = errors.New("faq not found")
)

// Repository specifies database operations for FAQ management.
type Repository interface {
	Create(ctx context.Context, f *domain.FAQ) error
	GetByID(ctx context.Context, id string) (*domain.FAQ, error)
	ListAdmin(ctx context.Context, page, size int) ([]domain.FAQ, int64, error)
	Update(ctx context.Context, f *domain.FAQ) error
	Delete(ctx context.Context, id string) error
	ListActivePublic(ctx context.Context) ([]domain.FAQ, error)
}

// Repo implements Repository backed by PostgreSQL.
type Repo struct {
	db *pgxpool.Pool
}

// New constructs a new PostgreSQL-backed FAQ repository.
func New(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

// Create inserts a new FAQ record into the database.
func (r *Repo) Create(ctx context.Context, f *domain.FAQ) error {
	query := `
		INSERT INTO faqs (question, answer, display_order, is_active)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRow(ctx, query, f.Question, f.Answer, f.DisplayOrder, f.IsActive).
		Scan(&f.ID, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create faq: %w", err)
	}
	return nil
}

// GetByID loads a single non-deleted FAQ by its UUID.
func (r *Repo) GetByID(ctx context.Context, id string) (*domain.FAQ, error) {
	query := `
		SELECT id, question, answer, display_order, is_active, created_at, updated_at
		FROM faqs
		WHERE id = $1 AND is_deleted = FALSE
	`
	var f domain.FAQ
	err := r.db.QueryRow(ctx, query, id).
		Scan(&f.ID, &f.Question, &f.Answer, &f.DisplayOrder, &f.IsActive, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get faq by id: %w", err)
	}
	return &f, nil
}

// ListAdmin retrieves a paginated list of non-deleted FAQs sorted by display_order ascending,
// with created_at and id as stable secondary sorts.
func (r *Repo) ListAdmin(ctx context.Context, page, size int) ([]domain.FAQ, int64, error) {
	var total int64
	countQuery := `SELECT COUNT(1) FROM faqs WHERE is_deleted = FALSE`
	if err := r.db.QueryRow(ctx, countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count faqs: %w", err)
	}

	offset := httpx.Offset(page, size)
	query := `
		SELECT id, question, answer, display_order, is_active, created_at, updated_at
		FROM faqs
		WHERE is_deleted = FALSE
		ORDER BY display_order ASC, created_at ASC, id ASC
		LIMIT $1 OFFSET $2
	`
	rows, err := r.db.Query(ctx, query, size, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin faqs: %w", err)
	}
	defer rows.Close()

	items := make([]domain.FAQ, 0)
	for rows.Next() {
		var f domain.FAQ
		if err := rows.Scan(&f.ID, &f.Question, &f.Answer, &f.DisplayOrder, &f.IsActive, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan faq: %w", err)
		}
		items = append(items, f)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate faqs: %w", err)
	}

	return items, total, nil
}

// Update updates an existing FAQ's question, answer, display_order, and is_active flag.
func (r *Repo) Update(ctx context.Context, f *domain.FAQ) error {
	query := `
		UPDATE faqs
		SET question = $1, answer = $2, display_order = $3, is_active = $4, updated_at = CURRENT_TIMESTAMP
		WHERE id = $5 AND is_deleted = FALSE
		RETURNING updated_at
	`
	err := r.db.QueryRow(ctx, query, f.Question, f.Answer, f.DisplayOrder, f.IsActive, f.ID).
		Scan(&f.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("update faq: %w", err)
	}
	return nil
}

// Delete marks an FAQ as soft-deleted.
func (r *Repo) Delete(ctx context.Context, id string) error {
	query := `
		UPDATE faqs
		SET is_deleted = TRUE, deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND is_deleted = FALSE
	`
	tag, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete faq: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListActivePublic retrieves all active, non-deleted FAQs ordered by display_order ascending.
func (r *Repo) ListActivePublic(ctx context.Context) ([]domain.FAQ, error) {
	query := `
		SELECT id, question, answer, display_order, is_active, created_at, updated_at
		FROM faqs
		WHERE is_deleted = FALSE AND is_active = TRUE
		ORDER BY display_order ASC, created_at ASC, id ASC
	`
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list active public faqs: %w", err)
	}
	defer rows.Close()

	items := make([]domain.FAQ, 0)
	for rows.Next() {
		var f domain.FAQ
		if err := rows.Scan(&f.ID, &f.Question, &f.Answer, &f.DisplayOrder, &f.IsActive, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan public faq: %w", err)
		}
		items = append(items, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public faqs: %w", err)
	}

	return items, nil
}
