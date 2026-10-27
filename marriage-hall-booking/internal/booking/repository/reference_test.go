package repository

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGenerateReferenceID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		ref := GenerateReferenceID()
		if !strings.HasPrefix(ref, "BK-") {
			t.Fatalf("expected reference ID to start with BK-, got %q", ref)
		}
		if len(ref) != 13 { // "BK-" (3) + 10 hex characters = 13
			t.Fatalf("expected reference ID length 13, got %d for %q", len(ref), ref)
		}
		if seen[ref] {
			t.Fatalf("duplicate reference ID generated: %q", ref)
		}
		seen[ref] = true
	}
}

func TestBookingJSONContainsReferenceID(t *testing.T) {
	b := Booking{
		ID:               "11111111-2222-3333-4444-555555555555",
		ReferenceID:      "BK-A1B2C3D4E5",
		BookingReference: "BK-A1B2C3D4E5",
		UserID:           100,
		TargetType:       "HALL",
		TargetID:         "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		CheckIn:          time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC),
		CheckOut:         time.Date(2027, 5, 2, 0, 0, 0, 0, time.UTC),
		TotalAmount:      150000,
		PaidAmount:       50000,
		Status:           "CONFIRMED",
		Payments: []BookingPayment{
			{
				ID:          "pay-123",
				Amount:      50000,
				Currency:    "INR",
				Status:      "SUCCESS",
				Gateway:     "RAZORPAY",
				PaymentType: "ADVANCE",
			},
		},
	}

	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("failed to marshal booking: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"referenceId":"BK-A1B2C3D4E5"`) {
		t.Errorf("expected JSON to contain referenceId, got: %s", raw)
	}
	if !strings.Contains(raw, `"bookingReference":"BK-A1B2C3D4E5"`) {
		t.Errorf("expected JSON to contain bookingReference, got: %s", raw)
	}
	if !strings.Contains(raw, `"payments":[`) {
		t.Errorf("expected JSON to contain payments, got: %s", raw)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if parsed["referenceId"] != "BK-A1B2C3D4E5" {
		t.Errorf("parsed referenceId = %v, want BK-A1B2C3D4E5", parsed["referenceId"])
	}
}
