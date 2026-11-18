package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
)

type CreateRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Version string `json:"version"`
}

type UpdateRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Version string `json:"version"`
}

type Service struct {
	repo repository.Repository
	db   *pgxpool.Pool
}

func New(repo repository.Repository, db *pgxpool.Pool) *Service {
	return &Service{repo: repo, db: db}
}

func (s *Service) Create(ctx context.Context, req CreateRequest, userID *int64) (*repository.PrivacyPolicy, error) {
	if err := s.validateFields(req.Title, req.Content, req.Version); err != nil {
		return nil, err
	}

	exists, err := s.repo.VersionExists(ctx, req.Version, 0)
	if err != nil {
		return nil, apperr.Internal("DB_ERROR", "Failed to check version uniqueness")
	}
	if exists {
		return nil, apperr.Conflict("VERSION_EXISTS", "A privacy policy with this version already exists")
	}

	policy := &repository.PrivacyPolicy{
		Title:     strings.TrimSpace(req.Title),
		Content:   req.Content, // Raw HTML preserved exactly as received
		Version:   strings.TrimSpace(req.Version),
		Status:    "INACTIVE",
		CreatedBy: userID,
		UpdatedBy: userID,
	}

	if err := s.repo.Create(ctx, policy); err != nil {
		return nil, apperr.Internal("DB_ERROR", "Failed to create privacy policy")
	}

	return policy, nil
}

func (s *Service) Update(ctx context.Context, id int64, req UpdateRequest, userID *int64) (*repository.PrivacyPolicy, error) {
	if id <= 0 {
		return nil, apperr.BadRequest("INVALID_ID", "Invalid privacy policy ID")
	}
	if err := s.validateFields(req.Title, req.Content, req.Version); err != nil {
		return nil, err
	}

	exists, err := s.repo.VersionExists(ctx, req.Version, id)
	if err != nil {
		return nil, apperr.Internal("DB_ERROR", "Failed to check version uniqueness")
	}
	if exists {
		return nil, apperr.Conflict("VERSION_EXISTS", "A privacy policy with this version already exists")
	}

	policy, err := s.repo.Update(ctx, id, strings.TrimSpace(req.Title), req.Content, strings.TrimSpace(req.Version), userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(404, "NOT_FOUND", "Privacy Policy not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to update privacy policy")
	}

	return policy, nil
}

func (s *Service) GetActive(ctx context.Context) (*repository.PrivacyPolicy, error) {
	policy, err := s.repo.GetActive(ctx)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(404, "NOT_FOUND", "No active Privacy Policy found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to fetch active privacy policy")
	}
	return policy, nil
}

func (s *Service) GetByID(ctx context.Context, id int64) (*repository.PrivacyPolicy, error) {
	if id <= 0 {
		return nil, apperr.BadRequest("INVALID_ID", "Invalid privacy policy ID")
	}
	policy, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(404, "NOT_FOUND", "Privacy Policy not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to fetch privacy policy")
	}
	return policy, nil
}

func (s *Service) GetAll(ctx context.Context) ([]repository.PrivacyPolicy, error) {
	list, err := s.repo.GetAll(ctx)
	if err != nil {
		return nil, apperr.Internal("DB_ERROR", "Failed to fetch privacy policies")
	}
	return list, nil
}

func (s *Service) Activate(ctx context.Context, id int64, userID *int64) (*repository.PrivacyPolicy, error) {
	if id <= 0 {
		return nil, apperr.BadRequest("INVALID_ID", "Invalid privacy policy ID")
	}
	effectiveFrom := time.Now()
	policy, err := s.repo.Activate(ctx, id, userID, effectiveFrom)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(404, "NOT_FOUND", "Privacy Policy not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to activate privacy policy")
	}
	return policy, nil
}

func (s *Service) Deactivate(ctx context.Context, id int64, userID *int64) (*repository.PrivacyPolicy, error) {
	if id <= 0 {
		return nil, apperr.BadRequest("INVALID_ID", "Invalid privacy policy ID")
	}

	current, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(404, "NOT_FOUND", "Privacy Policy not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to fetch privacy policy")
	}

	if current.Status == "INACTIVE" {
		return current, nil
	}

	policy, err := s.repo.Deactivate(ctx, id, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(404, "NOT_FOUND", "Privacy Policy not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to deactivate privacy policy")
	}
	return policy, nil
}

func (s *Service) validateFields(title, content, version string) error {
	if strings.TrimSpace(title) == "" {
		return apperr.BadRequest("INVALID_TITLE", "Title is required and cannot be empty")
	}
	if len(title) > 255 {
		return apperr.BadRequest("INVALID_TITLE", "Title must be at most 255 characters")
	}

	if strings.TrimSpace(content) == "" {
		return apperr.BadRequest("INVALID_CONTENT", "Content is required and cannot be empty")
	}

	if strings.TrimSpace(version) == "" {
		return apperr.BadRequest("INVALID_VERSION", "Version is required and cannot be empty")
	}
	if len(version) > 50 {
		return apperr.BadRequest("INVALID_VERSION", "Version must be at most 50 characters")
	}

	return nil
}
