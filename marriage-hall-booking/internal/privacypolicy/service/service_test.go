package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
)

type mockRepo struct {
	policy *repository.PrivacyPolicy
	err    error
}

func (m *mockRepo) Create(ctx context.Context, p *repository.PrivacyPolicy) error {
	if m.err != nil {
		return m.err
	}
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
	if m.err != nil {
		return nil, m.err
	}
	if m.policy == nil {
		return nil, repository.ErrNotFound
	}
	return m.policy, nil
}

func (m *mockRepo) Update(ctx context.Context, p *repository.PrivacyPolicy) error {
	if m.err != nil {
		return m.err
	}
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
	if m.err != nil {
		return m.err
	}
	if m.policy == nil {
		return repository.ErrNotFound
	}
	m.policy = nil
	return nil
}

func TestCreate_Success(t *testing.T) {
	svc := service.New(&mockRepo{}, nil)
	html := "<h1>Privacy Policy</h1><p>Welcome.</p>"
	res, err := svc.Create(context.Background(), service.CreateRequest{Title: "Privacy Policy", Content: html})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Title != "Privacy Policy" {
		t.Errorf("expected title 'Privacy Policy', got %q", res.Title)
	}
	if res.Content != html {
		t.Errorf("HTML not preserved: got %q", res.Content)
	}
}

func TestCreate_ValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		req     service.CreateRequest
		errCode string
	}{
		{"empty title", service.CreateRequest{Title: "   ", Content: "<p>ok</p>"}, "INVALID_TITLE"},
		{"empty content", service.CreateRequest{Title: "Title", Content: "   "}, "INVALID_CONTENT"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := service.New(&mockRepo{}, nil)
			_, err := svc.Create(context.Background(), tc.req)
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

func TestCreate_AlreadyExists(t *testing.T) {
	repo := &mockRepo{}
	svc := service.New(repo, nil)
	req := service.CreateRequest{Title: "Privacy Policy", Content: "<p>x</p>"}
	svc.Create(context.Background(), req)
	_, err := svc.Create(context.Background(), req)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	ae, ok := err.(*apperr.Error)
	if !ok || ae.Code != "ALREADY_EXISTS" {
		t.Errorf("expected ALREADY_EXISTS, got %v", err)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := service.New(&mockRepo{}, nil)
	_, err := svc.Get(context.Background())
	if err == nil {
		t.Fatal("expected 404, got nil")
	}
	ae, ok := err.(*apperr.Error)
	if !ok || ae.Status != 404 {
		t.Errorf("expected 404, got %v", err)
	}
}

func TestUpdate_Success(t *testing.T) {
	repo := &mockRepo{}
	svc := service.New(repo, nil)
	svc.Create(context.Background(), service.CreateRequest{Title: "Old", Content: "<p>old</p>"})

	res, err := svc.Update(context.Background(), service.UpdateRequest{Title: "New", Content: "<h1>New</h1>"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Title != "New" || res.Content != "<h1>New</h1>" {
		t.Errorf("update not applied: %+v", res)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	svc := service.New(&mockRepo{}, nil)
	_, err := svc.Update(context.Background(), service.UpdateRequest{Title: "T", Content: "<p>x</p>"})
	if err == nil {
		t.Fatal("expected 404, got nil")
	}
}

func TestDelete_Success(t *testing.T) {
	repo := &mockRepo{}
	svc := service.New(repo, nil)
	svc.Create(context.Background(), service.CreateRequest{Title: "T", Content: "<p>x</p>"})
	if err := svc.Delete(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err := svc.Get(context.Background())
	if err == nil {
		t.Fatal("expected 404 after delete, got nil")
	}
}

func TestDelete_NotFound(t *testing.T) {
	svc := service.New(&mockRepo{}, nil)
	err := svc.Delete(context.Background())
	if err == nil {
		t.Fatal("expected 404, got nil")
	}
}
