package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	helpdomain "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/dto"
	helprepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/repository"
	helpsvc "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/service"
)

type mockHelpRepo struct {
	userProfile *helpdomain.UserProfile
	userErr     error

	created *helpdomain.HelpCenterMessage
	byID    *helpdomain.HelpCenterMessage
	getErr  error

	readMarkedID string

	listed      []helpdomain.HelpCenterMessage
	listedTotal int64
	listErr     error

	deletedID string
	deleteErr error
}

func (m *mockHelpRepo) GetUserProfile(_ context.Context, userID int64) (*helpdomain.UserProfile, error) {
	if m.userErr != nil {
		return nil, m.userErr
	}
	if m.userProfile != nil {
		return m.userProfile, nil
	}
	email := "saif@example.com"
	phone := "9876543210"
	return &helpdomain.UserProfile{
		ID:    userID,
		Name:  "Saif Ali",
		Email: &email,
		Phone: &phone,
	}, nil
}

func (m *mockHelpRepo) Create(_ context.Context, msg *helpdomain.HelpCenterMessage) (*helpdomain.HelpCenterMessage, error) {
	res := *msg
	res.ID = "550e8400-e29b-41d4-a716-446655440000"
	res.CreatedAt = time.Now()
	res.UpdatedAt = time.Now()
	res.Status = helpdomain.StatusNew
	m.created = &res
	return &res, nil
}

func (m *mockHelpRepo) GetByID(_ context.Context, id string) (*helpdomain.HelpCenterMessage, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.byID != nil {
		return m.byID, nil
	}
	return nil, helprepo.ErrNotFound
}

func (m *mockHelpRepo) MarkAsRead(_ context.Context, id string) error {
	m.readMarkedID = id
	return nil
}

func (m *mockHelpRepo) List(_ context.Context, _ dto.HelpMessageFilter) ([]helpdomain.HelpCenterMessage, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	return m.listed, m.listedTotal, nil
}

