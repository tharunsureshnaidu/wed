package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
)

var phoneRe = regexp.MustCompile(`^\+?[1-9]\d{1,14}$`)

// Service defines the business logic contract for Contact Us management.
type Service interface {
	Create(ctx context.Context, req dto.CreateContactRequest) (*dto.ContactResponse, error)
	GetByID(ctx context.Context, id string) (*dto.ContactResponse, error)
	List(ctx context.Context, filter dto.ContactFilter) (*httpx.Paged, error)
	Update(ctx context.Context, id string, req dto.UpdateContactRequest) (*dto.ContactResponse, error)
	Delete(ctx context.Context, id string) error
}

type service struct {
	repo repository.Repository
}

// New constructs a new Contact Us service.
func New(repo repository.Repository) Service {
	return &service{repo: repo}
}

func (s *service) Create(ctx context.Context, req dto.CreateContactRequest) (*dto.ContactResponse, error) {
	var errs validate.Errors

	name := strings.TrimSpace(req.Name)
	if name == "" {
		errs.Required("Name", "")
	} else if len([]rune(name)) > 100 {
		errs = append(errs, "Name must be at most 100 characters")
	}

	email := strings.TrimSpace(req.Email)
	if email == "" {
		errs.Required("Email", "")
	} else if _, err := mail.ParseAddress(email); err != nil {
		errs = append(errs, "Invalid email format")
	}

	var phone *string
	if req.Phone != nil {
		trimmed := strings.TrimSpace(*req.Phone)
		if trimmed != "" {
			if !phoneRe.MatchString(trimmed) {
				errs = append(errs, "Invalid phone number format")
			}
			phone = &trimmed
		}
	}

	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		errs.Required("Subject", "")
	} else if len([]rune(subject)) > 200 {
		errs = append(errs, "Subject must be at most 200 characters")
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		errs.Required("Message", "")
	} else if len([]rune(message)) > 5000 {
		errs = append(errs, "Message must be at most 5000 characters")
	}

	if len(errs) > 0 {
		return nil, apperr.BadRequest("VALIDATION_ERROR", errs.Message())
	}

	item := &domain.ContactUs{
		Name:    name,
		Email:   email,
		Phone:   phone,
		Subject: subject,
		Message: message,
		Status:  domain.StatusOpen,
	}

	saved, err := s.repo.Create(ctx, item)
	if err != nil {
		return nil, apperr.Internal("DB_ERROR", "Failed to submit contact inquiry")
	}

	return toDTO(saved), nil
}

func (s *service) GetByID(ctx context.Context, id string) (*dto.ContactResponse, error) {
	if !httpx.ValidUUID(id) {
		return nil, apperr.BadRequest("VALIDATION_ERROR", "Invalid contact request ID")
	}

	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(http.StatusNotFound, "NOT_FOUND", "Contact request not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to fetch contact request")
	}

	return toDTO(item), nil
}

func (s *service) List(ctx context.Context, filter dto.ContactFilter) (*httpx.Paged, error) {
	if filter.Status != nil && strings.TrimSpace(*filter.Status) != "" {
		upper := strings.ToUpper(strings.TrimSpace(*filter.Status))
		if !domain.Status(upper).Valid() {
			return nil, apperr.BadRequest("VALIDATION_ERROR", "Status filter must be OPEN, IN_PROGRESS, RESOLVED, or CLOSED")
		}
		filter.Status = &upper
	}

	items, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, apperr.Internal("DB_ERROR", "Failed to list contact requests")
	}

	responses := make([]dto.ContactResponse, len(items))
	for i, it := range items {
		responses[i] = *toDTO(&it)
	}

	paged := httpx.NewPaged(responses, filter.Page, filter.Size, total)
	return &paged, nil
}

func (s *service) Update(ctx context.Context, id string, req dto.UpdateContactRequest) (*dto.ContactResponse, error) {
	if !httpx.ValidUUID(id) {
		return nil, apperr.BadRequest("VALIDATION_ERROR", "Invalid contact request ID")
	}

	if req.Status == nil && req.AdminResponse == nil {
		return nil, apperr.BadRequest("VALIDATION_ERROR", "At least one of status or adminResponse must be provided")
	}

	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(http.StatusNotFound, "NOT_FOUND", "Contact request not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to fetch contact request")
	}

	targetStatus := existing.Status
	if req.Status != nil {
		statusStr := strings.ToUpper(strings.TrimSpace(*req.Status))
		st := domain.Status(statusStr)
		if !st.Valid() {
			return nil, apperr.BadRequest("VALIDATION_ERROR", "Status must be OPEN, IN_PROGRESS, RESOLVED, or CLOSED")
		}
		if !existing.Status.CanTransitionTo(st) {
			return nil, apperr.BadRequest("VALIDATION_ERROR", fmt.Sprintf("Cannot transition status from %s to %s", existing.Status, st))
		}
		targetStatus = st
	}

	targetAdminResponse := existing.AdminResponse
	if req.AdminResponse != nil {
		trimmed := strings.TrimSpace(*req.AdminResponse)
		if len([]rune(trimmed)) > 5000 {
			return nil, apperr.BadRequest("VALIDATION_ERROR", "Admin response must be at most 5000 characters")
		}
		targetAdminResponse = &trimmed
	}

	updated, err := s.repo.Update(ctx, id, targetStatus, targetAdminResponse)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.New(http.StatusNotFound, "NOT_FOUND", "Contact request not found")
		}
		return nil, apperr.Internal("DB_ERROR", "Failed to update contact request")
	}

	return toDTO(updated), nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	if !httpx.ValidUUID(id) {
		return apperr.BadRequest("VALIDATION_ERROR", "Invalid contact request ID")
	}

	err := s.repo.Delete(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apperr.New(http.StatusNotFound, "NOT_FOUND", "Contact request not found")
		}
		return apperr.Internal("DB_ERROR", "Failed to delete contact request")
	}

	return nil
}

func toDTO(item *domain.ContactUs) *dto.ContactResponse {
	if item == nil {
		return nil
	}

	return &dto.ContactResponse{
		ID:            item.ID,
		Name:          item.Name,
		Email:         item.Email,
		Phone:         item.Phone,
		Subject:       item.Subject,
		Message:       item.Message,
		Status:        string(item.Status),
		AdminResponse: item.AdminResponse,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
	}
}

