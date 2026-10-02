package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	feedbackdomain "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/dto"
	feedbackrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/repository"
	feedbackservice "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/service"
)

// --------------------------------------------------------------------------
// Mock repository
// --------------------------------------------------------------------------

type mockRepo struct {
	created     *feedbackdomain.Feedback
	byID        *feedbackdomain.FeedbackWithUser
	byUser      *feedbackdomain.Feedback
	listed      []feedbackdomain.FeedbackWithUser
	listedTotal int64
	updated     *feedbackdomain.FeedbackWithUser
	summary     *feedbackdomain.FeedbackSummary
	err         error
}

func (m *mockRepo) Create(_ context.Context, f *feedbackdomain.Feedback) (*feedbackdomain.Feedback, error) {
	if m.err != nil {
		return nil, m.err
	}
	result := *f
	result.ID = "test-uuid"
	result.CreatedAt = time.Now()
	result.UpdatedAt = time.Now()
	result.Status = feedbackdomain.StatusNew
	return &result, nil
}

func (m *mockRepo) GetByID(_ context.Context, _ string) (*feedbackdomain.FeedbackWithUser, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.byID == nil {
		return nil, feedbackrepo.ErrNotFound
	}
	return m.byID, nil
}

func (m *mockRepo) GetByUserID(_ context.Context, _ int64) (*feedbackdomain.Feedback, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.byUser == nil {
		return nil, feedbackrepo.ErrNotFound
	}
	return m.byUser, nil
}

func (m *mockRepo) List(_ context.Context, _ dto.FeedbackFilter) ([]feedbackdomain.FeedbackWithUser, int64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	return m.listed, m.listedTotal, nil
}

func (m *mockRepo) ListByUserID(_ context.Context, _ int64, _, _ int) ([]feedbackdomain.FeedbackWithUser, int64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	return m.listed, m.listedTotal, nil
}

func (m *mockRepo) Update(_ context.Context, _ string, status, note *string, _ int64) (*feedbackdomain.FeedbackWithUser, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.updated == nil {
		return nil, feedbackrepo.ErrNotFound
	}
	return m.updated, nil
}

func (m *mockRepo) GetSummary(_ context.Context) (*feedbackdomain.FeedbackSummary, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.summary, nil
}

// --------------------------------------------------------------------------
// Helper builders
// --------------------------------------------------------------------------

func newService(repo feedbackrepo.Repository) feedbackservice.Service {
	return feedbackservice.New(repo)
}

func ptrInt(v int) *int       { return &v }
func ptrStr(v string) *string { return &v }
func userID() int64           { return 42 }

// --------------------------------------------------------------------------
// Submit tests
// --------------------------------------------------------------------------

func TestSubmit_Success_RatingAndFeedback(t *testing.T) {
	svc := newService(&mockRepo{})
	req := dto.CreateFeedbackRequest{
		Rating:   ptrInt(5),
		Feedback: ptrStr("Great app!"),
	}
	res, err := svc.Submit(context.Background(), userID(), req, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil response")
	}
	if *res.Rating != 5 {
		t.Errorf("expected rating 5, got %d", *res.Rating)
	}
	if res.Message != "Great app!" {
		t.Errorf("expected message 'Great app!', got %q", res.Message)
	}
}

