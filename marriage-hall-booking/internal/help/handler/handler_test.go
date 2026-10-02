package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	helpdomain "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/dto"
	helphandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/handler"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

const testSecret = "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v2W3x4Y5z6A7b8C9d0E1f2"

type mockService struct {
	submitFunc func(ctx context.Context, userID int64, req dto.CreateHelpMessageRequest) (*dto.HelpMessageResponse, error)
	getFunc    func(ctx context.Context, id string) (*dto.HelpMessageResponse, error)
	listFunc   func(ctx context.Context, filter dto.HelpMessageFilter) (*httpx.Paged, error)
	deleteFunc func(ctx context.Context, id string) error
}

func (m *mockService) Submit(ctx context.Context, userID int64, req dto.CreateHelpMessageRequest) (*dto.HelpMessageResponse, error) {
	if m.submitFunc != nil {
		return m.submitFunc(ctx, userID, req)
	}
	email := "saif@example.com"
	phone := "9876543210"
	return &dto.HelpMessageResponse{
		ID:        "msg-123",
		UserID:    &userID,
		UserName:  "Saif Ali",
		UserEmail: &email,
		UserPhone: &phone,
		Message:   req.Message,
		Status:    "NEW",
	}, nil
}

func (m *mockService) GetByID(ctx context.Context, id string) (*dto.HelpMessageResponse, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, id)
	}
	return &dto.HelpMessageResponse{
		ID:      id,
		Message: "Found query",
		Status:  "READ",
	}, nil
}

func (m *mockService) ListAll(ctx context.Context, filter dto.HelpMessageFilter) (*httpx.Paged, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, filter)
	}
	items := []dto.HelpMessageResponse{
		{ID: "msg-1", UserName: "Saif Ali", Status: "NEW", Message: "Query 1"},
	}
	p := httpx.NewPaged(items, filter.Page, filter.Limit, 1)
	return &p, nil
}

func (m *mockService) Delete(ctx context.Context, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
	}
	return nil
}

func (m *mockService) SetOnMessageCreated(_ func(ctx context.Context, msg *helpdomain.HelpCenterMessage)) {
}

func setupTestServer(t *testing.T, svc *mockService) (*http.ServeMux, *jwt.Signer) {
	t.Helper()
	signer, err := jwt.NewSigner(testSecret, time.Hour)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	h := helphandler.NewWithService(svc, signer)
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, signer
}

// --------------------------------------------------------------------------
// USER Endpoint Tests
// --------------------------------------------------------------------------

func TestUserSubmit_Success(t *testing.T) {
	var capturedUserID int64
	svc := &mockService{
		submitFunc: func(_ context.Context, uid int64, req dto.CreateHelpMessageRequest) (*dto.HelpMessageResponse, error) {
			capturedUserID = uid
			email := "saif@example.com"
			phone := "9876543210"
			return &dto.HelpMessageResponse{
				ID:        "msg-999",
				UserID:    &uid,
				UserName:  "Saif Ali",
				UserEmail: &email,
				UserPhone: &phone,
				Message:   req.Message,
				Status:    "NEW",
			}, nil
		},
	}
	mux, signer := setupTestServer(t, svc)

	token, _ := signer.Generate("saif@example.com", "42", domain.RoleCustomer)

	// Note: client maliciously passes userId/email/name, which must be ignored
	body := bytes.NewBufferString(`{
		"message": "I am unable to book a marriage hall for my selected date.",
		"user_id": 99999,
		"userName": "Imposter",
		"email": "hacker@evil.com",
		"phone": "0000000000"
	}`)

	req := httptest.NewRequest("POST", "/api/v1/help/messages", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created, got: %d (%s)", rec.Code, rec.Body.String())
	}

	if capturedUserID != 42 {
		t.Errorf("expected authenticated user ID 42, got %d", capturedUserID)
	}

	var env response.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode response envelope: %v", err)
	}

	if !env.Success {
		t.Error("expected success=true")
	}
	expectedMsg := "Your query has been submitted successfully. Our team will review your query and contact you as soon as possible."
	if env.Message != expectedMsg {
		t.Errorf("expected confirmation message %q, got %q", expectedMsg, env.Message)
	}

	dataMap, ok := env.Data.(map[string]any)
	if !ok || dataMap["id"] != "msg-999" {
		t.Errorf("expected data.id = 'msg-999', got %v", env.Data)
	}
}

