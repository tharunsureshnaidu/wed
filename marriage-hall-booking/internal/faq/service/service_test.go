package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
)

type mockRepo struct {
	items     map[string]*domain.FAQ
	createErr error
	getErr    error
	listErr   error
	updateErr error
	deleteErr error
}

func newMockRepo() *mockRepo {
	return &mockRepo{items: make(map[string]*domain.FAQ)}
}

func (m *mockRepo) Create(ctx context.Context, f *domain.FAQ) error {
	if m.createErr != nil {
		return m.createErr
	}
	f.ID = fmt.Sprintf("550e8400-e29b-41d4-a716-4466554400%02d", len(m.items)+1)
	now := time.Now()
	f.CreatedAt = now
	f.UpdatedAt = now
	copyItem := *f
	m.items[f.ID] = &copyItem
	return nil
}

func (m *mockRepo) GetByID(ctx context.Context, id string) (*domain.FAQ, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	item, ok := m.items[id]
	if !ok || item.IsDeleted {
		return nil, repository.ErrNotFound
	}
	res := *item
	return &res, nil
}

func (m *mockRepo) ListAdmin(ctx context.Context, page, size int) ([]domain.FAQ, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	var out []domain.FAQ
	for _, item := range m.items {
		if !item.IsDeleted {
			out = append(out, *item)
		}
	}
	return out, int64(len(out)), nil
}

func (m *mockRepo) Update(ctx context.Context, f *domain.FAQ) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	item, ok := m.items[f.ID]
	if !ok || item.IsDeleted {
		return repository.ErrNotFound
	}
	f.UpdatedAt = time.Now()
	copyItem := *f
	m.items[f.ID] = &copyItem
	return nil
}

func (m *mockRepo) Delete(ctx context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	item, ok := m.items[id]
	if !ok || item.IsDeleted {
		return repository.ErrNotFound
	}
	item.IsDeleted = true
	now := time.Now()
	item.DeletedAt = &now
	item.UpdatedAt = now
	return nil
}

