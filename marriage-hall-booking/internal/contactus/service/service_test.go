package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
)

type mockRepository struct {
	createFunc  func(ctx context.Context, item *domain.ContactUs) (*domain.ContactUs, error)
	getByIDFunc func(ctx context.Context, id string) (*domain.ContactUs, error)
	listFunc    func(ctx context.Context, filter dto.ContactFilter) ([]domain.ContactUs, int64, error)
	updateFunc  func(ctx context.Context, id string, status domain.Status, adminResponse *string) (*domain.ContactUs, error)
	deleteFunc  func(ctx context.Context, id string) error
}

func (m *mockRepository) Create(ctx context.Context, item *domain.ContactUs) (*domain.ContactUs, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, item)
	}
	return &domain.ContactUs{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		Name:      item.Name,
		Email:     item.Email,
		Phone:     item.Phone,
		Subject:   item.Subject,
		Message:   item.Message,
		Status:    domain.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *mockRepository) GetByID(ctx context.Context, id string) (*domain.ContactUs, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return &domain.ContactUs{
		ID:        id,
		Name:      "John Doe",
		Email:     "john@example.com",
		Subject:   "General Inquiry",
		Message:   "Hello from testing",
		Status:    domain.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *mockRepository) List(ctx context.Context, filter dto.ContactFilter) ([]domain.ContactUs, int64, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, filter)
	}
	items := []domain.ContactUs{
		{
			ID:        "550e8400-e29b-41d4-a716-446655440000",
			Name:      "John Doe",
			Email:     "john@example.com",
			Subject:   "General Inquiry",
			Message:   "Hello from testing",
			Status:    domain.StatusOpen,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	return items, 1, nil
}

func (m *mockRepository) Update(ctx context.Context, id string, status domain.Status, adminResponse *string) (*domain.ContactUs, error) {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, id, status, adminResponse)
	}
	return &domain.ContactUs{
		ID:            id,
		Name:          "John Doe",
		Email:         "john@example.com",
		Subject:       "General Inquiry",
		Message:       "Hello from testing",
		Status:        status,
		AdminResponse: adminResponse,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}, nil
}

func (m *mockRepository) Delete(ctx context.Context, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
	}
	return nil
}

func strPtr(s string) *string {
	return &s
}

// ---------------------------------------------------------------------------
// Create Tests
// ---------------------------------------------------------------------------

func TestCreate_Success(t *testing.T) {
	repo := &mockRepository{}
	svc := service.New(repo)

	req := dto.CreateContactRequest{
		Name:    "Ramesh Kumar",
		Email:   "ramesh@example.com",
		Phone:   strPtr("+919876543210"),
		Subject: "Hall Availability Inquiry",
		Message: "I want to enquire about marriage hall bookings for December 2026.",
	}

	res, err := svc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if res.ID == "" {
		t.Error("expected non-empty ID")
	}
	if res.Status != string(domain.StatusOpen) {
		t.Errorf("expected initial status OPEN, got %s", res.Status)
	}
	if res.Name != "Ramesh Kumar" {
		t.Errorf("expected name Ramesh Kumar, got %s", res.Name)
	}
	if res.Email != "ramesh@example.com" {
		t.Errorf("expected email ramesh@example.com, got %s", res.Email)
	}
	if res.Phone == nil || *res.Phone != "+919876543210" {
		t.Errorf("expected phone +919876543210, got %v", res.Phone)
	}
}