func TestUserSubmit_Unauthenticated_Returns401(t *testing.T) {
	mux, _ := setupTestServer(t, &mockService{})

	body := bytes.NewBufferString(`{"message": "Help me"}`)
	req := httptest.NewRequest("POST", "/api/v1/help/messages", body)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got: %d", rec.Code)
	}
}

func TestUserSubmit_EmptyMessage_Returns400(t *testing.T) {
	svc := &mockService{
		submitFunc: func(_ context.Context, _ int64, req dto.CreateHelpMessageRequest) (*dto.HelpMessageResponse, error) {
			if strings.TrimSpace(req.Message) == "" {
				return nil, apperr.New(http.StatusBadRequest, "Message is required", "VALIDATION_ERROR")
			}
			return &dto.HelpMessageResponse{ID: "1"}, nil
		},
	}
	mux, signer := setupTestServer(t, svc)
	token, _ := signer.Generate("u@example.com", "10", domain.RoleCustomer)

	body := bytes.NewBufferString(`{"message": "   "}`)
	req := httptest.NewRequest("POST", "/api/v1/help/messages", body)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got: %d", rec.Code)
	}
}

// --------------------------------------------------------------------------
// SUPER ADMIN Authorization Tests
// --------------------------------------------------------------------------

func TestSuperAdmin_UnauthorizedRoles_Denied(t *testing.T) {
	mux, signer := setupTestServer(t, &mockService{})

	// 1. Regular Customer -> 403 Forbidden
	custToken, _ := signer.Generate("cust@example.com", "1", domain.RoleCustomer)
	req := httptest.NewRequest("GET", "/api/v1/super-admin/help/messages", nil)
	req.Header.Set("Authorization", "Bearer "+custToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for ROLE_CUSTOMER, got %d", rec.Code)
	}

	// 2. Hall Owner -> 403 Forbidden
	ownerToken, _ := signer.Generate("owner@example.com", "2", domain.RoleHallOwner)
	req = httptest.NewRequest("GET", "/api/v1/super-admin/help/messages", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for ROLE_HALL_OWNER, got %d", rec.Code)
	}

	// 3. Plain Admin (without ROLE_SUPER_ADMIN) -> 403 Forbidden
	adminToken, _ := signer.Generate("admin@example.com", "3", domain.RoleAdmin)
	req = httptest.NewRequest("GET", "/api/v1/super-admin/help/messages", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for plain ROLE_ADMIN without ROLE_SUPER_ADMIN, got %d", rec.Code)
	}

	// 4. Single message GET as plain Admin -> 403 Forbidden
	req = httptest.NewRequest("GET", "/api/v1/super-admin/help/messages/550e8400-e29b-41d4-a716-446655440000", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for plain Admin viewing single message, got %d", rec.Code)
	}

	// 5. Delete as plain Admin -> 403 Forbidden
	req = httptest.NewRequest("DELETE", "/api/v1/super-admin/help/messages/550e8400-e29b-41d4-a716-446655440000", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for plain Admin deleting message, got %d", rec.Code)
	}

	// 6. Unauthenticated -> 401 Unauthorized
	req = httptest.NewRequest("GET", "/api/v1/super-admin/help/messages", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}
}

// --------------------------------------------------------------------------
// SUPER ADMIN Management Tests
// --------------------------------------------------------------------------

