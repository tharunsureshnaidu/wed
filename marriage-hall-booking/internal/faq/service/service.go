package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/sanitizer"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
)

// Service defines the business logic contract for FAQ management.
type Service struct {
	repo repository.Repository
}

// New constructs an FAQ service with the given repository dependency.
func New(repo repository.Repository) *Service {
	return &Service{repo: repo}
}

// Create validates, sanitises, and persists a new FAQ record.
func (s *Service) Create(ctx context.Context, req dto.CreateFAQRequest) (*dto.FAQResponse, error) {
	cleanQ, cleanA, err := s.validateInput(req.Question, req.Answer)
	if err != nil {
		return nil, err
	}

	displayOrder := 0
	if req.DisplayOrder != nil {
		displayOrder = *req.DisplayOrder
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	faq := &domain.FAQ{
		Question:     cleanQ,
		Answer:       cleanA,
		DisplayOrder: displayOrder,
		IsActive:     isActive,
	}

	if err := s.repo.Create(ctx, faq); err != nil {
		return nil, apperr.Internal("DB_ERROR", "Failed to create FAQ")
	}

	return toFAQResponse(faq), nil
}

// GetByID retrieves a single FAQ by its UUID.
func (s *Service) GetByID(ctx context.Context, id string) (*dto.FAQResponse, error) {
	faq, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(http.StatusNotFound, "NOT_FOUND", "FAQ not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to fetch FAQ")
	}
	return toFAQResponse(faq), nil
}

// ListAdmin retrieves a paginated view of all FAQs (active and inactive) sorted by display order.
func (s *Service) ListAdmin(ctx context.Context, page, size int) (*httpx.Paged, error) {
	items, total, err := s.repo.ListAdmin(ctx, page, size)
	if err != nil {
		return nil, apperr.Internal("DB_ERROR", "Failed to list FAQs")
	}

	responses := make([]dto.FAQResponse, len(items))
	for i := range items {
		responses[i] = *toFAQResponse(&items[i])
	}

	paged := httpx.NewPaged(responses, page, size, total)
	return &paged, nil
}

// Update updates an existing FAQ record.
func (s *Service) Update(ctx context.Context, id string, req dto.UpdateFAQRequest) (*dto.FAQResponse, error) {
	cleanQ, cleanA, err := s.validateInput(req.Question, req.Answer)
	if err != nil {
		return nil, err
	}

	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(http.StatusNotFound, "NOT_FOUND", "FAQ not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to find FAQ for update")
	}

	displayOrder := existing.DisplayOrder
	if req.DisplayOrder != nil {
		displayOrder = *req.DisplayOrder
	}

	isActive := existing.IsActive
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	existing.Question = cleanQ
	existing.Answer = cleanA
	existing.DisplayOrder = displayOrder
	existing.IsActive = isActive

	if err := s.repo.Update(ctx, existing); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(http.StatusNotFound, "NOT_FOUND", "FAQ not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to update FAQ")
	}

	return toFAQResponse(existing), nil
}

// Delete marks an FAQ as deleted.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apperr.New(http.StatusNotFound, "NOT_FOUND", "FAQ not found")
		}
		return apperr.Internal("DB_ERROR", "Failed to delete FAQ")
	}
	return nil
}

// ListPublic retrieves only active FAQs for public/mobile display.
func (s *Service) ListPublic(ctx context.Context) ([]dto.PublicFAQResponse, error) {
	items, err := s.repo.ListActivePublic(ctx)
	if err != nil {
		return nil, apperr.Internal("DB_ERROR", "Failed to retrieve public FAQs")
	}

	responses := make([]dto.PublicFAQResponse, len(items))
	for i := range items {
		responses[i] = dto.PublicFAQResponse{
			ID:           items[i].ID,
			Question:     items[i].Question,
			Answer:       items[i].Answer,
			DisplayOrder: items[i].DisplayOrder,
		}
	}

	return responses, nil
}

// validateInput ensures required fields are present and safe.
func (s *Service) validateInput(question, answer string) (string, string, error) {
	q := strings.TrimSpace(question)
	if q == "" {
		return "", "", apperr.BadRequest("VALIDATION_ERROR", "Question is required and cannot be empty")
	}
	if len([]rune(q)) > 500 {
		return "", "", apperr.BadRequest("VALIDATION_ERROR", "Question must be at most 500 characters")
	}

	a := strings.TrimSpace(answer)
	if a == "" {
		return "", "", apperr.BadRequest("VALIDATION_ERROR", "Answer is required and cannot be empty")
	}

	cleanAnswer := sanitizer.SanitizeHTML(a)
	if strings.TrimSpace(cleanAnswer) == "" {
		return "", "", apperr.BadRequest("VALIDATION_ERROR", "Answer contains no valid content")
	}

	return q, cleanAnswer, nil
}

func toFAQResponse(f *domain.FAQ) *dto.FAQResponse {
	return &dto.FAQResponse{
		ID:           f.ID,
		Question:     f.Question,
		Answer:       f.Answer,
		DisplayOrder: f.DisplayOrder,
		IsActive:     f.IsActive,
		CreatedAt:    f.CreatedAt,
		UpdatedAt:    f.UpdatedAt,
	}
}