func TestCreate_ValidationErrors(t *testing.T) {
	svc := service.New(&mockRepository{})

	tests := []struct {
		name    string
		req     dto.CreateContactRequest
		errPart string
	}{
		{
			name: "missing name",
			req: dto.CreateContactRequest{
				Email:   "test@example.com",
				Subject: "Sub",
				Message: "Msg",
			},
			errPart: "Name is required",
		},
		{
			name: "name too long",
			req: dto.CreateContactRequest{
				Name:    strings.Repeat("A", 101),
				Email:   "test@example.com",
				Subject: "Sub",
				Message: "Msg",
			},
			errPart: "Name must be at most 100 characters",
		},
		{
			name: "missing email",
			req: dto.CreateContactRequest{
				Name:    "John",
				Subject: "Sub",
				Message: "Msg",
			},
			errPart: "Email is required",
		},
		{
			name: "invalid email format",
			req: dto.CreateContactRequest{
				Name:    "John",
				Email:   "invalid-email",
				Subject: "Sub",
				Message: "Msg",
			},
			errPart: "Invalid email format",
		},
		{
			name: "invalid phone number format",
			req: dto.CreateContactRequest{
				Name:    "John",
				Email:   "john@example.com",
				Phone:   strPtr("abc-123"),
				Subject: "Sub",
				Message: "Msg",
			},
			errPart: "Invalid phone number format",
		},
		{
			name: "missing subject",
			req: dto.CreateContactRequest{
				Name:    "John",
				Email:   "john@example.com",
				Message: "Msg",
			},
			errPart: "Subject is required",
		},
		{
			name: "subject too long",
			req: dto.CreateContactRequest{
				Name:    "John",
				Email:   "john@example.com",
				Subject: strings.Repeat("S", 201),
				Message: "Msg",
			},
			errPart: "Subject must be at most 200 characters",
		},
		{
			name: "missing message",
			req: dto.CreateContactRequest{
				Name:    "John",
				Email:   "john@example.com",
				Subject: "Sub",
			},
			errPart: "Message is required",
		},
		{
			name: "message too long",
			req: dto.CreateContactRequest{
				Name:    "John",
				Email:   "john@example.com",
				Subject: "Sub",
				Message: strings.Repeat("M", 5001),
			},
			errPart: "Message must be at most 5000 characters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errPart) {
				t.Errorf("expected error to contain %q, got %q", tc.errPart, err.Error())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GetByID Tests
// ---------------------------------------------------------------------------

func TestGetByID_Success(t *testing.T) {
	repo := &mockRepository{}
	svc := service.New(repo)

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	res, err := svc.GetByID(context.Background(), validUUID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.ID != validUUID {
		t.Errorf("expected ID %s, got %s", validUUID, res.ID)
	}
}

func TestGetByID_InvalidUUID(t *testing.T) {
	svc := service.New(&mockRepository{})
	_, err := svc.GetByID(context.Background(), "invalid-uuid")
	if err == nil {
		t.Fatalf("expected error for invalid UUID, got nil")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
		t.Errorf("expected VALIDATION_ERROR apperr, got %v", err)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	repo := &mockRepository{
		getByIDFunc: func(ctx context.Context, id string) (*domain.ContactUs, error) {
			return nil, repository.ErrNotFound
		},
	}
	svc := service.New(repo)

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	_, err := svc.GetByID(context.Background(), validUUID)
	if err == nil {
		t.Fatalf("expected not found error, got nil")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "NOT_FOUND" {
		t.Errorf("expected NOT_FOUND apperr, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// List Tests
// ---------------------------------------------------------------------------

func TestList_Success(t *testing.T) {
	repo := &mockRepository{}
	svc := service.New(repo)

	paged, err := svc.List(context.Background(), dto.ContactFilter{
		Page: 0,
		Size: 20,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if paged.TotalElements != 1 {
		t.Errorf("expected 1 total element, got %d", paged.TotalElements)
	}
}

func TestList_WithStatusFilter(t *testing.T) {
	calledWithStatus := ""
	repo := &mockRepository{
		listFunc: func(ctx context.Context, filter dto.ContactFilter) ([]domain.ContactUs, int64, error) {
			if filter.Status != nil {
				calledWithStatus = *filter.Status
			}
			return nil, 0, nil
		},
	}
	svc := service.New(repo)

	_, err := svc.List(context.Background(), dto.ContactFilter{
		Status: strPtr("OPEN"),
		Page:   0,
		Size:   20,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calledWithStatus != "OPEN" {
		t.Errorf("expected filter status OPEN, got %s", calledWithStatus)
	}
}

func TestList_InvalidStatusFilter(t *testing.T) {
	svc := service.New(&mockRepository{})
	_, err := svc.List(context.Background(), dto.ContactFilter{
		Status: strPtr("UNKNOWN_STATUS"),
	})
	if err == nil {
		t.Fatalf("expected error for invalid status, got nil")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
		t.Errorf("expected VALIDATION_ERROR apperr, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Update Tests
// ---------------------------------------------------------------------------

func TestUpdate_Success(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	repo := &mockRepository{
		getByIDFunc: func(ctx context.Context, id string) (*domain.ContactUs, error) {
			return &domain.ContactUs{
				ID:      id,
				Status:  domain.StatusOpen,
				Message: "Original message",
			}, nil
		},
		updateFunc: func(ctx context.Context, id string, status domain.Status, adminResponse *string) (*domain.ContactUs, error) {
			return &domain.ContactUs{
				ID:            id,
				Status:        status,
				AdminResponse: adminResponse,
				Message:       "Original message",
			}, nil
		},
	}
	svc := service.New(repo)

	req := dto.UpdateContactRequest{
		Status:        strPtr("IN_PROGRESS"),
		AdminResponse: strPtr("We are reviewing your request."),
	}

	res, err := svc.Update(context.Background(), validUUID, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.Status != "IN_PROGRESS" {
		t.Errorf("expected status IN_PROGRESS, got %s", res.Status)
	}
	if res.AdminResponse == nil || *res.AdminResponse != "We are reviewing your request." {
		t.Errorf("expected admin response set, got %v", res.AdminResponse)
	}
	if res.Message != "Original message" {
		t.Errorf("expected original message preserved, got %s", res.Message)
	}
}

func TestUpdate_InvalidStatusTransition(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	repo := &mockRepository{
		getByIDFunc: func(ctx context.Context, id string) (*domain.ContactUs, error) {
			return &domain.ContactUs{
				ID:     id,
				Status: domain.StatusClosed,
			}, nil
		},
	}
	svc := service.New(repo)

	// CLOSED cannot transition directly to RESOLVED
	req := dto.UpdateContactRequest{
		Status: strPtr("RESOLVED"),
	}

	_, err := svc.Update(context.Background(), validUUID, req)
	if err == nil {
		t.Fatalf("expected error for invalid transition, got nil")
	}
	if !strings.Contains(err.Error(), "Cannot transition status from CLOSED to RESOLVED") {
		t.Errorf("expected transition error message, got %v", err)
	}
}

func TestUpdate_MissingAllFields(t *testing.T) {
	svc := service.New(&mockRepository{})
	validUUID := "550e8400-e29b-41d4-a716-446655440000"

	req := dto.UpdateContactRequest{}
	_, err := svc.Update(context.Background(), validUUID, req)
	if err == nil {
		t.Fatalf("expected error when no update fields provided, got nil")
	}
}

func TestUpdate_NotFound(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	repo := &mockRepository{
		getByIDFunc: func(ctx context.Context, id string) (*domain.ContactUs, error) {
			return nil, repository.ErrNotFound
		},
	}
	svc := service.New(repo)

	req := dto.UpdateContactRequest{
		Status: strPtr("IN_PROGRESS"),
	}

	_, err := svc.Update(context.Background(), validUUID, req)
	if err == nil {
		t.Fatalf("expected not found error, got nil")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "NOT_FOUND" {
		t.Errorf("expected NOT_FOUND apperr, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Delete Tests
// ---------------------------------------------------------------------------

func TestDelete_Success(t *testing.T) {
	repo := &mockRepository{}
	svc := service.New(repo)

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	err := svc.Delete(context.Background(), validUUID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDelete_InvalidUUID(t *testing.T) {
	svc := service.New(&mockRepository{})
	err := svc.Delete(context.Background(), "invalid-uuid")
	if err == nil {
		t.Fatalf("expected error for invalid UUID, got nil")
	}
}

func TestDelete_NotFound(t *testing.T) {
	repo := &mockRepository{
		deleteFunc: func(ctx context.Context, id string) error {
			return repository.ErrNotFound
		},
	}
	svc := service.New(repo)

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	err := svc.Delete(context.Background(), validUUID)
	if err == nil {
		t.Fatalf("expected not found error, got nil")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "NOT_FOUND" {
		t.Errorf("expected NOT_FOUND apperr, got %v", err)
	}
}
