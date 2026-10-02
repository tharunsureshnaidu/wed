package service

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
)

type CreateRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type UpdateRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type Service struct {
	repo repository.Repository
	db   *pgxpool.Pool
}

func New(repo repository.Repository, db *pgxpool.Pool) *Service {
	return &Service{repo: repo, db: db}
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (*repository.PrivacyPolicy, error) {
	if err := s.validateRequest(req.Title, req.Content); err != nil {
		return nil, err
	}

	policy := &repository.PrivacyPolicy{
		Title:   strings.TrimSpace(req.Title),
		Content: req.Content,
	}

	if err := s.repo.Create(ctx, policy); err != nil {
		if errors.Is(err, repository.ErrAlreadyExists) {
			return nil, apperr.Conflict("ALREADY_EXISTS", "A Privacy Policy already exists. Use PUT to update it.")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to create privacy policy")
	}

	return policy, nil
}

func (s *Service) Update(ctx context.Context, req UpdateRequest) (*repository.PrivacyPolicy, error) {
	if err := s.validateRequest(req.Title, req.Content); err != nil {
		return nil, err
	}

	policy := &repository.PrivacyPolicy{
		Title:   strings.TrimSpace(req.Title),
		Content: req.Content,
	}

	if err := s.repo.Update(ctx, policy); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(404, "NOT_FOUND", "Privacy Policy not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to update privacy policy")
	}

	return policy, nil
}

func (s *Service) Get(ctx context.Context) (*repository.PrivacyPolicy, error) {
	policy, err := s.repo.Get(ctx)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(404, "NOT_FOUND", "Privacy Policy not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to fetch privacy policy")
	}
	return policy, nil
}

func (s *Service) Delete(ctx context.Context) error {
	if err := s.repo.Delete(ctx); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apperr.New(404, "NOT_FOUND", "Privacy Policy not found")
		}
		return apperr.Internal("DB_ERROR", "Failed to delete privacy policy")
	}
	return nil
}

func (s *Service) validateRequest(title, content string) error {
	if strings.TrimSpace(title) == "" {
		return apperr.BadRequest("INVALID_TITLE", "Title is required and cannot be empty")
	}
	if len(title) > 255 {
		return apperr.BadRequest("INVALID_TITLE", "Title must be at most 255 characters")
	}
	if strings.TrimSpace(content) == "" {
		return apperr.BadRequest("INVALID_CONTENT", "Content is required and cannot be empty")
	}
	return nil
}
