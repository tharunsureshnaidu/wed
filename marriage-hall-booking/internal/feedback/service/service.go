package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
)

// Service defines the business logic operations for app feedback.
type Service interface {
	Submit(ctx context.Context, userID int64, req dto.CreateFeedbackRequest, attachmentURL *string) (*dto.FeedbackResponse, error)
	GetByID(ctx context.Context, id string) (*dto.FeedbackResponse, error)
	ListAll(ctx context.Context, filter dto.FeedbackFilter) (*httpx.Paged, error)
	ListMine(ctx context.Context, userID int64, page, size int) (*httpx.Paged, error)
	UpdateStatus(ctx context.Context, id string, adminID int64, req dto.UpdateFeedbackRequest) (*dto.FeedbackResponse, error)
	GetSummary(ctx context.Context) (*dto.AdminFeedbackSummaryResponse, error)
	SetOnSubmitted(fn func(ctx context.Context, id, message string, rating *int))
}

type service struct {
	repo        repository.Repository
	onSubmitted func(ctx context.Context, id, message string, rating *int)
}

func New(repo repository.Repository) Service {
	return &service{repo: repo}
}

func (s *service) SetOnSubmitted(fn func(ctx context.Context, id, message string, rating *int)) {
	s.onSubmitted = fn
}

func (s *service) Submit(ctx context.Context, userID int64, req dto.CreateFeedbackRequest, attachmentURL *string) (*dto.FeedbackResponse, error) {
	if userID <= 0 {
		return nil, apperr.New(http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
	}

	content := strings.TrimSpace(req.GetContent())
	if req.Rating == nil && content == "" {
		return nil, apperr.New(http.StatusBadRequest, "Either a rating or feedback message is required", "VALIDATION_ERROR")
	}

	if req.Rating != nil && (*req.Rating < 1 || *req.Rating > 5) {
		return nil, apperr.New(http.StatusBadRequest, "Rating must be between 1 and 5", "VALIDATION_ERROR")
	}

	if len([]rune(content)) > 1000 {
		return nil, apperr.New(http.StatusBadRequest, "Feedback message must be 1000 characters or fewer", "VALIDATION_ERROR")
	}

	var platform *string
	if req.Platform != nil && strings.TrimSpace(*req.Platform) != "" {
		p := strings.ToUpper(strings.TrimSpace(*req.Platform))
		switch p {
		case "ANDROID", "IOS", "WEB":
			platform = &p
		default:
			return nil, apperr.New(http.StatusBadRequest, "Platform must be ANDROID, IOS or WEB", "VALIDATION_ERROR")
		}
	}

	var appVersion *string
	if req.AppVersion != nil && strings.TrimSpace(*req.AppVersion) != "" {
		v := strings.TrimSpace(*req.AppVersion)
		if len(v) > 50 {
			v = v[:50]
		}
		appVersion = &v
	}
// Ensure a user can submit only once
if _, err := s.repo.GetByUserID(ctx, userID); err == nil {
	return nil, apperr.New(http.StatusConflict, "Feedback already submitted", "ALREADY_EXISTS")
} else if err != repository.ErrNotFound {
	return nil, err
}

	entity := &domain.Feedback{
		UserID:        &userID,
		Rating:        req.Rating,
		Message:       content,
		AttachmentURL: attachmentURL,
		AppVersion:    appVersion,
		Platform:      platform,
		Status:        domain.StatusNew,
	}

	saved, err := s.repo.Create(ctx, entity)
	if err != nil {
		return nil, err
	}

	// Trigger async/background notifications safely if hook is registered
	if s.onSubmitted != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("feedback: onSubmitted hook panicked", "id", saved.ID)
				}
			}()
			s.onSubmitted(ctx, saved.ID, saved.Message, saved.Rating)
		}()
	}

	return toDTO(&domain.FeedbackWithUser{Feedback: *saved}), nil
}

func (s *service) GetByID(ctx context.Context, id string) (*dto.FeedbackResponse, error) {
	if !httpx.ValidUUID(id) {
		return nil, apperr.New(http.StatusBadRequest, "Invalid feedback id", "VALIDATION_ERROR")
	}

	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(http.StatusNotFound, "Feedback not found", "NOT_FOUND")
		}
		return nil, err
	}

	return toDTO(item), nil
}

