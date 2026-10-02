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
	policy *repository.PrivacyPolicy
}

func (m *mockRepo) Create(ctx context.Context, p *repository.PrivacyPolicy) error {
	if m.policy != nil {
		return repository.ErrAlreadyExists
	}
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	m.policy = p
	return nil
}

func (m *mockRepo) Get(ctx context.Context) (*repository.PrivacyPolicy, error) {
	if m.policy == nil {
		return nil, repository.ErrNotFound
	}
	return m.policy, nil
}

func (m *mockRepo) Update(ctx context.Context, p *repository.PrivacyPolicy) error {
	if m.policy == nil {
		return repository.ErrNotFound
	}
	m.policy.Title = p.Title
	m.policy.Content = p.Content
	m.policy.UpdatedAt = time.Now()
	*p = *m.policy
	return nil
}

func (m *mockRepo) Delete(ctx context.Context) error {
	if m.policy == nil {
		return repository.ErrNotFound
	}
	m.policy = nil
	return nil
}

func setup(t *testing.T) (*httptest.Server, *jwt.Signer, *mockRepo) {
	signer, err := jwt.NewSigner("test-secret-32-bytes-long-key-for-jwt-signing!!", time.Hour)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	repo := &mockRepo{}
	svc := service.New(repo, nil)
	mux := http.NewServeMux()
	privacypolicyhandler.New(svc, signer).Register(mux)
	return httptest.NewServer(mux), signer, repo
}

func adminToken(t *testing.T, signer *jwt.Signer) string {
	tok, err := signer.Generate("admin@example.com", "10", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok
}

// --- Public API tests ---

func TestPublicGet_NoToken_Success(t *testing.T) {
	ts, _, repo := setup(t)
	defer ts.Close()

	html := "<h1>Privacy Policy</h1><p>Content</p>"
	repo.policy = &repository.PrivacyPolicy{Title: "Privacy Policy", Content: html, CreatedAt: time.Now(), UpdatedAt: time.Now()}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/privacy-policy", nil)
	// No Authorization header intentionally
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Data.Content != html {
		t.Errorf("HTML not preserved: got %q", body.Data.Content)
	}
}

func TestPublicGet_NotFound(t *testing.T) {
	ts, _, _ := setup(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/privacy-policy", nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestPublicGet_TokenPresent_StillWorks(t *testing.T) {
	ts, signer, repo := setup(t)
	defer ts.Close()

	repo.policy = &repository.PrivacyPolicy{Title: "T", Content: "<p>x</p>", CreatedAt: time.Now(), UpdatedAt: time.Now()}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/privacy-policy", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken(t, signer))
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 even with token, got %d", resp.StatusCode)
	}
}

// --- Admin CRUD tests ---

func TestAdminCreate_Success(t *testing.T) {
	ts, signer, _ := setup(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{
		"title":   "Privacy Policy",
		"content": "<h1>Privacy Policy</h1><p>Content</p>",
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/admin/privacy-policy", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken(t, signer))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestAdminCreate_Unauthenticated(t *testing.T) {
	ts, _, _ := setup(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"title": "T", "content": "<p>x</p>"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/admin/privacy-policy", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAdminCreate_CustomerForbidden(t *testing.T) {
	ts, signer, _ := setup(t)
	defer ts.Close()

	tok, _ := signer.Generate("u@example.com", "5", domain.RoleCustomer)
	body, _ := json.Marshal(map[string]string{"title": "T", "content": "<p>x</p>"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/admin/privacy-policy", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestAdminUpdate_NoID_Success(t *testing.T) {
	ts, signer, repo := setup(t)
	defer ts.Close()

	// Seed a policy first
	repo.policy = &repository.PrivacyPolicy{Title: "Old", Content: "<p>old</p>", CreatedAt: time.Now(), UpdatedAt: time.Now()}

	body, _ := json.Marshal(map[string]string{
		"title":   "Updated Policy",
		"content": "<h1>Updated</h1><p>New content</p>",
	})
	// Note: PUT /api/admin/privacy-policy — NO {id} in URL
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/admin/privacy-policy", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken(t, signer))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Data.Title != "Updated Policy" {
		t.Errorf("title not updated: got %q", result.Data.Title)
	}
}

func TestAdminDelete_NoID_Success(t *testing.T) {
	ts, signer, repo := setup(t)
	defer ts.Close()

	repo.policy = &repository.PrivacyPolicy{Title: "T", Content: "<p>x</p>", CreatedAt: time.Now(), UpdatedAt: time.Now()}

	// Note: DELETE /api/admin/privacy-policy — NO {id} in URL
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/admin/privacy-policy", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken(t, signer))
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestPayloadSizeLimit(t *testing.T) {
	ts, signer, _ := setup(t)
	defer ts.Close()

	t.Setenv("PRIVACY_POLICY_MAX_BYTES", "1024")
	repo2 := &mockRepo{}
	svc2 := service.New(repo2, nil)
	mux2 := http.NewServeMux()
	privacypolicyhandler.New(svc2, signer).Register(mux2)
	ts2 := httptest.NewServer(mux2)
	defer ts2.Close()

	hugeHTML := strings.Repeat("A", 2*1024*1024)
	body, _ := json.Marshal(map[string]string{"title": "T", "content": hugeHTML})
	req, _ := http.NewRequest(http.MethodPost, ts2.URL+"/api/admin/privacy-policy", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken(t, signer))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}
