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
	privacypolicyhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/handler"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

type mockRepo struct {
	policies []repository.PrivacyPolicy
	nextID   int64
	err      error
}

func newMockRepo() *mockRepo {
	return &mockRepo{nextID: 1}
}

func (m *mockRepo) VersionExists(ctx context.Context, version string, excludeID int64) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	for _, p := range m.policies {
		if strings.EqualFold(p.Version, strings.TrimSpace(version)) && p.ID != excludeID {
			return true, nil
		}
	}
	return false, nil
}

func (m *mockRepo) Create(ctx context.Context, p *repository.PrivacyPolicy) error {
	if m.err != nil {
		return m.err
	}
	p.ID = m.nextID
	m.nextID++
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	m.policies = append(m.policies, *p)
	return nil
}

func (m *mockRepo) GetActive(ctx context.Context) (*repository.PrivacyPolicy, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, p := range m.policies {
		if p.Status == "ACTIVE" {
			res := p
			return &res, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockRepo) GetByID(ctx context.Context, id int64) (*repository.PrivacyPolicy, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, p := range m.policies {
		if p.ID == id {
			res := p
			return &res, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockRepo) GetAll(ctx context.Context) ([]repository.PrivacyPolicy, error) {
	if m.err != nil {
		return nil, m.err
	}
	res := make([]repository.PrivacyPolicy, len(m.policies))
	copy(res, m.policies)
	return res, nil
}

func (m *mockRepo) Update(ctx context.Context, id int64, title, content, version string, updatedBy *int64) (*repository.PrivacyPolicy, error) {
	if m.err != nil {
		return nil, m.err
	}
	for i, p := range m.policies {
		if p.ID == id {
			m.policies[i].Title = title
			m.policies[i].Content = content
			m.policies[i].Version = version
			m.policies[i].UpdatedBy = updatedBy
			m.policies[i].UpdatedAt = time.Now()
			res := m.policies[i]
			return &res, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockRepo) Activate(ctx context.Context, id int64, updatedBy *int64, effectiveFrom time.Time) (*repository.PrivacyPolicy, error) {
	if m.err != nil {
		return nil, m.err
	}
	found := false
	for i, p := range m.policies {
		if p.ID == id {
			found = true
			m.policies[i].Status = "ACTIVE"
			m.policies[i].EffectiveFrom = &effectiveFrom
			m.policies[i].UpdatedBy = updatedBy
			m.policies[i].UpdatedAt = time.Now()
		} else if p.Status == "ACTIVE" {
			m.policies[i].Status = "INACTIVE"
			m.policies[i].UpdatedAt = time.Now()
		}
	}
	if !found {
		return nil, repository.ErrNotFound
	}
	return m.GetByID(ctx, id)
}

func (m *mockRepo) Deactivate(ctx context.Context, id int64, updatedBy *int64) (*repository.PrivacyPolicy, error) {
	if m.err != nil {
		return nil, m.err
	}
	for i, p := range m.policies {
		if p.ID == id {
			m.policies[i].Status = "INACTIVE"
			m.policies[i].UpdatedBy = updatedBy
			m.policies[i].UpdatedAt = time.Now()
			res := m.policies[i]
			return &res, nil
		}
	}
	return nil, repository.ErrNotFound
}

func setupTest(t *testing.T) (*http.ServeMux, *jwt.Signer, *mockRepo) {
	signer, err := jwt.NewSigner("test-secret-32-bytes-long-key-for-jwt-signing!!", time.Hour)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	repo := newMockRepo()
	svc := service.New(repo, nil)
	mux := http.NewServeMux()
	privacypolicyhandler.New(svc, signer).Register(mux)
	return mux, signer, repo
}

func adminToken(t *testing.T, signer *jwt.Signer) string {
	tok, err := signer.Generate("admin@example.com", "10", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok
}

func customerToken(t *testing.T, signer *jwt.Signer) string {
	tok, err := signer.Generate("user@example.com", "20", domain.RoleCustomer)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok
}

// --- Public API Tests ---

func TestPublicGet_NoToken_Success(t *testing.T) {
	mux, _, repo := setupTest(t)

	html := "<h1>Privacy Policy</h1><p>Welcome to our application.</p>"
	now := time.Now()
	repo.policies = append(repo.policies, repository.PrivacyPolicy{
		ID:            1,
		Title:         "Privacy Policy",
		Content:       html,
		Version:       "1.0",
		Status:        "ACTIVE",
		EffectiveFrom: &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/privacy-policy", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success bool                     `json:"success"`
		Message string                   `json:"message"`
		Data    repository.PrivacyPolicy `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json parse: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success true")
	}
	if resp.Data.Content != html {
		t.Errorf("HTML content altered: got %q, expected %q", resp.Data.Content, html)
	}
	if resp.Data.Status != "ACTIVE" {
		t.Errorf("expected status ACTIVE, got %q", resp.Data.Status)
	}
}

func TestPublicGet_NotFound(t *testing.T) {
	mux, _, _ := setupTest(t)

	req := httptest.NewRequest(http.MethodGet, "/api/privacy-policy", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// --- Admin Authorization Tests ---

func TestAdminCreate_NonAdminForbidden(t *testing.T) {
	mux, signer, _ := setupTest(t)

	body := `{"title":"Policy","content":"<p>text</p>","version":"1.0"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/privacy-policy", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+customerToken(t, signer))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminCreate_NoTokenUnauthorized(t *testing.T) {
	mux, _, _ := setupTest(t)

	body := `{"title":"Policy","content":"<p>text</p>","version":"1.0"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/privacy-policy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Admin CRUD & HTML Preservation Tests ---

func TestAdminFlow_Create_Get_Update_Activate(t *testing.T) {
	mux, signer, _ := setupTest(t)
	token := adminToken(t, signer)

	htmlOriginal := "<h1>Privacy Policy</h1><p>Our app values privacy.</p><ul><li>Data</li></ul>"
	createReq := map[string]string{
		"title":   "Privacy Policy",
		"content": htmlOriginal,
		"version": "1.0",
	}
	body, _ := json.Marshal(createReq)

	// 1. Create
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/privacy-policy", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create failed, status %d: %s", rec.Code, rec.Body.String())
	}

	var createResp struct {
		Data repository.PrivacyPolicy `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &createResp)
	createdID := createResp.Data.ID

	if createResp.Data.Content != htmlOriginal {
		t.Errorf("expected HTML preserved on create, got %q", createResp.Data.Content)
	}

	// 2. Get by ID
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/privacy-policy/1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get by id failed: %d", rec.Code)
	}

	// 3. Update
	htmlUpdated := "<h1>Updated Privacy Policy</h1><p>Updated content.</p>"
	updateReq := map[string]string{
		"title":   "Updated Policy",
		"content": htmlUpdated,
		"version": "1.1",
	}
	body, _ = json.Marshal(updateReq)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/admin/privacy-policy/1", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d, %s", rec.Code, rec.Body.String())
	}

	// 4. Activate
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/privacy-policy/1/activate", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("activate failed: %d, %s", rec.Code, rec.Body.String())
	}

	// 5. Verify public endpoint now returns the activated updated policy
	req = httptest.NewRequest(http.MethodGet, "/api/v1/privacy-policy", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("public get failed: %d, %s", rec.Code, rec.Body.String())
	}

	var pubResp struct {
		Data repository.PrivacyPolicy `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &pubResp)

	if pubResp.Data.ID != createdID {
		t.Errorf("expected ID %d, got %d", createdID, pubResp.Data.ID)
	}
	if pubResp.Data.Content != htmlUpdated {
		t.Errorf("expected updated HTML, got %q", pubResp.Data.Content)
	}
	if pubResp.Data.Status != "ACTIVE" {
		t.Errorf("expected ACTIVE status, got %s", pubResp.Data.Status)
	}
}
