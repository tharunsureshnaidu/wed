package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/dto"
	faqhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/handler"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

const testSecret = "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v2W3x4Y5z6A7b8C9d0E1f2"

type mockService struct {
	createFunc     func(ctx context.Context, req dto.CreateFAQRequest) (*dto.FAQResponse, error)
	getByIDFunc    func(ctx context.Context, id string) (*dto.FAQResponse, error)
	listAdminFunc  func(ctx context.Context, page, size int) (*httpx.Paged, error)
	updateFunc     func(ctx context.Context, id string, req dto.UpdateFAQRequest) (*dto.FAQResponse, error)
	deleteFunc     func(ctx context.Context, id string) error
	listPublicFunc func(ctx context.Context) ([]dto.PublicFAQResponse, error)
}

func (m *mockService) Create(ctx context.Context, req dto.CreateFAQRequest) (*dto.FAQResponse, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, req)
	}
	return &dto.FAQResponse{
		ID:           "550e8400-e29b-41d4-a716-446655440001",
		Question:     req.Question,
		Answer:       req.Answer,
		DisplayOrder: 1,
		IsActive:     true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}, nil
}

func (m *mockService) GetByID(ctx context.Context, id string) (*dto.FAQResponse, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return &dto.FAQResponse{
		ID:           id,
		Question:     "Sample Question",
		Answer:       "<p>Sample HTML Answer</p>",
		DisplayOrder: 1,
		IsActive:     true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}, nil
}

func (m *mockService) ListAdmin(ctx context.Context, page, size int) (*httpx.Paged, error) {
	if m.listAdminFunc != nil {
		return m.listAdminFunc(ctx, page, size)
	}
	items := []dto.FAQResponse{
		{
			ID:           "550e8400-e29b-41d4-a716-446655440001",
			Question:     "Q1",
			Answer:       "<p>A1</p>",
			DisplayOrder: 1,
			IsActive:     true,
		},
	}
	p := httpx.NewPaged(items, page, size, 1)
	return &p, nil
}

func (m *mockService) Update(ctx context.Context, id string, req dto.UpdateFAQRequest) (*dto.FAQResponse, error) {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, id, req)
	}
	return &dto.FAQResponse{
		ID:           id,
		Question:     req.Question,
		Answer:       req.Answer,
		DisplayOrder: 1,
		IsActive:     true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}, nil
}

func (m *mockService) Delete(ctx context.Context, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
	}
	return nil
}

func (m *mockService) ListPublic(ctx context.Context) ([]dto.PublicFAQResponse, error) {
	if m.listPublicFunc != nil {
		return m.listPublicFunc(ctx)
	}
	return []dto.PublicFAQResponse{
		{
			ID:           "550e8400-e29b-41d4-a716-446655440001",
			Question:     "Public Q1",
			Answer:       "<p>Public A1</p>",
			DisplayOrder: 1,
		},
	}, nil
}

func setupServer(t *testing.T, svc *mockService) (*httptest.Server, *jwt.Signer) {
	t.Helper()
	signer, err := jwt.NewSigner(testSecret, time.Hour)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	mux := http.NewServeMux()
	faqhandler.NewWithService(svc, signer).Register(mux)
	return httptest.NewServer(mux), signer
}

func mintToken(t *testing.T, signer *jwt.Signer, role string) string {
	t.Helper()
	token, err := signer.Generate("test@example.com", "1", role)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	return token
}

// ---------------------------------------------------------------------------
// Public API Tests (Unauthenticated)
// ---------------------------------------------------------------------------