func (m *mockRepo) ListActivePublic(ctx context.Context) ([]domain.FAQ, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var out []domain.FAQ
	for _, item := range m.items {
		if !item.IsDeleted && item.IsActive {
			out = append(out, *item)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCreate_SuccessPreservesHTML(t *testing.T) {
	repo := newMockRepo()
	svc := service.New(repo)

	htmlAnswer := "<p>To book a marriage hall, follow these steps:</p><ol><li>Browse available venues.</li><li>Select your preferred hall.</li><li>Choose your event date.</li><li>Submit your booking request.</li></ol><p>Our team will help you with the booking process.</p>"
	order := 2
	active := true

	req := dto.CreateFAQRequest{
		Question:     "How can I book a marriage hall?",
		Answer:       htmlAnswer,
		DisplayOrder: &order,
		IsActive:     &active,
	}

	res, err := svc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.ID == "" {
		t.Errorf("expected non-empty ID")
	}
	if res.Question != req.Question {
		t.Errorf("expected question %q, got %q", req.Question, res.Question)
	}
	if res.Answer != htmlAnswer {
		t.Errorf("expected HTML answer to be preserved exactly.\nGot:\n%s\nExpected:\n%s", res.Answer, htmlAnswer)
	}
	if res.DisplayOrder != 2 {
		t.Errorf("expected displayOrder 2, got %d", res.DisplayOrder)
	}
	if !res.IsActive {
		t.Errorf("expected isActive true")
	}
}

func TestCreate_Defaults(t *testing.T) {
	repo := newMockRepo()
	svc := service.New(repo)

	req := dto.CreateFAQRequest{
		Question: "Basic Question",
		Answer:   "<p>Basic Answer</p>",
	}

	res, err := svc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.DisplayOrder != 0 {
		t.Errorf("expected default displayOrder 0, got %d", res.DisplayOrder)
	}
	if !res.IsActive {
		t.Errorf("expected default isActive true, got false")
	}
}

func TestCreate_ValidationErrors(t *testing.T) {
	repo := newMockRepo()
	svc := service.New(repo)

	tests := []struct {
		name    string
		req     dto.CreateFAQRequest
		errCode string
	}{
		{
			name:    "empty question",
			req:     dto.CreateFAQRequest{Question: "   ", Answer: "<p>Answer</p>"},
			errCode: "VALIDATION_ERROR",
		},
		{
			name:    "question too long",
			req:     dto.CreateFAQRequest{Question: strings.Repeat("a", 501), Answer: "<p>Answer</p>"},
			errCode: "VALIDATION_ERROR",
		},
		{
			name:    "empty answer",
			req:     dto.CreateFAQRequest{Question: "Question?", Answer: "   "},
			errCode: "VALIDATION_ERROR",
		},
		{
			name:    "answer contains only stripped script",
			req:     dto.CreateFAQRequest{Question: "Question?", Answer: "<script>alert(1)</script>"},
			errCode: "VALIDATION_ERROR",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			ae, ok := err.(*apperr.Error)
			if !ok {
				t.Fatalf("expected *apperr.Error, got %T", err)
			}
			if ae.Code != tc.errCode {
				t.Errorf("expected code %s, got %s", tc.errCode, ae.Code)
			}
		})
	}
}

func TestGetByID_SuccessAndNotFound(t *testing.T) {
	repo := newMockRepo()
	svc := service.New(repo)

	created, _ := svc.Create(context.Background(), dto.CreateFAQRequest{
		Question: "Q1",
		Answer:   "<p>A1</p>",
	})

	// Found
	res, err := svc.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ID != created.ID {
		t.Errorf("expected id %s, got %s", created.ID, res.ID)
	}

	// Not Found
	_, err = svc.GetByID(context.Background(), "non-existent-uuid")
	if err == nil {
		t.Fatal("expected error for non-existent id, got nil")
	}
	ae, ok := err.(*apperr.Error)
	if !ok || ae.Status != 404 {
		t.Errorf("expected 404 NOT_FOUND, got %v", err)
	}
}

func TestUpdate_SuccessAndNotFound(t *testing.T) {
	repo := newMockRepo()
	svc := service.New(repo)

	created, _ := svc.Create(context.Background(), dto.CreateFAQRequest{
		Question: "Original Q",
		Answer:   "<p>Original A</p>",
	})

	updatedHTML := "<p>Follow these steps to book your venue:</p><ul><li>Choose a venue.</li><li>Select your event date.</li><li>Submit your booking request.</li></ul><p><strong>Note:</strong> Booking confirmation depends on availability.</p>"
	newOrder := 5
	newActive := false

	res, err := svc.Update(context.Background(), created.ID, dto.UpdateFAQRequest{
		Question:     "Updated Q",
		Answer:       updatedHTML,
		DisplayOrder: &newOrder,
		IsActive:     &newActive,
	})
	if err != nil {
		t.Fatalf("unexpected error on update: %v", err)
	}

	if res.Question != "Updated Q" {
		t.Errorf("expected updated question, got %s", res.Question)
	}
	if res.Answer != updatedHTML {
		t.Errorf("expected updated HTML to be preserved, got %s", res.Answer)
	}
	if res.DisplayOrder != 5 {
		t.Errorf("expected displayOrder 5, got %d", res.DisplayOrder)
	}
	if res.IsActive {
		t.Errorf("expected isActive to be false")
	}

	// Not found update
	_, err = svc.Update(context.Background(), "unknown-id", dto.UpdateFAQRequest{
		Question: "Valid Q",
		Answer:   "<p>Valid A</p>",
	})
	if err == nil {
		t.Fatal("expected 404 on updating unknown id, got nil")
	}
}

func TestDelete_SuccessAndNotFound(t *testing.T) {
	repo := newMockRepo()
	svc := service.New(repo)

	created, _ := svc.Create(context.Background(), dto.CreateFAQRequest{
		Question: "To Delete",
		Answer:   "<p>Delete me</p>",
	})

	if err := svc.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("unexpected error deleting faq: %v", err)
	}

	// Double delete or get should return 404
	_, err := svc.GetByID(context.Background(), created.ID)
	if err == nil {
		t.Fatal("expected 404 after deletion, got nil")
	}

	err = svc.Delete(context.Background(), created.ID)
	if err == nil {
		t.Fatal("expected 404 deleting already-deleted faq, got nil")
	}
}

func TestListPublic_OnlyActiveAndNoAdminMetadata(t *testing.T) {
	repo := newMockRepo()
	svc := service.New(repo)

	active := true
	inactive := false

	svc.Create(context.Background(), dto.CreateFAQRequest{
		Question: "Active Q",
		Answer:   "<p>Active Answer</p>",
		IsActive: &active,
	})
	svc.Create(context.Background(), dto.CreateFAQRequest{
		Question: "Inactive Q",
		Answer:   "<p>Inactive Answer</p>",
		IsActive: &inactive,
	})

	res, err := svc.ListPublic(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res) != 1 {
		t.Fatalf("expected 1 active FAQ, got %d", len(res))
	}
	if res[0].Question != "Active Q" {
		t.Errorf("expected 'Active Q', got %s", res[0].Question)
	}
	if res[0].Answer != "<p>Active Answer</p>" {
		t.Errorf("expected HTML preserved, got %s", res[0].Answer)
	}
}
