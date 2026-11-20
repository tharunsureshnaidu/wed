package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCashfreeCreateOrder_Success(t *testing.T) {
	var receivedBody map[string]any
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		if r.URL.Path != "/pg/orders" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"cf_order_id": "cf_1234567890",
			"order_id": "ord_sample_123",
			"order_amount": 25000.50,
			"order_currency": "INR",
			"payment_session_id": "session_cf_test_xyz"
		}`))
	}))
	defer server.Close()

	client := NewCashfreeClient("sandbox", "test-key", "test-secret", "test-wh-secret", server.URL, "https://api.example.com")
	order, err := client.CreateOrder(
		context.Background(),
		"123e4567-e89b-12d3-a456-426614174000",
		42,
		25000.50,
		"INR",
		"customer@domain.com",
		"+91 98765 43210",
	)
	if err != nil {
		t.Fatalf("unexpected error creating order: %v", err)
	}

	if order.OrderID != "ord_sample_123" {
		t.Errorf("got order ID %q, want ord_sample_123", order.OrderID)
	}
	if order.SessionID != "session_cf_test_xyz" {
		t.Errorf("got session ID %q, want session_cf_test_xyz", order.SessionID)
	}
	if order.Amount != 25000.50 {
		t.Errorf("got amount %v, want 25000.50", order.Amount)
	}
	if !strings.Contains(order.CheckoutURL, "session_cf_test_xyz") {
		t.Errorf("checkout url %q does not contain session ID", order.CheckoutURL)
	}

	// Verify headers
	if receivedHeaders.Get("x-client-id") != "test-key" {
		t.Errorf("x-client-id: got %q, want test-key", receivedHeaders.Get("x-client-id"))
	}
	if receivedHeaders.Get("x-client-secret") != "test-secret" {
		t.Errorf("x-client-secret: got %q, want test-secret", receivedHeaders.Get("x-client-secret"))
	}
	if receivedHeaders.Get("x-api-version") != "2026-01-01" {
		t.Errorf("x-api-version: got %q, want 2026-01-01", receivedHeaders.Get("x-api-version"))
	}

	// Verify payload sanitation
	cust, _ := receivedBody["customer_details"].(map[string]any)
	if cust["customer_phone"] != "9876543210" {
		t.Errorf("expected sanitized 10-digit phone 9876543210, got %v", cust["customer_phone"])
	}
	sentOrderID, _ := receivedBody["order_id"].(string)
	if len(sentOrderID) > 50 {
		t.Errorf("generated order_id length %d exceeds Cashfree 50-char limit: %s", len(sentOrderID), sentOrderID)
	}
	if !strings.HasPrefix(sentOrderID, "ord_") {
		t.Errorf("expected order_id to start with ord_, got %s", sentOrderID)
	}
}

func TestCashfreeCreateOrder_ValidationAndErrors(t *testing.T) {
	// 1. Missing credentials
	nilClient := (*CashfreeClient)(nil)
	if _, err := nilClient.CreateOrder(context.Background(), "b1", 1, 100, "INR", "", ""); err == nil {
		t.Fatal("expected error on nil client")
	}

	emptyClient := NewCashfreeClient("sandbox", "", "", "", "", "")
	if _, err := emptyClient.CreateOrder(context.Background(), "b1", 1, 100, "INR", "", ""); err == nil {
		t.Fatal("expected error on unconfigured credentials")
	}

	// 2. Non-positive amount
	configuredClient := NewCashfreeClient("sandbox", "k", "s", "w", "http://localhost", "")
	if _, err := configuredClient.CreateOrder(context.Background(), "b1", 1, 0, "INR", "", ""); err == nil {
		t.Fatal("expected error on zero amount")
	}
	if _, err := configuredClient.CreateOrder(context.Background(), "b1", 1, -50, "INR", "", ""); err == nil {
		t.Fatal("expected error on negative amount")
	}

	// 3. Upstream error response
	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message": "order_amount is invalid", "code": "order_amount_invalid"}`))
	}))
	defer errorServer.Close()

	errClient := NewCashfreeClient("sandbox", "k", "s", "w", errorServer.URL, "")
	_, err := errClient.CreateOrder(context.Background(), "b1", 1, 100, "INR", "", "")
	if err == nil {
		t.Fatal("expected error on 400 from cashfree")
	}
	if !strings.Contains(err.Error(), "order_amount_invalid") {
		t.Fatalf("expected error to contain cashfree message, got %v", err)
	}
}

func TestCashfreeGetOrderStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/pg/orders/ord_123") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"order_id": "ord_123",
			"order_amount": 15000.00,
			"order_currency": "INR",
			"order_status": "PAID"
		}`))
	}))
	defer server.Close()

	client := NewCashfreeClient("sandbox", "k", "s", "w", server.URL, "")
	status, err := client.GetOrderStatus(context.Background(), "ord_123")
	if err != nil {
		t.Fatalf("unexpected error fetching order status: %v", err)
	}
	if status.OrderID != "ord_123" {
		t.Errorf("got order ID %q, want ord_123", status.OrderID)
	}
	if status.Status != "PAID" {
		t.Errorf("got status %q, want PAID", status.Status)
	}
	if status.Amount != 15000.00 {
		t.Errorf("got amount %v, want 15000", status.Amount)
	}

	// Order ID required
	if _, err := client.GetOrderStatus(context.Background(), "   "); err == nil {
		t.Fatal("expected error on empty order id")
	}
}

func TestCashfreeRefund(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/pg/orders/ord_123/refunds") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"cf_refund_id": "cfr_998877",
			"refund_id": "rfnd_my_ref",
			"refund_status": "SUCCESS"
		}`))
	}))
	defer server.Close()

	client := NewCashfreeClient("sandbox", "k", "s", "w", server.URL, "")
	refund, err := client.Refund(context.Background(), "ord_123", "rfnd_my_ref", 5000.00, "INR", "Customer cancellation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if refund.RefundID != "cfr_998877" {
		t.Errorf("got refund ID %q, want cfr_998877", refund.RefundID)
	}
	if refund.Status != "SUCCESS" {
		t.Errorf("got status %q, want SUCCESS", refund.Status)
	}
}

func TestCashfreeVerifyWebhook(t *testing.T) {
	secret := "test-secret-key-12345"
	client := NewCashfreeClient("sandbox", "k", secret, secret, "", "")

	body := []byte(`{"data":{"order":{"order_id":"ord_1"},"payment":{"payment_status":"SUCCESS"}}}`)
	timestamp := "1700000000"

	// 1. Signature with timestamp (signedPayload = timestamp + body)
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(timestamp))
	_, _ = h.Write(body)
	validSigWithTS := base64.StdEncoding.EncodeToString(h.Sum(nil))

	if !client.VerifyWebhook(body, validSigWithTS, timestamp) {
		t.Fatal("expected valid signature with timestamp to pass verification")
	}

	// 2. Signature without timestamp (raw body)
	hRaw := hmac.New(sha256.New, []byte(secret))
	_, _ = hRaw.Write(body)
	validSigRaw := base64.StdEncoding.EncodeToString(hRaw.Sum(nil))

	if !client.VerifyWebhook(body, validSigRaw, "") {
		t.Fatal("expected valid raw signature to pass verification")
	}

	// 3. Tampered body should fail
	tamperedBody := []byte(`{"data":{"order":{"order_id":"ord_1"},"payment":{"payment_status":"FAILED"}}}`)
	if client.VerifyWebhook(tamperedBody, validSigWithTS, timestamp) {
		t.Fatal("tampered body must fail verification")
	}

	// 4. Empty signature or client should fail
	if client.VerifyWebhook(body, "", timestamp) {
		t.Fatal("empty signature must fail")
	}
	var nilClient *CashfreeClient
	if nilClient.VerifyWebhook(body, validSigWithTS, timestamp) {
		t.Fatal("nil client must fail")
	}
}

func TestMockPaymentProvider(t *testing.T) {
	mock := NewMockPaymentProvider()

	order, err := mock.CreateOrder(context.Background(), "bk-test-1", 10, 5000, "INR", "u@test.com", "9999999999")
	if err != nil {
		t.Fatalf("unexpected error from mock provider: %v", err)
	}
	if !strings.HasPrefix(order.OrderID, "cf_mock_") {
		t.Errorf("order id %q should have cf_mock_ prefix", order.OrderID)
	}
	if !strings.HasPrefix(order.SessionID, "session_mock_") {
		t.Errorf("session id %q should have session_mock_ prefix", order.SessionID)
	}
	if order.Amount != 5000 {
		t.Errorf("amount: got %v want 5000", order.Amount)
	}

	status, err := mock.GetOrderStatus(context.Background(), order.OrderID)
	if err != nil {
		t.Fatalf("unexpected error getting mock status: %v", err)
	}
	if status.Status != "PAID" {
		t.Errorf("mock status: got %q want PAID", status.Status)
	}

	refund, err := mock.Refund(context.Background(), order.OrderID, "rf_1", 2000, "INR", "test reason")
	if err != nil {
		t.Fatalf("unexpected refund error: %v", err)
	}
	if refund.Status != "SUCCESS" {
		t.Errorf("refund status: got %q want SUCCESS", refund.Status)
	}

	if !mock.VerifyWebhook([]byte("{}"), "mock-valid-signature", "") {
		t.Fatal("mock should verify mock-valid-signature")
	}
}