func TestPublicList_NoTokenRequired(t *testing.T) {
	expectedHTML := "<p>To book a marriage hall, follow these steps:</p><ol><li>Browse available venues.</li><li>Select your preferred hall.</li></ol>"
	svc := &mockService{
		listPublicFunc: func(ctx context.Context) ([]dto.PublicFAQResponse, error) {
			return []dto.PublicFAQResponse{
				{
					ID:           "550e8400-e29b-41d4-a716-446655440001",
					Question:     "How can I book a marriage hall?",
					Answer:       expectedHTML,
					DisplayOrder: 1,
				},
			}, nil
		},
	}
	ts, _ := setupServer(t, svc)
	defer ts.Close()

	// Make request WITHOUT Authorization header
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/public/faqs", nil)
	if err != nil {
		t.Fatalf("failed to construct request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var body struct {
		Success bool                    `json:"success"`
		Message string                  `json:"message"`
		Data    []dto.PublicFAQResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !body.Success {
		t.Errorf("expected success true")
	}
	if len(body.Data) != 1 {
		t.Fatalf("expected 1 FAQ, got %d", len(body.Data))
	}
	if body.Data[0].Answer != expectedHTML {
		t.Errorf("HTML answer not preserved in public response.\nGot:\n%s\nExpected:\n%s", body.Data[0].Answer, expectedHTML)
	}
}

// ---------------------------------------------------------------------------
// Admin Role Enforcement Tests
// ---------------------------------------------------------------------------

func TestAdminRoutes_RoleEnforcement(t *testing.T) {
	svc := &mockService{}
	ts, signer := setupServer(t, svc)
	defer ts.Close()

	validUUID := "550e8400-e29b-41d4-a716-446655440001"
	endpoints := []struct {
		method string
		url    string
		body   string
	}{
		{"POST", ts.URL + "/api/v1/admin/faqs", `{"question":"Q","answer":"<p>A</p>"}`},
		{"GET", ts.URL + "/api/v1/admin/faqs", ""},
		{"GET", ts.URL + "/api/v1/admin/faqs/" + validUUID, ""},
		{"PUT", ts.URL + "/api/v1/admin/faqs/" + validUUID, `{"question":"Q","answer":"<p>A</p>"}`},
		{"DELETE", ts.URL + "/api/v1/admin/faqs/" + validUUID, ""},
	}

	for _, ep := range endpoints {
		t.Run(fmt.Sprintf("%s %s - No Token 401", ep.method, ep.url), func(t *testing.T) {
			var bodyReader *bytes.Buffer
			if ep.body != "" {
				bodyReader = bytes.NewBufferString(ep.body)
			} else {
				bodyReader = bytes.NewBuffer(nil)
			}
			req, _ := http.NewRequest(ep.method, ep.url, bodyReader)
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401 Unauthorized, got %d", resp.StatusCode)
			}
		})

		t.Run(fmt.Sprintf("%s %s - Customer Role 403", ep.method, ep.url), func(t *testing.T) {
			var bodyReader *bytes.Buffer
			if ep.body != "" {
				bodyReader = bytes.NewBufferString(ep.body)
			} else {
				bodyReader = bytes.NewBuffer(nil)
			}
			req, _ := http.NewRequest(ep.method, ep.url, bodyReader)
			req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleCustomer))
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("expected 403 Forbidden, got %d", resp.StatusCode)
			}
		})

		t.Run(fmt.Sprintf("%s %s - Hall Owner Role 403", ep.method, ep.url), func(t *testing.T) {
			var bodyReader *bytes.Buffer
			if ep.body != "" {
				bodyReader = bytes.NewBufferString(ep.body)
			} else {
				bodyReader = bytes.NewBuffer(nil)
			}
			req, _ := http.NewRequest(ep.method, ep.url, bodyReader)
			req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleHallOwner))
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("expected 403 Forbidden, got %d", resp.StatusCode)
			}
		})

		t.Run(fmt.Sprintf("%s %s - Admin Role Allowed", ep.method, ep.url), func(t *testing.T) {
			var bodyReader *bytes.Buffer
			if ep.body != "" {
				bodyReader = bytes.NewBufferString(ep.body)
			} else {
				bodyReader = bytes.NewBuffer(nil)
			}
			req, _ := http.NewRequest(ep.method, ep.url, bodyReader)
			req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleAdmin))
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
				t.Errorf("expected 200 or 201 for ROLE_ADMIN, got %d", resp.StatusCode)
			}
		})

		t.Run(fmt.Sprintf("%s %s - Super Admin Role Allowed", ep.method, ep.url), func(t *testing.T) {
			var bodyReader *bytes.Buffer
			if ep.body != "" {
				bodyReader = bytes.NewBufferString(ep.body)
			} else {
				bodyReader = bytes.NewBuffer(nil)
			}
			req, _ := http.NewRequest(ep.method, ep.url, bodyReader)
			req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleSuperAdmin))
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
				t.Errorf("expected 200 or 201 for ROLE_SUPER_ADMIN, got %d", resp.StatusCode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// CRUD Flow and HTML Preservation Tests
// ---------------------------------------------------------------------------

func TestAdminCreate_Success_PreservesHTML(t *testing.T) {
	htmlAnswer := "<p>To book a marriage hall, follow these steps:</p><ol><li>Browse available venues.</li><li>Select your preferred hall.</li><li>Choose your event date.</li><li>Submit your booking request.</li></ol><p>Our team will help you with the booking process.</p>"

	svc := &mockService{
		createFunc: func(ctx context.Context, req dto.CreateFAQRequest) (*dto.FAQResponse, error) {
			return &dto.FAQResponse{
				ID:           "550e8400-e29b-41d4-a716-446655440001",
				Question:     req.Question,
				Answer:       req.Answer,
				DisplayOrder: 1,
				IsActive:     true,
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
			}, nil
		},
	}
	ts, signer := setupServer(t, svc)
	defer ts.Close()

	payload := map[string]any{
		"question":     "How can I book a marriage hall?",
		"answer":       htmlAnswer,
		"displayOrder": 1,
		"isActive":     true,
	}
	b, _ := json.Marshal(payload)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/admin/faqs", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleAdmin))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if loc != "/api/v1/admin/faqs/550e8400-e29b-41d4-a716-446655440001" {
		t.Errorf("expected Location header /api/v1/admin/faqs/550e8400-e29b-41d4-a716-446655440001, got %q", loc)
	}

	var res struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    dto.FAQResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if res.Data.Answer != htmlAnswer {
		t.Errorf("HTML answer not preserved in response.\nGot:\n%s\nExpected:\n%s", res.Data.Answer, htmlAnswer)
	}
}

