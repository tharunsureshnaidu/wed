package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/dto"
)

var ErrNotFound = errors.New("feedback not found")

// Repository defines the persistence interface for application feedback.
type Repository interface {
	Create(ctx context.Context, f *domain.Feedback) (*domain.Feedback, error)
	GetByID(ctx context.Context, id string) (*domain.FeedbackWithUser, error)
	List(ctx context.Context, filter dto.FeedbackFilter) ([]domain.FeedbackWithUser, int64, error)
	ListByUserID(ctx context.Context, userID int64, page, size int) ([]domain.FeedbackWithUser, int64, error)
	Update(ctx context.Context, id string, status, adminNote *string, resolvedBy int64) (*domain.FeedbackWithUser, error)
	GetSummary(ctx context.Context) (*domain.FeedbackSummary, error)
	// GetByUserID returns a feedback entry for the given user, if any.
	GetByUserID(ctx context.Context, userID int64) (*domain.Feedback, error)
}

type pgxRepository struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) Repository {
	return &pgxRepository{db: db}
}

func (r *pgxRepository) Create(ctx context.Context, f *domain.Feedback) (*domain.Feedback, error) {
	status := domain.StatusNew
	if f.Status != "" {
		status = f.Status
	}

	var res domain.Feedback
	err := r.db.QueryRow(ctx, `
		INSERT INTO app_feedback (
			user_id, rating, message, attachment_url, app_version, platform, status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, user_id, rating, message, attachment_url, app_version, platform,
		          status, admin_note, resolved_by, resolved_at, is_deleted, created_at, updated_at`,
		f.UserID, f.Rating, f.Message, f.AttachmentURL, f.AppVersion, f.Platform, status).Scan(
		&res.ID, &res.UserID, &res.Rating, &res.Message, &res.AttachmentURL, &res.AppVersion, &res.Platform,
		&res.Status, &res.AdminNote, &res.ResolvedBy, &res.ResolvedAt, &res.IsDeleted, &res.CreatedAt, &res.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create feedback: %w", err)
	}
	return &res, nil
}

func (r *pgxRepository) GetByID(ctx context.Context, id string) (*domain.FeedbackWithUser, error) {
	var item domain.FeedbackWithUser
	err := r.db.QueryRow(ctx, `
		SELECT f.id::text, f.user_id, u.full_name, u.email,
		       f.rating, f.message, f.attachment_url, f.app_version, f.platform,
		       f.status, f.admin_note, f.resolved_by, f.resolved_at, f.is_deleted, f.created_at, f.updated_at
		  FROM app_feedback f
		  LEFT JOIN users u ON u.id = f.user_id
		 WHERE f.id = $1 AND f.is_deleted = FALSE`, id).Scan(
		&item.ID, &item.UserID, &item.UserName, &item.UserEmail,
		&item.Rating, &item.Message, &item.AttachmentURL, &item.AppVersion, &item.Platform,
		&item.Status, &item.AdminNote, &item.ResolvedBy, &item.ResolvedAt, &item.IsDeleted, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get feedback by id: %w", err)
	}
	return &item, nil
}

func (r *pgxRepository) List(ctx context.Context, filter dto.FeedbackFilter) ([]domain.FeedbackWithUser, int64, error) {
	whereClauses := []string{"f.is_deleted = FALSE"}
	args := []any{}
	argIdx := 1

	if filter.Status != nil && *filter.Status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("f.status = $%d", argIdx))
		args = append(args, strings.ToUpper(*filter.Status))
		argIdx++
	}

	if filter.Rating != nil && *filter.Rating > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("f.rating = $%d", argIdx))
		args = append(args, *filter.Rating)
		argIdx++
	}

	if filter.Search != nil && strings.TrimSpace(*filter.Search) != "" {
		term := "%" + strings.TrimSpace(*filter.Search) + "%"
		whereClauses = append(whereClauses, fmt.Sprintf("(u.full_name ILIKE $%d OR u.email ILIKE $%d OR f.message ILIKE $%d)", argIdx, argIdx, argIdx))
		args = append(args, term)
		argIdx++
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	countSQL := fmt.Sprintf(`
		SELECT COUNT(*)
		  FROM app_feedback f
		  LEFT JOIN users u ON u.id = f.user_id
		 WHERE %s`, whereSQL)

	var total int64
	if err := r.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count feedback: %w", err)
	}

	orderClause := "ORDER BY f.created_at DESC"
	if strings.ToLower(filter.Sort) == "oldest" {
		orderClause = "ORDER BY f.created_at ASC"
	}

	size := filter.Size
	if size <= 0 {
		size = 20
	}
	page := filter.Page
	if page < 0 {
		page = 0
	}
	offset := page * size

	listSQL := fmt.Sprintf(`
		SELECT f.id::text, f.user_id, u.full_name, u.email,
		       f.rating, f.message, f.attachment_url, f.app_version, f.platform,
		       f.status, f.admin_note, f.resolved_by, f.resolved_at, f.is_deleted, f.created_at, f.updated_at
		  FROM app_feedback f
		  LEFT JOIN users u ON u.id = f.user_id
		 WHERE %s
		 %s
		 LIMIT $%d OFFSET $%d`, whereSQL, orderClause, argIdx, argIdx+1)

	queryArgs := append(args, size, offset)
	rows, err := r.db.Query(ctx, listSQL, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query feedback: %w", err)
	}
	defer rows.Close()

	var results []domain.FeedbackWithUser
	for rows.Next() {
		var item domain.FeedbackWithUser
		if err := rows.Scan(
			&item.ID, &item.UserID, &item.UserName, &item.UserEmail,
			&item.Rating, &item.Message, &item.AttachmentURL, &item.AppVersion, &item.Platform,
			&item.Status, &item.AdminNote, &item.ResolvedBy, &item.ResolvedAt, &item.IsDeleted, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan feedback row: %w", err)
		}
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate feedback rows: %w", err)
	}

	return results, total, nil
}

