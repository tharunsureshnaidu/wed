package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	contacthandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/handler"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

const testSecret = "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v2W3x4Y5z6A7b8C9d0E1f2"

type mockContactService struct {
	createFunc  func(ctx context.Context, req dto.CreateContactRequest) (*dto.ContactResponse, error)
	getByIDFunc func(ctx context.Context, id string) (*dto.ContactResponse, error)
	listFunc    func(ctx context.Context, filter dto.ContactFilter) (*httpx.Paged, error)
	updateFunc  func(ctx context.Context, id string, req dto.UpdateContactRequest) (*dto.ContactResponse, error)
	deleteFunc  func(ctx context.Context, id string) error
}

func (m *mockContactService) Create(ctx context.Context, req dto.CreateContactRequest) (*dto.ContactResponse, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, req)
	}
	return &dto.ContactResponse{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		Name:      req.Name,
		Email:     req.Email,
		Phone:     req.Phone,
		Subject:   req.Subject,
		Message:   req.Message,
		Status:    "OPEN",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *mockContactService) GetByID(ctx context.Context, id string) (*dto.ContactResponse, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return &dto.ContactResponse{
		ID:        id,
		Name:      "John Doe",
		Email:     "john@example.com",
		Subject:   "General Inquiry",
		Message:   "Test message",
		Status:    "OPEN",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *mockContactService) List(ctx context.Context, filter dto.ContactFilter) (*httpx.Paged, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, filter)
	}
	items := []dto.ContactResponse{
		{
			ID:        "550e8400-e29b-41d4-a716-446655440000",
			Name:      "John Doe",
			Email:     "john@example.com",
			Subject:   "General Inquiry",
			Message:   "Test message",
			Status:    "OPEN",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	p := httpx.NewPaged(items, filter.Page, filter.Size, 1)
	return &p, nil
}

func (m *mockContactService) Update(ctx context.Context, id string, req dto.UpdateContactRequest) (*dto.ContactResponse, error) {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, id, req)
	}
	status := "IN_PROGRESS"
	if req.Status != nil {
		status = *req.Status
	}
	return &dto.ContactResponse{
		ID:            id,
		Name:          "John Doe",
		Email:         "john@example.com",
		Subject:       "General Inquiry",
		Message:       "Test message",
		Status:        status,
		AdminResponse: req.AdminResponse,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}, nil
}

func (m *mockContactService) Delete(ctx context.Context, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
	}
	return nil
}

func setupTestMux(t *testing.T, svc *mockContactService) (*http.ServeMux, *jwt.Signer) {
	t.Helper()
	signer, err := jwt.NewSigner(testSecret, time.Hour)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	mux := http.NewServeMux()
	contacthandler.NewWithService(svc, signer).Register(mux)
	return mux, signer
}

func mintToken(t *testing.T, signer *jwt.Signer, role string) string {
	t.Helper()
	token, err := signer.Generate("admin@example.com", "1", role)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	return token
}

// ---------------------------------------------------------------------------
// Public Create API Tests
// ---------------------------------------------------------------------------

func TestPublicCreate_NoTokenRequired_Success(t *testing.T) {
	mux, _ := setupTestMux(t, &mockContactService{})

	payload := `{"name":"Aarav Sharma","email":"aarav@example.com","phone":"+919876543210","subject":"Hall Pricing","message":"What is the per day package?"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/contact-us", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	var env struct {
		Success bool                `json:"success"`
		Message string              `json:"message"`
		Data    dto.ContactResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !env.Success {
		t.Errorf("expected success true, got false")
	}
	if env.Data.ID == "" {
		t.Errorf("expected non-empty data.id")
	}
	if env.Data.Name != "Aarav Sharma" {
		t.Errorf("expected name Aarav Sharma, got %s", env.Data.Name)
	}
}

func TestPublicCreate_ValidationError_Returns400(t *testing.T) {
	svc := &mockContactService{
		createFunc: func(ctx context.Context, req dto.CreateContactRequest) (*dto.ContactResponse, error) {
			return nil, apperr.BadRequest("VALIDATION_ERROR", "Email is required")
		},
	}
	mux, _ := setupTestMux(t, svc)

	payload := `{"name":"Aarav Sharma","subject":"Hall Pricing","message":"What is the per day package?"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/contact-us", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d. Body: %s", rec.Code, rec.Body.String())
	}
}