func TestAdminUpdate_Success_PreservesHTML(t *testing.T) {
	updatedHTML := "<p>Follow these steps to book your venue:</p><ul><li>Choose a venue.</li><li>Select your event date.</li><li>Submit your booking request.</li></ul><p><strong>Note:</strong> Booking confirmation depends on availability.</p>"

	svc := &mockService{
		updateFunc: func(ctx context.Context, id string, req dto.UpdateFAQRequest) (*dto.FAQResponse, error) {
			return &dto.FAQResponse{
				ID:           id,
				Question:     req.Question,
				Answer:       req.Answer,
				DisplayOrder: 2,
				IsActive:     true,
				CreatedAt:    time.Now().Add(-time.Hour),
				UpdatedAt:    time.Now(),
			}, nil
		},
	}
	ts, signer := setupServer(t, svc)
	defer ts.Close()

	payload := map[string]any{
		"question":     "How can I book a marriage hall?",
		"answer":       updatedHTML,
		"displayOrder": 2,
		"isActive":     true,
	}
	b, _ := json.Marshal(payload)

	faqID := "550e8400-e29b-41d4-a716-446655440001"
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/admin/faqs/"+faqID, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+mintToken(t, signer, domain.RoleAdmin))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var res struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    dto.FAQResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if res.Data.Answer != updatedHTML {
		t.Errorf("updated HTML answer not preserved.\nGot:\n%s\nExpected:\n%s", res.Data.Answer, updatedHTML)
	}
}

// ---------------------------------------------------------------------------
// Error Handling: Invalid ID, Malformed JSON, Validation Failures, Not Found
// ---------------------------------------------------------------------------

func TestInvalidUUID_Returns400(t *testing.T) {
	svc := &mockService{}
	ts, signer := setupServer(t, svc)
	defer ts.Close()

	token := mintToken(t, signer, domain.RoleAdmin)

	testCases := []struct {
		method string
		url    string
	}{
		{"GET", ts.URL + "/api/v1/admin/faqs/not-a-valid-uuid"},
		{"PUT", ts.URL + "/api/v1/admin/faqs/not-a-valid-uuid"},
		{"DELETE", ts.URL + "/api/v1/admin/faqs/not-a-valid-uuid"},
	}

	for _, tc := range testCases {
		t.Run(tc.method+" "+tc.url, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, tc.url, bytes.NewBufferString(`{"question":"Q","answer":"A"}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("expected 400 Bad Request, got %d", resp.StatusCode)
			}

			var body struct {
				Success   bool   `json:"success"`
				ErrorCode string `json:"errorCode"`
			}
			json.NewDecoder(resp.Body).Decode(&body)
			if body.ErrorCode != "INVALID_ID" {
				t.Errorf("expected error code INVALID_ID, got %s", body.ErrorCode)
			}
		})
	}
}

func TestMalformedJSON_Returns400(t *testing.T) {
	svc := &mockService{}
	ts, signer := setupServer(t, svc)
	defer ts.Close()

	token := mintToken(t, signer, domain.RoleAdmin)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/admin/faqs", bytes.NewBufferString("{ invalid json"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", resp.StatusCode)
	}

	var body struct {
		ErrorCode string `json:"errorCode"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.ErrorCode != "MALFORMED_JSON" {
		t.Errorf("expected MALFORMED_JSON, got %s", body.ErrorCode)
	}
}

func TestNotFound_Returns404(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440001"
	svc := &mockService{
		getByIDFunc: func(ctx context.Context, id string) (*dto.FAQResponse, error) {
			return nil, apperr.New(http.StatusNotFound, "NOT_FOUND", "FAQ not found")
		},
		deleteFunc: func(ctx context.Context, id string) error {
			return apperr.New(http.StatusNotFound, "NOT_FOUND", "FAQ not found")
		},
	}
	ts, signer := setupServer(t, svc)
	defer ts.Close()

	token := mintToken(t, signer, domain.RoleAdmin)

	// GET 404
	reqGet, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/admin/faqs/"+validUUID, nil)
	reqGet.Header.Set("Authorization", "Bearer "+token)
	respGet, _ := http.DefaultClient.Do(reqGet)
	if respGet.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 Not Found on GET, got %d", respGet.StatusCode)
	}
	respGet.Body.Close()

	// DELETE 404
	reqDel, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/admin/faqs/"+validUUID, nil)
	reqDel.Header.Set("Authorization", "Bearer "+token)
	respDel, _ := http.DefaultClient.Do(reqDel)
	if respDel.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 Not Found on DELETE, got %d", respDel.StatusCode)
	}
	respDel.Body.Close()
}
