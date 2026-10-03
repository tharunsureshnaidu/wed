package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/dto"
)

var (
	ErrNotFound     = errors.New("help message not found")
	ErrUserNotFound = errors.New("user not found")
)

// Repository defines the persistence operations for Help Center messages.
type Repository interface {
	Create(ctx context.Context, msg *domain.HelpCenterMessage) (*domain.HelpCenterMessage, error)
	GetByID(ctx context.Context, id string) (*domain.HelpCenterMessage, error)
	MarkAsRead(ctx context.Context, id string) error
	List(ctx context.Context, filter dto.HelpMessageFilter) ([]domain.HelpCenterMessage, int64, error)
	Delete(ctx context.Context, id string) error
	GetUserProfile(ctx context.Context, userID int64) (*domain.UserProfile, error)
}

type pgxRepository struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) Repository {
	return &pgxRepository{db: db}
}

func (r *pgxRepository) GetUserProfile(ctx context.Context, userID int64) (*domain.UserProfile, error) {
	var p domain.UserProfile
	err := r.db.QueryRow(ctx, `
		SELECT u.id,
		       COALESCE(NULLIF(TRIM(CONCAT(p.first_name, ' ', p.last_name)), ''), u.full_name),
		       u.email,
		       u.phone_number
		  FROM users u
		  LEFT JOIN user_profiles p ON p.id = u.id AND p.is_deleted = FALSE
		 WHERE u.id = $1 AND u.is_deleted = FALSE`, userID).Scan(
		&p.ID, &p.Name, &p.Email, &p.Phone,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user profile: %w", err)
	}
	return &p, nil
}

func (r *pgxRepository) Create(ctx context.Context, msg *domain.HelpCenterMessage) (*domain.HelpCenterMessage, error) {
	status := domain.StatusNew
	if msg.Status.Valid() {
		status = msg.Status
	}

	var res domain.HelpCenterMessage
	err := r.db.QueryRow(ctx, `
		INSERT INTO help_center_messages (
			user_id, user_name, user_email, user_phone, message, status
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, user_id, user_name, user_email, user_phone, message,
		          status, created_at, updated_at, deleted_at, is_deleted`,
		msg.UserID, msg.UserName, msg.UserEmail, msg.UserPhone, msg.Message, status,
	).Scan(
		&res.ID, &res.UserID, &res.UserName, &res.UserEmail, &res.UserPhone, &res.Message,
		&res.Status, &res.CreatedAt, &res.UpdatedAt, &res.DeletedAt, &res.IsDeleted,
	)
	if err != nil {
		return nil, fmt.Errorf("create help center message: %w", err)
	}
	return &res, nil
}

func (r *pgxRepository) GetByID(ctx context.Context, id string) (*domain.HelpCenterMessage, error) {
	var item domain.HelpCenterMessage
	err := r.db.QueryRow(ctx, `
		SELECT id::text, user_id, user_name, user_email, user_phone, message,
		       status, created_at, updated_at, deleted_at, is_deleted
		  FROM help_center_messages
		 WHERE id = $1 AND is_deleted = FALSE`, id).Scan(
		&item.ID, &item.UserID, &item.UserName, &item.UserEmail, &item.UserPhone, &item.Message,
		&item.Status, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt, &item.IsDeleted,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get help center message by id: %w", err)
	}
	return &item, nil
}

func (r *pgxRepository) MarkAsRead(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE help_center_messages
		   SET status = 'READ', updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE AND status = 'NEW'`, id)
	if err != nil {
		return fmt.Errorf("mark help center message as read: %w", err)
	}
	return nil
}

func (r *pgxRepository) List(ctx context.Context, filter dto.HelpMessageFilter) ([]domain.HelpCenterMessage, int64, error) {
	whereClauses := []string{"m.is_deleted = FALSE"}
	args := []any{}
	argIdx := 1

	if filter.Status != nil && strings.TrimSpace(*filter.Status) != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("m.status = $%d", argIdx))
		args = append(args, strings.ToUpper(strings.TrimSpace(*filter.Status)))
		argIdx++
	}

	if filter.UserID != nil && *filter.UserID > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("m.user_id = $%d", argIdx))
		args = append(args, *filter.UserID)
		argIdx++
	}

	if filter.FromDate != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("m.created_at >= $%d", argIdx))
		args = append(args, *filter.FromDate)
		argIdx++
	}

	if filter.ToDate != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("m.created_at <= $%d", argIdx))
		args = append(args, *filter.ToDate)
		argIdx++
	}

	if filter.Search != nil && strings.TrimSpace(*filter.Search) != "" {
		term := "%" + strings.TrimSpace(*filter.Search) + "%"
		whereClauses = append(whereClauses, fmt.Sprintf(
			"(m.user_name ILIKE $%d OR COALESCE(m.user_email,'') ILIKE $%d OR COALESCE(m.user_phone,'') ILIKE $%d OR m.message ILIKE $%d)",
			argIdx, argIdx, argIdx, argIdx,
		))
		args = append(args, term)
		argIdx++
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	countSQL := fmt.Sprintf(`
		SELECT COUNT(*)
		  FROM help_center_messages m
		 WHERE %s`, whereSQL)

	var total int64
	if err := r.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count help center messages: %w", err)
	}

	var sortCol string
	switch strings.ToLower(filter.SortBy) {
	case "status":
		sortCol = "m.status"
	case "user_name", "username":
		sortCol = "m.user_name"
	case "user_email", "useremail":
		sortCol = "m.user_email"
	case "user_phone", "userphone":
		sortCol = "m.user_phone"
	case "updated_at", "updatedat":
		sortCol = "m.updated_at"
	default:
		sortCol = "m.created_at"
	}

	sortOrder := "DESC"
	if strings.ToUpper(filter.SortOrder) == "ASC" {
		sortOrder = "ASC"
	}
	orderClause := fmt.Sprintf("ORDER BY %s %s", sortCol, sortOrder)

	size := filter.Limit
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
	offset := page * size

	listSQL := fmt.Sprintf(`
		SELECT m.id::text, m.user_id, m.user_name, m.user_email, m.user_phone, m.message,
		       m.status, m.created_at, m.updated_at, m.deleted_at, m.is_deleted
		  FROM help_center_messages m
		 WHERE %s
		 %s
		 LIMIT $%d OFFSET $%d`, whereSQL, orderClause, argIdx, argIdx+1)

	queryArgs := append(args, size, offset)
	rows, err := r.db.Query(ctx, listSQL, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query help center messages: %w", err)
	}
	defer rows.Close()

	var results []domain.HelpCenterMessage
	for rows.Next() {
		var item domain.HelpCenterMessage
		if err := rows.Scan(
			&item.ID, &item.UserID, &item.UserName, &item.UserEmail, &item.UserPhone, &item.Message,
			&item.Status, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt, &item.IsDeleted,
		); err != nil {
			return nil, 0, fmt.Errorf("scan help center message row: %w", err)
		}
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate help center message rows: %w", err)
	}

	return results, total, nil
}

func (r *pgxRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE help_center_messages
		   SET is_deleted = TRUE, deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE`, id)
	if err != nil {
		return fmt.Errorf("delete help center message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