func (m *mockHelpRepo) Delete(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedID = id
	return nil
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

func TestSubmit_Success_AutoFetchesUserDetails(t *testing.T) {
	repo := &mockHelpRepo{}
	svc := helpsvc.New(repo)

	var hookCalled bool
	var notifiedMsg *helpdomain.HelpCenterMessage
	svc.SetOnMessageCreated(func(_ context.Context, msg *helpdomain.HelpCenterMessage) {
		hookCalled = true
		notifiedMsg = msg
	})

	req := dto.CreateHelpMessageRequest{
		Message: "I am unable to book a marriage hall for my selected date.",
	}

	res, err := svc.Submit(context.Background(), 456, req)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if res.ID == "" {
		t.Error("expected non-empty ID")
	}
	if res.UserName != "Saif Ali" || res.UserNameSnake != "Saif Ali" {
		t.Errorf("expected user name 'Saif Ali', got %q", res.UserName)
	}
	if res.UserEmail == nil || *res.UserEmail != "saif@example.com" {
		t.Errorf("expected email 'saif@example.com', got %v", res.UserEmail)
	}
	if res.UserPhone == nil || *res.UserPhone != "9876543210" {
		t.Errorf("expected phone '9876543210', got %v", res.UserPhone)
	}
	if res.Status != "NEW" {
		t.Errorf("expected status 'NEW', got %q", res.Status)
	}
	if res.Message != req.Message {
		t.Errorf("expected message %q, got %q", req.Message, res.Message)
	}

	if !hookCalled {
		t.Error("expected OnMessageCreated hook to be invoked")
	}
	if notifiedMsg == nil || notifiedMsg.ID != res.ID {
		t.Errorf("expected hook to receive created message with ID %s", res.ID)
	}
}

func TestSubmit_NotificationFailure_DoesNotFailMessageCreation(t *testing.T) {
	repo := &mockHelpRepo{}
	svc := helpsvc.New(repo)

	// Notification hook panics or errors - message creation must still succeed!
	svc.SetOnMessageCreated(func(_ context.Context, _ *helpdomain.HelpCenterMessage) {
		panic("simulated push gateway outage")
	})

	req := dto.CreateHelpMessageRequest{
		Message: "Need help with hall refund.",
	}

	res, err := svc.Submit(context.Background(), 789, req)
	if err != nil {
		t.Fatalf("expected message submission to succeed despite notification failure, got: %v", err)
	}
	if res == nil || res.ID == "" {
		t.Fatal("expected saved message response")
	}
}

func TestSubmit_Validation_RejectsEmptyOrWhitespace(t *testing.T) {
	svc := helpsvc.New(&mockHelpRepo{})

	for _, msg := range []string{"", "   ", "\t\n  \n"} {
		req := dto.CreateHelpMessageRequest{Message: msg}
		_, err := svc.Submit(context.Background(), 100, req)
		if err == nil {
			t.Errorf("expected error for empty/whitespace message %q, got nil", msg)
		}
	}
}

func TestSubmit_Validation_RejectsTooLongMessage(t *testing.T) {
	svc := helpsvc.New(&mockHelpRepo{})
	longMsg := strings.Repeat("A", 2001)

	req := dto.CreateHelpMessageRequest{Message: longMsg}
	_, err := svc.Submit(context.Background(), 100, req)
	if err == nil {
		t.Fatal("expected error for message exceeding 2000 chars, got nil")
	}
}

func TestSubmit_RejectsUnauthenticated(t *testing.T) {
	svc := helpsvc.New(&mockHelpRepo{})
	req := dto.CreateHelpMessageRequest{Message: "Help"}

	_, err := svc.Submit(context.Background(), 0, req)
	if err == nil {
		t.Fatal("expected unauthorized error for userID <= 0, got nil")
	}

	_, err = svc.Submit(context.Background(), -1, req)
	if err == nil {
		t.Fatal("expected unauthorized error for negative userID, got nil")
	}
}

func TestSubmit_UserNotFound(t *testing.T) {
	repo := &mockHelpRepo{userErr: helprepo.ErrUserNotFound}
	svc := helpsvc.New(repo)

	req := dto.CreateHelpMessageRequest{Message: "Help"}
	_, err := svc.Submit(context.Background(), 9999, req)
	if err == nil {
		t.Fatal("expected not found error, got nil")
	}
}

func TestGetByID_Success_MarksNewAsRead(t *testing.T) {
	repo := &mockHelpRepo{
		byID: &helpdomain.HelpCenterMessage{
			ID:        "550e8400-e29b-41d4-a716-446655440000",
			UserName:  "Saif Ali",
			Message:   "Testing view",
			Status:    helpdomain.StatusNew,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	svc := helpsvc.New(repo)

	res, err := svc.GetByID(context.Background(), "550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if res.Status != "READ" {
		t.Errorf("expected message status to be updated to READ, got %q", res.Status)
	}
	if repo.readMarkedID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected repo.MarkAsRead to be called with ID, got %q", repo.readMarkedID)
	}
}

func TestGetByID_InvalidUUID(t *testing.T) {
	svc := helpsvc.New(&mockHelpRepo{})

	_, err := svc.GetByID(context.Background(), "invalid-uuid")
	if err == nil {
		t.Fatal("expected validation error for invalid UUID, got nil")
	}
}

func TestGetByID_NotFound(t *testing.T) {
	repo := &mockHelpRepo{getErr: helprepo.ErrNotFound}
	svc := helpsvc.New(repo)

	_, err := svc.GetByID(context.Background(), "550e8400-e29b-41d4-a716-446655440000")
	if err == nil {
		t.Fatal("expected not found error, got nil")
	}
}

func TestDelete_Success(t *testing.T) {
	repo := &mockHelpRepo{}
	svc := helpsvc.New(repo)

	err := svc.Delete(context.Background(), "550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if repo.deletedID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected delete on id %s, got %s", "550e8400-e29b-41d4-a716-446655440000", repo.deletedID)
	}
}

func TestDelete_NotFound(t *testing.T) {
	repo := &mockHelpRepo{deleteErr: helprepo.ErrNotFound}
	svc := helpsvc.New(repo)

	err := svc.Delete(context.Background(), "550e8400-e29b-41d4-a716-446655440000")
	if err == nil {
		t.Fatal("expected not found error, got nil")
	}
}

func TestListAll_FiltersValidation(t *testing.T) {
	svc := helpsvc.New(&mockHelpRepo{})

	invalidStatus := "UNKNOWN_STATUS"
	_, err := svc.ListAll(context.Background(), dto.HelpMessageFilter{
		Status: &invalidStatus,
	})
	if err == nil {
		t.Fatal("expected error for invalid status, got nil")
	}

	from := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	_, err = svc.ListAll(context.Background(), dto.HelpMessageFilter{
		FromDate: &from,
		ToDate:   &to,
	})
	if err == nil {
		t.Fatal("expected error when from_date is after to_date, got nil")
	}
}
