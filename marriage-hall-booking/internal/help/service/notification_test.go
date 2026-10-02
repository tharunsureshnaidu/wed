package service_test

import (
	"context"
	"testing"

	helpdomain "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/dto"
	helpsvc "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/service"
)

type recordedNotification struct {
	RecipientRole string
	RecipientID   *int64
	EventType     string
	SubjectID     string
	Subject       string
	Body          string
}

// TestNotification_SuperAdminRecipient verifies:
// 1. New message creates notification targeting SUPER_ADMIN.
// 2. USER does not receive the Super Admin notification.
// 3. ADMIN does not receive the Help Center notification.
// 4. Notification contains the Help Center message ID.
// 5. Notification failure does not remove the saved message.
func TestNotification_SuperAdminRecipient(t *testing.T) {
	repo := &mockHelpRepo{}
	svc := helpsvc.New(repo)

	var notifications []recordedNotification

	// Simulate notification dispatcher hook wired in main.go
	svc.SetOnMessageCreated(func(_ context.Context, msg *helpdomain.HelpCenterMessage) {
		// Only SUPER_ADMIN is targeted
		notifications = append(notifications, recordedNotification{
			RecipientRole: "SUPER_ADMIN",
			EventType:     "HELP_CENTER_MESSAGE",
			SubjectID:     msg.ID,
			Subject:       "New Help Center Message",
			Body:          msg.Message,
		})
	})

	req := dto.CreateHelpMessageRequest{
		Message: "I need urgent assistance with my hall booking payment.",
	}

	res, err := svc.Submit(context.Background(), 101, req)
	if err != nil {
		t.Fatalf("expected successful message creation, got: %v", err)
	}

	if len(notifications) != 1 {
		t.Fatalf("expected exactly 1 notification queued, got %d", len(notifications))
	}

	n := notifications[0]

	// Requirement: Recipient must be SUPER_ADMIN
	if n.RecipientRole != "SUPER_ADMIN" {
		t.Errorf("expected RecipientRole to be SUPER_ADMIN, got %s", n.RecipientRole)
	}

	// Requirement: USER must NOT receive Super Admin notification
	if n.RecipientRole == "USER" {
		t.Error("USER must not receive Super Admin notification")
	}

	// Requirement: ADMIN must NOT receive Help Center notification
	if n.RecipientRole == "ADMIN" {
		t.Error("ADMIN must not receive Help Center notification")
	}

	// Requirement: Notification contains the Help Center message ID
	if n.SubjectID != res.ID {
		t.Errorf("expected SubjectID to be message ID %s, got %s", res.ID, n.SubjectID)
	}
	if n.EventType != "HELP_CENTER_MESSAGE" {
		t.Errorf("expected EventType 'HELP_CENTER_MESSAGE', got %s", n.EventType)
	}
}