func TestSuperAdmin_ListMessages_SearchFilterPagination(t *testing.T) {
	var capturedFilter dto.HelpMessageFilter
	svc := &mockService{
		listFunc: func(_ context.Context, f dto.HelpMessageFilter) (*httpx.Paged, error) {
			capturedFilter = f
			email := "saif@example.com"
			items := []dto.HelpMessageResponse{
				{
					ID:        "msg-1",
					UserID:    f.UserID,
					UserName:  "Saif Ali",
					UserEmail: &email,
					Message:   "Testing search",
					Status:    "NEW",
				},
			}
			p := httpx.NewPaged(items, f.Page, f.Limit, 1)
			return &p, nil
		},
	}

	mux, signer := setupTestServer(t, svc)
	superAdminToken, _ := signer.Generate("super@example.com", "1", domain.RoleSuperAdmin)

	targetURL := "/api/v1/super-admin/help/messages?search=saif&status=NEW&user_id=456&from_date=2026-10-01&to_date=2026-10-02&page=1&limit=20&sort=created_at&order=desc"
	req := httptest.NewRequest("GET", targetURL, nil)
	req.Header.Set("Authorization", "Bearer "+superAdminToken)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d (%s)", rec.Code, rec.Body.String())
	}

	if capturedFilter.Search == nil || *capturedFilter.Search != "saif" {
		t.Errorf("expected search 'saif', got %v", capturedFilter.Search)
	}
	if capturedFilter.Status == nil || *capturedFilter.Status != "NEW" {
		t.Errorf("expected status 'NEW', got %v", capturedFilter.Status)
	}
	if capturedFilter.UserID == nil || *capturedFilter.UserID != 456 {
		t.Errorf("expected user_id 456, got %v", capturedFilter.UserID)
	}
	if capturedFilter.FromDate == nil {
		t.Error("expected FromDate to be parsed")
	}
	if capturedFilter.ToDate == nil {
		t.Error("expected ToDate to be parsed")
	}
	if capturedFilter.Page != 1 {
		t.Errorf("expected page 1, got %d", capturedFilter.Page)
	}
	if capturedFilter.Limit != 20 {
		t.Errorf("expected limit 20, got %d", capturedFilter.Limit)
	}
	if capturedFilter.SortBy != "created_at" {
		t.Errorf("expected sort 'created_at', got %s", capturedFilter.SortBy)
	}
	if capturedFilter.SortOrder != "desc" {
		t.Errorf("expected order 'desc', got %s", capturedFilter.SortOrder)
	}
}

func TestSuperAdmin_GetSingleMessage(t *testing.T) {
	msgID := "550e8400-e29b-41d4-a716-446655440000"
	svc := &mockService{
		getFunc: func(_ context.Context, id string) (*dto.HelpMessageResponse, error) {
			uid := int64(100)
			email := "saif@example.com"
			phone := "9876543210"
			return &dto.HelpMessageResponse{
				ID:        id,
				UserID:    &uid,
				UserName:  "Saif Ali",
				UserEmail: &email,
				UserPhone: &phone,
				Message:   "Query details",
				Status:    "READ",
			}, nil
		},
	}
	mux, signer := setupTestServer(t, svc)
	token, _ := signer.Generate("super@example.com", "1", domain.RoleSuperAdmin)

	req := httptest.NewRequest("GET", "/api/v1/super-admin/help/messages/"+msgID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", rec.Code)
	}

	var env response.Envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	dataMap := env.Data.(map[string]any)
	if dataMap["id"] != msgID || dataMap["userName"] != "Saif Ali" || dataMap["status"] != "READ" {
		t.Errorf("unexpected message data: %v", dataMap)
	}
}

func TestSuperAdmin_DeleteMessage(t *testing.T) {
	msgID := "550e8400-e29b-41d4-a716-446655440000"
	var deletedID string
	svc := &mockService{
		deleteFunc: func(_ context.Context, id string) error {
			deletedID = id
			return nil
		},
	}
	mux, signer := setupTestServer(t, svc)
	token, _ := signer.Generate("super@example.com", "1", domain.RoleSuperAdmin)

	req := httptest.NewRequest("DELETE", "/api/v1/super-admin/help/messages/"+msgID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", rec.Code)
	}
	if deletedID != msgID {
		t.Errorf("expected deleted id %s, got %s", msgID, deletedID)
	}

	var env response.Envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if !env.Success {
		t.Error("expected success=true")
	}
}
