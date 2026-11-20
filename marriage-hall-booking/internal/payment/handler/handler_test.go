package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/payment/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestNormalizeWebhookPayload(t *testing.T) {
	// 1. Official Cashfree PG Webhook payload structure
	cfJSON := `{
		"data": {
			"order": {
				"order_id": "ord_test_001",
				"order_amount": 12500.00,
				"order_currency": "INR"
			},
			"payment": {
				"cf_payment_id": "cf_pay_9988",
				"payment_status": "SUCCESS",
				"payment_amount": 12500.00,
				"payment_message": "Payment succeeded via UPI"
			}
		},
		"event_time": "2026-11-20T12:00:00Z",
		"type": "PAYMENT_SUCCESS_WEBHOOK"
	}`

	p, err := normalizeWebhookPayload([]byte(cfJSON))
	if err != nil {
		t.Fatalf("unexpected error normalizing Cashfree payload: %v", err)
	}
	if p.OrderID != "ord_test_001" {
		t.Errorf("got OrderID %q, want ord_test_001", p.OrderID)
	}
	if p.PaymentID != "cf_pay_9988" {
		t.Errorf("got PaymentID %q, want cf_pay_9988", p.PaymentID)
	}
	if p.Status != "SUCCESS" {
		t.Errorf("got Status %q, want SUCCESS", p.Status)
	}
	if p.Amount != 12500.00 {
		t.Errorf("got Amount %v, want 12500.00", p.Amount)
	}
	if p.EventID != "ord_test_001:cf_pay_9988" {
		t.Errorf("got EventID %q, want ord_test_001:cf_pay_9988", p.EventID)
	}

	// 2. Failure webhook payload
	failJSON := `{
		"data": {
			"order": {
				"order_id": "ord_fail_002",
				"order_amount": 5000.00
			},
			"payment": {
				"cf_payment_id": "cf_pay_fail",
				"payment_status": "FAILED",
				"payment_amount": 5000.00,
				"payment_message": "Bank declined transaction"
			}
		},
		"event_id": "evt_fail_1",
		"type": "PAYMENT_FAILED_WEBHOOK"
	}`
	pFail, err := normalizeWebhookPayload([]byte(failJSON))
	if err != nil {
		t.Fatalf("unexpected error normalizing failure payload: %v", err)
	}
	if pFail.Status != "FAILED" {
		t.Errorf("got Status %q, want FAILED", pFail.Status)
	}
	if pFail.Reason != "Bank declined transaction" {
		t.Errorf("got Reason %q, want 'Bank declined transaction'", pFail.Reason)
	}
	if pFail.EventID != "evt_fail_1" {
		t.Errorf("got EventID %q, want evt_fail_1", pFail.EventID)
	}

	// 3. User dropped webhook
	dropJSON := `{
		"data": {
			"order": {
				"order_id": "ord_drop_003"
			},
			"payment": {
				"cf_payment_id": "cf_pay_drop",
				"payment_status": "USER_DROPPED"
			}
		}
	}`
	pDrop, err := normalizeWebhookPayload([]byte(dropJSON))
	if err != nil {
		t.Fatalf("unexpected error on user dropped payload: %v", err)
	}
	if pDrop.Status != "FAILED" {
		t.Errorf("got Status %q, want FAILED for USER_DROPPED", pDrop.Status)
	}

	// 4. Flat payload
	flatJSON := `{
		"eventId": "evt_flat_1",
		"orderId": "ord_flat_1",
		"paymentId": "pay_flat_1",
		"status": "SUCCESS",
		"amount": 7500.00
	}`
	pFlat, err := normalizeWebhookPayload([]byte(flatJSON))
	if err != nil {
		t.Fatalf("unexpected error normalizing flat payload: %v", err)
	}
	if pFlat.OrderID != "ord_flat_1" || pFlat.Status != "SUCCESS" || pFlat.Amount != 7500.00 {
		t.Errorf("flat payload normalized incorrectly: %+v", pFlat)
	}
}

func TestWebhookHandler_SignatureVerification(t *testing.T) {
	svc := service.New(nil, nil, "secret-key-456")
	mockProvider := service.NewMockPaymentProvider()
	mockProvider.Secret = "secret-key-456"
	svc.SetCashfreeClient(mockProvider)

	signer, _ := jwt.NewSigner("A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v2W3x4Y5z6A7b8C9d0E1f2", time.Hour)
	h := New(svc, signer)

	// 1. Request with invalid signature and real payload should return 401
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhook", bytes.NewReader([]byte(`{"order_id":"real_123"}`)))
	req.Header.Set("x-webhook-signature", "invalid-sig")
	rec := httptest.NewRecorder()
	h.webhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// 2. Empty ping from dashboard should acknowledge with 200 OK
	reqEmpty := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhook", bytes.NewReader([]byte(``)))
	recEmpty := httptest.NewRecorder()
	h.webhook(recEmpty, reqEmpty)
	if recEmpty.Code != http.StatusOK {
		t.Errorf("empty ping got status %d, want 200", recEmpty.Code)
	}

	// 3. Test ping from dashboard should acknowledge with 200 OK
	reqTestProbe := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhook", bytes.NewReader([]byte(`{"type":"TEST_NOTIFICATION"}`)))
	recTestProbe := httptest.NewRecorder()
	h.webhook(recTestProbe, reqTestProbe)
	if recTestProbe.Code != http.StatusOK {
		t.Errorf("test probe got status %d, want 200", recTestProbe.Code)
	}

	// 4. Request with valid mock signature but pending status acknowledges with 200
	pendingBody := []byte(`{
		"data": {
			"order": {"order_id": "ord_p1"},
			"payment": {"payment_status": "PENDING"}
		}
	}`)
	reqPending := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhook", bytes.NewReader(pendingBody))
	reqPending.Header.Set("x-webhook-signature", "mock-valid-signature")
	recPending := httptest.NewRecorder()
	h.webhook(recPending, reqPending)

	if recPending.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", recPending.Code, http.StatusOK)
	}
}