func (r *pgxRepository) ListByUserID(ctx context.Context, userID int64, page, size int) ([]domain.FeedbackWithUser, int64, error) {
	if size <= 0 {
		size = 20
	}
	if page < 0 {
		page = 0
	}
	offset := page * size

	var total int64
	countSQL := `SELECT COUNT(*) FROM app_feedback WHERE user_id = $1 AND is_deleted = FALSE`
	if err := r.db.QueryRow(ctx, countSQL, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count user feedback: %w", err)
	}

	listSQL := `
		SELECT f.id::text, f.user_id, NULL, NULL,
		       f.rating, f.message, f.attachment_url, f.app_version, f.platform,
		       f.status, NULL, f.resolved_by, f.resolved_at, f.is_deleted, f.created_at, f.updated_at
		  FROM app_feedback f
		 WHERE f.user_id = $1 AND f.is_deleted = FALSE
		 ORDER BY f.created_at DESC
		 LIMIT $2 OFFSET $3`

	rows, err := r.db.Query(ctx, listSQL, userID, size, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query user feedback: %w", err)
	}
	defer rows.Close()

	var results []domain.FeedbackWithUser
	for rows.Next() {
		var item domain.FeedbackWithUser
		if err := rows.Scan(
			&item.ID, &item.UserID, &item.UserName, &item.UserEmail,
			&item.Rating, &item.Message, &item.AttachmentURL, &item.AppVersion, &item.Platform,
			&item.Status, &item.AdminNote, &item.ResolvedBy, &item.ResolvedAt, &item.IsDeleted, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan user feedback row: %w", err)
		}
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate user feedback rows: %w", err)
	}

	return results, total, nil
}
// GetByUserID checks if a feedback entry already exists for the specified user.
func (r *pgxRepository) GetByUserID(ctx context.Context, userID int64) (*domain.Feedback, error) {
    var f domain.Feedback
    err := r.db.QueryRow(ctx, `
        SELECT id::text, user_id, rating, message, attachment_url, app_version, platform, status, admin_note, resolved_by, resolved_at, is_deleted, created_at, updated_at
        FROM app_feedback
        WHERE user_id = $1 AND is_deleted = FALSE
        LIMIT 1`, userID).Scan(
        &f.ID, &f.UserID, &f.Rating, &f.Message, &f.AttachmentURL, &f.AppVersion, &f.Platform, &f.Status, &f.AdminNote, &f.ResolvedBy, &f.ResolvedAt, &f.IsDeleted, &f.CreatedAt, &f.UpdatedAt,
    )
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, ErrNotFound
        }
        return nil, fmt.Errorf("get feedback by user: %w", err)
    }
    return &f, nil
}


func (r *pgxRepository) Update(ctx context.Context, id string, status, adminNote *string, resolvedBy int64) (*domain.FeedbackWithUser, error) {
	var item domain.FeedbackWithUser
	err := r.db.QueryRow(ctx, `
		UPDATE app_feedback SET
		    status      = COALESCE($2, status),
		    admin_note  = COALESCE($3, admin_note),
		    resolved_by = CASE WHEN $2 IN ('RESOLVED','CLOSED') THEN $4 ELSE resolved_by END,
		    resolved_at = CASE
		        WHEN $2 IN ('RESOLVED','CLOSED') THEN CURRENT_TIMESTAMP
		        WHEN $2 IS NOT NULL THEN NULL
		        ELSE resolved_at END,
		    updated_at  = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE
		 RETURNING id::text, user_id, rating, message, attachment_url,
		           app_version, platform, status, admin_note, resolved_by, resolved_at, is_deleted, created_at, updated_at`,
		id, status, adminNote, resolvedBy).Scan(
		&item.ID, &item.UserID, &item.Rating, &item.Message, &item.AttachmentURL,
		&item.AppVersion, &item.Platform, &item.Status, &item.AdminNote, &item.ResolvedBy, &item.ResolvedAt, &item.IsDeleted, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update feedback: %w", err)
	}

	// Fetch user details for the updated row
	if item.UserID != nil {
		_ = r.db.QueryRow(ctx, `SELECT full_name, email FROM users WHERE id = $1`, *item.UserID).Scan(&item.UserName, &item.UserEmail)
	}

	return &item, nil
}

func (r *pgxRepository) GetSummary(ctx context.Context) (*domain.FeedbackSummary, error) {
	var summary domain.FeedbackSummary
	err := r.db.QueryRow(ctx, `
		SELECT 
			COUNT(*),
			COALESCE(ROUND(AVG(rating)::numeric, 1), 0.0),
			COUNT(*) FILTER (WHERE rating = 5),
			COUNT(*) FILTER (WHERE rating = 4),
			COUNT(*) FILTER (WHERE rating = 3),
			COUNT(*) FILTER (WHERE rating = 2),
			COUNT(*) FILTER (WHERE rating = 1)
		  FROM app_feedback
		 WHERE is_deleted = FALSE AND rating IS NOT NULL`).Scan(
		&summary.TotalReviews,
		&summary.AverageRating,
		&summary.FiveStar,
		&summary.FourStar,
		&summary.ThreeStar,
		&summary.TwoStar,
		&summary.OneStar,
	)
	if err != nil {
		return nil, fmt.Errorf("get feedback summary: %w", err)
	}
	return &summary, nil
}
