package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
)

// Service defines the business logic operations for Help Center messages.
type Service interface {
	Submit(ctx context.Context, userID int64, req dto.CreateHelpMessageRequest) (*dto.HelpMessageResponse, error)
	GetByID(ctx context.Context, id string) (*dto.HelpMessageResponse, error)
	ListAll(ctx context.Context, filter dto.HelpMessageFilter) (*httpx.Paged, error)
	Delete(ctx context.Context, id string) error
	SetOnMessageCreated(fn func(ctx context.Context, msg *domain.HelpCenterMessage))
}

type service struct {
	repo             repository.Repository
	onMessageCreated func(ctx context.Context, msg *domain.HelpCenterMessage)
}

func New(repo repository.Repository) Service {
	return &service{repo: repo}
}

func (s *service) SetOnMessageCreated(fn func(ctx context.Context, msg *domain.HelpCenterMessage)) {
	s.onMessageCreated = fn
}

func (s *service) Submit(ctx context.Context, userID int64, req dto.CreateHelpMessageRequest) (*dto.HelpMessageResponse, error) {
	if userID <= 0 {
		return nil, apperr.New(http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
	}

	content := strings.TrimSpace(req.Message)
	if content == "" {
		return nil, apperr.New(http.StatusBadRequest, "Message is required", "VALIDATION_ERROR")
	}

	if len([]rune(content)) > 2000 {
		return nil, apperr.New(http.StatusBadRequest, "Message must be 2000 characters or fewer", "VALIDATION_ERROR")
	}

	user, err := s.repo.GetUserProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apperr.New(http.StatusNotFound, "User not found", "NOT_FOUND")
		}
		return nil, err
	}

	name := strings.TrimSpace(user.Name)
	if name == "" {
		name = "User"
	}

	entity := &domain.HelpCenterMessage{
		UserID:    &user.ID,
		UserName:  name,
		UserEmail: user.Email,
		UserPhone: user.Phone,
		Message:   content,
		Status:    domain.StatusNew,
	}

	saved, err := s.repo.Create(ctx, entity)
	if err != nil {
		return nil, err
	}

	// Trigger Super Admin notification safely if hook is registered.
	// Notification failure never aborts or rolls back the saved message.
	if s.onMessageCreated != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("help: onMessageCreated hook panicked", "id", saved.ID)
				}
			}()
			s.onMessageCreated(ctx, saved)
		}()
	}

	return toDTO(saved), nil
}

func (s *service) GetByID(ctx context.Context, id string) (*dto.HelpMessageResponse, error) {
	if !httpx.ValidUUID(id) {
		return nil, apperr.New(http.StatusBadRequest, "Invalid message id", "VALIDATION_ERROR")
	}

	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(http.StatusNotFound, "Help message not found", "NOT_FOUND")
		}
		return nil, err
	}

	// When Super Admin views a NEW message, mark it as READ.
	if item.Status == domain.StatusNew {
		if err := s.repo.MarkAsRead(ctx, id); err != nil {
			logger.Error("help: mark as read failed", "id", id, logger.Err(err))
		} else {
			item.Status = domain.StatusRead
		}
	}

	return toDTO(item), nil
}

func (s *service) ListAll(ctx context.Context, filter dto.HelpMessageFilter) (*httpx.Paged, error) {
	if filter.Status != nil && strings.TrimSpace(*filter.Status) != "" {
		upper := strings.ToUpper(strings.TrimSpace(*filter.Status))
		if !domain.HelpCenterStatus(upper).Valid() {
			return nil, apperr.New(http.StatusBadRequest, "Status filter must be NEW or READ", "VALIDATION_ERROR")
		}
		filter.Status = &upper
	}

	if filter.FromDate != nil && filter.ToDate != nil && filter.FromDate.After(*filter.ToDate) {
		return nil, apperr.New(http.StatusBadRequest, "from_date cannot be after to_date", "VALIDATION_ERROR")
	}

	items, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	responses := make([]dto.HelpMessageResponse, len(items))
	for i, it := range items {
		responses[i] = *toDTO(&it)
	}

	p := httpx.NewPaged(responses, filter.Page, filter.Limit, total)
	return &p, nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	if !httpx.ValidUUID(id) {
		return apperr.New(http.StatusBadRequest, "Invalid message id", "VALIDATION_ERROR")
	}

	err := s.repo.Delete(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apperr.New(http.StatusNotFound, "Help message not found", "NOT_FOUND")
		}
		return err
	}

	return nil
}

func toDTO(item *domain.HelpCenterMessage) *dto.HelpMessageResponse {
	if item == nil {
		return nil
	}

	createdAt := item.CreatedAt
	updatedAt := item.UpdatedAt

	return &dto.HelpMessageResponse{
		ID:             item.ID,
		UserID:         item.UserID,
		UserIDSnake:    item.UserID,
		UserName:       item.UserName,
		UserNameSnake:  item.UserName,
		UserEmail:      item.UserEmail,
		UserEmailSnake: item.UserEmail,
		UserPhone:      item.UserPhone,
		UserPhoneSnake: item.UserPhone,
		Message:        item.Message,
		Status:         string(item.Status),
		CreatedAt:      createdAt,
		CreatedAtSnake: &createdAt,
		UpdatedAt:      updatedAt,
		UpdatedAtSnake: &updatedAt,
	}
}