func TestSubmit_Success_RatingOnly(t *testing.T) {
	svc := newService(&mockRepo{})
	req := dto.CreateFeedbackRequest{Rating: ptrInt(4)}
	res, err := svc.Submit(context.Background(), userID(), req, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if *res.Rating != 4 {
		t.Errorf("expected rating 4, got %d", *res.Rating)
	}
}

func TestSubmit_Success_FeedbackOnly(t *testing.T) {
	svc := newService(&mockRepo{})
	req := dto.CreateFeedbackRequest{Feedback: ptrStr("Works well")}
	_, err := svc.Submit(context.Background(), userID(), req, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSubmit_Fail_AlreadySubmitted(t *testing.T) {
	existing := &feedbackdomain.Feedback{ID: "test-uuid"}
	svc := newService(&mockRepo{byUser: existing})
	req := dto.CreateFeedbackRequest{Rating: ptrInt(5)}
	_, err := svc.Submit(context.Background(), userID(), req, nil)
	if err == nil {
		t.Fatal("expected conflict error when user has already submitted feedback")
	}
}

func TestSubmit_Fail_BothEmpty(t *testing.T) {
	svc := newService(&mockRepo{})
	req := dto.CreateFeedbackRequest{}
	_, err := svc.Submit(context.Background(), userID(), req, nil)
	if err == nil {
		t.Fatal("expected validation error when both rating and message are absent")
	}
}

func TestSubmit_Fail_RatingTooLow(t *testing.T) {
	svc := newService(&mockRepo{})
	req := dto.CreateFeedbackRequest{Rating: ptrInt(0), Feedback: ptrStr("ok")}
	_, err := svc.Submit(context.Background(), userID(), req, nil)
	if err == nil {
		t.Fatal("expected error for rating 0")
	}
}

func TestSubmit_Fail_RatingTooHigh(t *testing.T) {
	svc := newService(&mockRepo{})
	req := dto.CreateFeedbackRequest{Rating: ptrInt(6), Feedback: ptrStr("ok")}
	_, err := svc.Submit(context.Background(), userID(), req, nil)
	if err == nil {
		t.Fatal("expected error for rating 6")
	}
}

func TestSubmit_Fail_MessageTooLong(t *testing.T) {
	svc := newService(&mockRepo{})
	// 1001 runes
	long := make([]rune, 1001)
	for i := range long {
		long[i] = 'a'
	}
	req := dto.CreateFeedbackRequest{Feedback: ptrStr(string(long))}
	_, err := svc.Submit(context.Background(), userID(), req, nil)
	if err == nil {
		t.Fatal("expected error for message over 1000 chars")
	}
}

func TestSubmit_Fail_InvalidPlatform(t *testing.T) {
	svc := newService(&mockRepo{})
	req := dto.CreateFeedbackRequest{
		Rating:   ptrInt(5),
		Platform: ptrStr("BLACKBERRY"),
	}
	_, err := svc.Submit(context.Background(), userID(), req, nil)
	if err == nil {
		t.Fatal("expected error for unknown platform")
	}
}

func TestSubmit_Fail_ZeroUserID(t *testing.T) {
	svc := newService(&mockRepo{})
	req := dto.CreateFeedbackRequest{Rating: ptrInt(5)}
	_, err := svc.Submit(context.Background(), 0, req, nil)
	if err == nil {
		t.Fatal("expected unauthorized error for zero userID")
	}
}

func TestSubmit_Fail_RepoError(t *testing.T) {
	svc := newService(&mockRepo{err: errors.New("db is down")})
	req := dto.CreateFeedbackRequest{Rating: ptrInt(3), Feedback: ptrStr("ok")}
	_, err := svc.Submit(context.Background(), userID(), req, nil)
	if err == nil {
		t.Fatal("expected error when repo fails")
	}
}

func TestSubmit_OnSubmittedHookCalled(t *testing.T) {
	svc := newService(&mockRepo{})
	hookCalled := false
	svc.SetOnSubmitted(func(_ context.Context, id, message string, rating *int) {
		hookCalled = true
	})
	req := dto.CreateFeedbackRequest{Rating: ptrInt(5), Feedback: ptrStr("nice")}
	_, err := svc.Submit(context.Background(), userID(), req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hookCalled {
		t.Error("expected OnSubmitted hook to be called")
	}
}

// --------------------------------------------------------------------------
// ListAll tests
// --------------------------------------------------------------------------

func TestListAll_InvalidRatingFilter(t *testing.T) {
	svc := newService(&mockRepo{})
	filter := dto.FeedbackFilter{Rating: ptrInt(6), Size: 20}
	_, err := svc.ListAll(context.Background(), filter)
	if err == nil {
		t.Fatal("expected validation error for rating filter > 5")
	}
}

func TestListAll_InvalidStatusFilter(t *testing.T) {
	svc := newService(&mockRepo{})
	filter := dto.FeedbackFilter{Status: ptrStr("PENDING"), Size: 20}
	_, err := svc.ListAll(context.Background(), filter)
	if err == nil {
		t.Fatal("expected validation error for unknown status")
	}
}

func TestListAll_EmptyResult(t *testing.T) {
	svc := newService(&mockRepo{listed: nil, listedTotal: 0})
	paged, err := svc.ListAll(context.Background(), dto.FeedbackFilter{Size: 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paged.TotalElements != 0 {
		t.Errorf("expected 0 elements, got %d", paged.TotalElements)
	}
}

// --------------------------------------------------------------------------
// GetSummary tests
// --------------------------------------------------------------------------

func TestGetSummary_Success(t *testing.T) {
	svc := newService(&mockRepo{
		summary: &feedbackdomain.FeedbackSummary{
			TotalReviews:  10,
			AverageRating: 4.2,
			FiveStar:      5,
			FourStar:      3,
			ThreeStar:     1,
			TwoStar:       1,
			OneStar:       0,
		},
	})
	summary, err := svc.GetSummary(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.TotalReviews != 10 {
		t.Errorf("expected 10 total reviews, got %d", summary.TotalReviews)
	}
	if summary.AverageRating != 4.2 {
		t.Errorf("expected avg 4.2, got %f", summary.AverageRating)
	}
}

// --------------------------------------------------------------------------
// UpdateStatus tests
// --------------------------------------------------------------------------

func TestUpdateStatus_InvalidStatus(t *testing.T) {
	svc := newService(&mockRepo{})
	req := dto.UpdateFeedbackRequest{Status: ptrStr("INVALID")}
	_, err := svc.UpdateStatus(context.Background(), "some-uuid-12345678901", 1, req)
	if err == nil {
		t.Fatal("expected validation error for invalid status")
	}
}

func TestUpdateStatus_NothingToUpdate(t *testing.T) {
	svc := newService(&mockRepo{})
	req := dto.UpdateFeedbackRequest{}
	_, err := svc.UpdateStatus(context.Background(), "some-uuid-12345678901", 1, req)
	if err == nil {
		t.Fatal("expected error when no update fields provided")
	}
}

func TestUpdateStatus_NotFound(t *testing.T) {
	svc := newService(&mockRepo{err: feedbackrepo.ErrNotFound})
	req := dto.UpdateFeedbackRequest{Status: ptrStr("RESOLVED")}
	_, err := svc.UpdateStatus(context.Background(), "some-uuid-12345678901", 1, req)
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

// --------------------------------------------------------------------------
// GetByID tests
// --------------------------------------------------------------------------

func TestGetByID_InvalidUUID(t *testing.T) {
	svc := newService(&mockRepo{})
	_, err := svc.GetByID(context.Background(), "not-a-uuid")
	if err == nil {
		t.Fatal("expected validation error for invalid UUID")
	}
}

func TestGetByID_NotFound(t *testing.T) {
	svc := newService(&mockRepo{})
	_, err := svc.GetByID(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected not-found error")
	}
}