func TestPublicCreate_MalformedJSON_Returns400(t *testing.T) {
	mux, _ := setupTestMux(t, &mockContactService{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/contact-us", bytes.NewBufferString("{invalid-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request for malformed JSON, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Admin List API Tests (Auth & RBAC)
// ---------------------------------------------------------------------------

func TestAdminList_RoleEnforcement(t *testing.T) {
	mux, signer := setupTestMux(t, &mockContactService{})

	tests := []struct {
		name         string
		role         string
		includeToken bool
		expectedCode int
	}{
		{"no token", "", false, http.StatusUnauthorized},
		{"customer role", domain.RoleCustomer, true, http.StatusForbidden},
		{"hall owner role", domain.RoleHallOwner, true, http.StatusForbidden},
		{"admin role", domain.RoleAdmin, true, http.StatusOK},
		{"super admin role", domain.RoleSuperAdmin, true, http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/contact-us?page=0&size=20", nil)
			if tc.includeToken {
				req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, tc.role))
			}
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != tc.expectedCode {
				t.Errorf("expected status %d, got %d. Body: %s", tc.expectedCode, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminList_WithStatusFilter(t *testing.T) {
	filterStatusReceived := ""
	svc := &mockContactService{
		listFunc: func(ctx context.Context, filter dto.ContactFilter) (*httpx.Paged, error) {
			if filter.Status != nil {
				filterStatusReceived = *filter.Status
			}
			p := httpx.NewPaged([]dto.ContactResponse{}, filter.Page, filter.Size, 0)
			return &p, nil
		},
	}
	mux, signer := setupTestMux(t, svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/contact-us?status=OPEN&page=1&size=20", nil)
	req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleAdmin))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}
	if filterStatusReceived != "OPEN" {
		t.Errorf("expected filter status OPEN, got %s", filterStatusReceived)
	}
}

// ---------------------------------------------------------------------------
// Admin GetByID API Tests
// ---------------------------------------------------------------------------

func TestAdminGetByID_Success(t *testing.T) {
	mux, signer := setupTestMux(t, &mockContactService{})

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contact-us/"+validUUID, nil)
	req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleAdmin))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminGetByID_NotFound_Returns404(t *testing.T) {
	svc := &mockContactService{
		getByIDFunc: func(ctx context.Context, id string) (*dto.ContactResponse, error) {
			return nil, apperr.New(http.StatusNotFound, "NOT_FOUND", "Contact request not found")
		},
	}
	mux, signer := setupTestMux(t, svc)

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contact-us/"+validUUID, nil)
	req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleSuperAdmin))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 Not Found, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Admin Update API Tests
// ---------------------------------------------------------------------------

func TestAdminUpdate_Success(t *testing.T) {
	mux, signer := setupTestMux(t, &mockContactService{})

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	payload := `{"status":"IN_PROGRESS","adminResponse":"Our executive will call you."}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/contact-us/"+validUUID, bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleAdmin))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminUpdate_InvalidTransition_Returns400(t *testing.T) {
	svc := &mockContactService{
		updateFunc: func(ctx context.Context, id string, req dto.UpdateContactRequest) (*dto.ContactResponse, error) {
			return nil, apperr.BadRequest("VALIDATION_ERROR", "Cannot transition status from CLOSED to RESOLVED")
		},
	}
	mux, signer := setupTestMux(t, svc)

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	payload := `{"status":"RESOLVED"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/contact-us/"+validUUID, bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleAdmin))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Admin Delete API Tests
// ---------------------------------------------------------------------------

func TestAdminDelete_Success(t *testing.T) {
	mux, signer := setupTestMux(t, &mockContactService{})

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/contact-us/"+validUUID, nil)
	req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleSuperAdmin))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminDelete_NotFound_Returns404(t *testing.T) {
	svc := &mockContactService{
		deleteFunc: func(ctx context.Context, id string) error {
			return apperr.New(http.StatusNotFound, "NOT_FOUND", "Contact request not found")
		},
	}
	mux, signer := setupTestMux(t, svc)

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/contact-us/"+validUUID, nil)
	req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleAdmin))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 Not Found, got %d", rec.Code)
	}
}

