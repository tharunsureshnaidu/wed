package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
)

var (
	ErrNotFound = errors.New("contact us request not found")
)

// Repository defines persistence operations for Contact Us inquiries.
type Repository interface {
	Create(ctx context.Context, item *domain.ContactUs) (*domain.ContactUs, error)
	GetByID(ctx context.Context, id string) (*domain.ContactUs, error)
	List(ctx context.Context, filter dto.ContactFilter) ([]domain.ContactUs, int64, error)
	Update(ctx context.Context, id string, status domain.Status, adminResponse *string) (*domain.ContactUs, error)
	Delete(ctx context.Context, id string) error
}

type pgxRepository struct {
	db *pgxpool.Pool
}

// New creates a new Contact Us repository backed by pgxpool.
func New(db *pgxpool.Pool) Repository {
	return &pgxRepository{db: db}
}

func (r *pgxRepository) Create(ctx context.Context, item *domain.ContactUs) (*domain.ContactUs, error) {
	status := domain.StatusOpen
	if item.Status.Valid() {
		status = item.Status
	}

	var res domain.ContactUs
	err := r.db.QueryRow(ctx, `
		INSERT INTO contact_us (
			name, email, phone, subject, message, status
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, name, email, phone, subject, message,
		          status, admin_response, created_at, updated_at, deleted_at, is_deleted`,
		item.Name, item.Email, item.Phone, item.Subject, item.Message, status,
	).Scan(
		&res.ID, &res.Name, &res.Email, &res.Phone, &res.Subject, &res.Message,
		&res.Status, &res.AdminResponse, &res.CreatedAt, &res.UpdatedAt, &res.DeletedAt, &res.IsDeleted,
	)
	if err != nil {
		return nil, fmt.Errorf("create contact us: %w", err)
	}
	return &res, nil
}

func (r *pgxRepository) GetByID(ctx context.Context, id string) (*domain.ContactUs, error) {
	var item domain.ContactUs
	err := r.db.QueryRow(ctx, `
		SELECT id::text, name, email, phone, subject, message,
		       status, admin_response, created_at, updated_at, deleted_at, is_deleted
		  FROM contact_us
		 WHERE id = $1 AND is_deleted = FALSE`, id).Scan(
		&item.ID, &item.Name, &item.Email, &item.Phone, &item.Subject, &item.Message,
		&item.Status, &item.AdminResponse, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt, &item.IsDeleted,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get contact us by id: %w", err)
	}
	return &item, nil
}

func (r *pgxRepository) List(ctx context.Context, filter dto.ContactFilter) ([]domain.ContactUs, int64, error) {
	whereClauses := []string{"m.is_deleted = FALSE"}
	args := []any{}
	argIdx := 1

	if filter.Status != nil && strings.TrimSpace(*filter.Status) != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("m.status = $%d", argIdx))
		args = append(args, strings.ToUpper(strings.TrimSpace(*filter.Status)))
		argIdx++
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	countSQL := fmt.Sprintf(`
		SELECT COUNT(*)
		  FROM contact_us m
		 WHERE %s`, whereSQL)

	var total int64
	if err := r.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count contact us requests: %w", err)
	}

	size := filter.Size
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}

	page := filter.Page
	if page < 0 {
		page = 0
	}

	offset := httpx.Offset(page, size)

	listSQL := fmt.Sprintf(`
		SELECT m.id::text, m.name, m.email, m.phone, m.subject, m.message,
		       m.status, m.admin_response, m.created_at, m.updated_at, m.deleted_at, m.is_deleted
		  FROM contact_us m
		 WHERE %s
		 ORDER BY m.created_at DESC
		 LIMIT $%d OFFSET $%d`, whereSQL, argIdx, argIdx+1)

	queryArgs := append(args, size, offset)
	rows, err := r.db.Query(ctx, listSQL, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query contact us requests: %w", err)
	}
	defer rows.Close()

	var results []domain.ContactUs
	for rows.Next() {
		var item domain.ContactUs
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Email, &item.Phone, &item.Subject, &item.Message,
			&item.Status, &item.AdminResponse, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt, &item.IsDeleted,
		); err != nil {
			return nil, 0, fmt.Errorf("scan contact us row: %w", err)
		}
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate contact us rows: %w", err)
	}

	return results, total, nil
}

func (r *pgxRepository) Update(ctx context.Context, id string, status domain.Status, adminResponse *string) (*domain.ContactUs, error) {
	var res domain.ContactUs
	err := r.db.QueryRow(ctx, `
		UPDATE contact_us
		   SET status = $2,
		       admin_response = $3,
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE
		RETURNING id::text, name, email, phone, subject, message,
		          status, admin_response, created_at, updated_at, deleted_at, is_deleted`,
		id, status, adminResponse,
	).Scan(
		&res.ID, &res.Name, &res.Email, &res.Phone, &res.Subject, &res.Message,
		&res.Status, &res.AdminResponse, &res.CreatedAt, &res.UpdatedAt, &res.DeletedAt, &res.IsDeleted,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update contact us: %w", err)
	}
	return &res, nil
}

func (r *pgxRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE contact_us
		   SET is_deleted = TRUE,
		       deleted_at = CURRENT_TIMESTAMP,
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE`, id)
	if err != nil {
		return fmt.Errorf("delete contact us: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

