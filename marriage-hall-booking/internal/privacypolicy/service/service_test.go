package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
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

func TestCreate_Success_PreservesHTML(t *testing.T) {
	repo := newMockRepo()
	svc := service.New(repo, nil)
	html := "<h1>Privacy Policy</h1><p>Welcome to our application.</p><ul><li>Name</li><li>Email</li></ul>"

	res, err := svc.Create(context.Background(), service.CreateRequest{
		Title:   "Privacy Policy",
		Content: html,
		Version: "1.0",
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ID == 0 {
		t.Errorf("expected non-zero ID")
	}
	if res.Title != "Privacy Policy" {
		t.Errorf("expected title 'Privacy Policy', got %q", res.Title)
	}
	if res.Content != html {
		t.Errorf("HTML content not preserved: got %q, expected %q", res.Content, html)
	}
	if res.Version != "1.0" {
		t.Errorf("expected version 1.0, got %q", res.Version)
	}
	if res.Status != "INACTIVE" {
		t.Errorf("expected new policy to be INACTIVE, got %q", res.Status)
	}
}

func TestCreate_DuplicateVersion(t *testing.T) {
	repo := newMockRepo()
	svc := service.New(repo, nil)

	_, err := svc.Create(context.Background(), service.CreateRequest{
		Title:   "Policy V1",
		Content: "<p>Content</p>",
		Version: "1.0",
	}, nil)
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	_, err = svc.Create(context.Background(), service.CreateRequest{
		Title:   "Policy V1 Dup",
		Content: "<p>Other</p>",
		Version: "1.0",
	}, nil)
	if err == nil {
		t.Fatal("expected conflict error on duplicate version, got nil")
	}
	ae, ok := err.(*apperr.Error)
	if !ok || ae.Code != "VERSION_EXISTS" {
		t.Errorf("expected error code VERSION_EXISTS, got %v", err)
	}
}

func TestCreate_ValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		req     service.CreateRequest
		errCode string
	}{
		{"empty title", service.CreateRequest{Title: "   ", Content: "<p>ok</p>", Version: "1.0"}, "INVALID_TITLE"},
		{"empty content", service.CreateRequest{Title: "Title", Content: "   ", Version: "1.0"}, "INVALID_CONTENT"},
		{"empty version", service.CreateRequest{Title: "Title", Content: "<p>ok</p>", Version: "   "}, "INVALID_VERSION"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := service.New(newMockRepo(), nil)
			_, err := svc.Create(context.Background(), tc.req, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			ae, ok := err.(*apperr.Error)
			if !ok || ae.Code != tc.errCode {
				t.Errorf("expected code %s, got %v", tc.errCode, err)
			}
		})
	}
}

func TestActivate_EnsuresSingleActive(t *testing.T) {
	repo := newMockRepo()
	svc := service.New(repo, nil)

	p1, _ := svc.Create(context.Background(), service.CreateRequest{Title: "P1", Content: "<p>1</p>", Version: "1.0"}, nil)
	p2, _ := svc.Create(context.Background(), service.CreateRequest{Title: "P2", Content: "<p>2</p>", Version: "2.0"}, nil)

	// Activate p1
	act1, err := svc.Activate(context.Background(), p1.ID, nil)
	if err != nil {
		t.Fatalf("failed to activate p1: %v", err)
	}
	if act1.Status != "ACTIVE" {
		t.Errorf("expected p1 ACTIVE, got %s", act1.Status)
	}

	active, err := svc.GetActive(context.Background())
	if err != nil || active.ID != p1.ID {
		t.Fatalf("expected active policy to be p1, got %v, err=%v", active, err)
	}

	// Now activate p2
	act2, err := svc.Activate(context.Background(), p2.ID, nil)
	if err != nil {
		t.Fatalf("failed to activate p2: %v", err)
	}
	if act2.Status != "ACTIVE" {
		t.Errorf("expected p2 ACTIVE, got %s", act2.Status)
	}

	// p1 should now be INACTIVE
	p1Check, _ := svc.GetByID(context.Background(), p1.ID)
	if p1Check.Status != "INACTIVE" {
		t.Errorf("expected p1 to become INACTIVE after p2 activation, got %s", p1Check.Status)
	}

	active, err = svc.GetActive(context.Background())
	if err != nil || active.ID != p2.ID {
		t.Fatalf("expected active policy to be p2, got %v, err=%v", active, err)
	}
}