func (s *service) ListAll(ctx context.Context, filter dto.FeedbackFilter) (*httpx.Paged, error) {
	if filter.Rating != nil && (*filter.Rating < 1 || *filter.Rating > 5) {
		return nil, apperr.New(http.StatusBadRequest, "Rating filter must be between 1 and 5", "VALIDATION_ERROR")
	}

	if filter.Status != nil && strings.TrimSpace(*filter.Status) != "" {
		upper := strings.ToUpper(strings.TrimSpace(*filter.Status))
		if !domain.FeedbackStatus(upper).Valid() {
			return nil, apperr.New(http.StatusBadRequest, "Status must be NEW, REVIEWING, RESOLVED or CLOSED", "VALIDATION_ERROR")
		}
		filter.Status = &upper
	}

	items, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	responses := make([]dto.FeedbackResponse, len(items))
	for i, it := range items {
		responses[i] = *toDTO(&it)
	}

	p := httpx.NewPaged(responses, filter.Page, filter.Size, total)
	return &p, nil
}

func (s *service) ListMine(ctx context.Context, userID int64, page, size int) (*httpx.Paged, error) {
	if userID <= 0 {
		return nil, apperr.New(http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
	}

	items, total, err := s.repo.ListByUserID(ctx, userID, page, size)
	if err != nil {
		return nil, err
	}

	responses := make([]dto.FeedbackResponse, len(items))
	for i, it := range items {
		responses[i] = *toDTO(&it)
	}

	p := httpx.NewPaged(responses, page, size, total)
	return &p, nil
}

func (s *service) UpdateStatus(ctx context.Context, id string, adminID int64, req dto.UpdateFeedbackRequest) (*dto.FeedbackResponse, error) {
	if !httpx.ValidUUID(id) {
		return nil, apperr.New(http.StatusBadRequest, "Invalid feedback id", "VALIDATION_ERROR")
	}

	if req.Status == nil && req.AdminNote == nil {
		return nil, apperr.New(http.StatusBadRequest, "No fields to update", "VALIDATION_ERROR")
	}

	var statusVal *string
	if req.Status != nil && strings.TrimSpace(*req.Status) != "" {
		upper := strings.ToUpper(strings.TrimSpace(*req.Status))
		if !domain.FeedbackStatus(upper).Valid() {
			return nil, apperr.New(http.StatusBadRequest, "Status must be NEW, REVIEWING, RESOLVED or CLOSED", "VALIDATION_ERROR")
		}
		statusVal = &upper
	}

	var adminNoteVal *string
	if req.AdminNote != nil {
		note := strings.TrimSpace(*req.AdminNote)
		if len([]rune(note)) > 1000 {
			return nil, apperr.New(http.StatusBadRequest, "Admin note must be 1000 characters or fewer", "VALIDATION_ERROR")
		}
		adminNoteVal = &note
	}

	updated, err := s.repo.Update(ctx, id, statusVal, adminNoteVal, adminID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(http.StatusNotFound, "Feedback not found", "NOT_FOUND")
		}
		return nil, err
	}

	return toDTO(updated), nil
}

func (s *service) GetSummary(ctx context.Context) (*dto.AdminFeedbackSummaryResponse, error) {
	summary, err := s.repo.GetSummary(ctx)
	if err != nil {
		return nil, err
	}

	return &dto.AdminFeedbackSummaryResponse{
		TotalReviews:  summary.TotalReviews,
		AverageRating: summary.AverageRating,
		FiveStar:      summary.FiveStar,
		FourStar:      summary.FourStar,
		ThreeStar:     summary.ThreeStar,
		TwoStar:       summary.TwoStar,
		OneStar:       summary.OneStar,
	}, nil
}

func toDTO(item *domain.FeedbackWithUser) *dto.FeedbackResponse {
	if item == nil {
		return nil
	}
	return &dto.FeedbackResponse{
		ID:            item.ID,
		UserID:        item.UserID,
		UserName:      item.UserName,
		UserEmail:     item.UserEmail,
		Rating:        item.Rating,
		Feedback:      item.Message,
		Message:       item.Message,
		AttachmentURL: item.AttachmentURL,
		AppVersion:    item.AppVersion,
		Platform:      item.Platform,
		Status:        string(item.Status),
		AdminNote:     item.AdminNote,
		CreatedAt:     item.CreatedAt,
		ResolvedAt:    item.ResolvedAt,
	}
}
