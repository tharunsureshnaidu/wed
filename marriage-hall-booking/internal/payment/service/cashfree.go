package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
)

// PaymentProvider is the provider contract used by the booking payment flow.
type PaymentProvider interface {
	CreateOrder(ctx context.Context, bookingID string, userID int64, amount float64, currency string, customerEmail string, customerPhone string) (*ProviderOrder, error)
	GetOrderStatus(ctx context.Context, orderID string) (*ProviderOrderStatus, error)
	Refund(ctx context.Context, orderID, refundID string, amount float64, currency, reason string) (*ProviderRefund, error)
	VerifyWebhook(body []byte, signature, timestamp string) bool
}

type ProviderOrder struct {
	OrderID        string   `json:"orderId"`
	SessionID      string   `json:"paymentSessionId,omitempty"`
	CheckoutURL    string   `json:"checkoutUrl,omitempty"`
	ReferenceID    string   `json:"referenceId,omitempty"`
	Status         string   `json:"status,omitempty"`
	PaymentMethods []string `json:"paymentMethods,omitempty"`
	Amount         float64  `json:"amount"`
	Currency       string   `json:"currency,omitempty"`
	CustomerEmail  string   `json:"customerEmail,omitempty"`
	CustomerPhone  string   `json:"customerPhone,omitempty"`
}

type ProviderOrderStatus struct {
	OrderID   string  `json:"orderId"`
	Status    string  `json:"status"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Reference string  `json:"referenceId,omitempty"`
}

type ProviderRefund struct {
	RefundID string `json:"refundId"`
	Status   string `json:"status,omitempty"`
}

type CashfreeClient struct {
	baseURL         string
	apiKey          string
	apiSecret       string
	webhookSecret   string
	publicBaseURL   string
	checkoutBaseURL string
	httpClient      *http.Client
}

func NewCashfreeClient(env, apiKey, apiSecret, webhookSecret, baseURL, publicBaseURL string) *CashfreeClient {
	if strings.TrimSpace(baseURL) == "" {
		if strings.EqualFold(env, "production") {
			baseURL = "https://api.cashfree.com"
		} else {
			baseURL = "https://sandbox.cashfree.com"
		}
	}
	checkoutBaseURL := "https://payments.cashfree.com"
	if strings.EqualFold(strings.TrimSpace(env), "production") {
		checkoutBaseURL = "https://payments.cashfree.com"
	} else {
		checkoutBaseURL = "https://sandbox.cashfree.com"
	}
	return &CashfreeClient{
		baseURL:         strings.TrimRight(baseURL, "/"),
		apiKey:          strings.TrimSpace(apiKey),
		apiSecret:       strings.TrimSpace(apiSecret),
		webhookSecret:   strings.TrimSpace(webhookSecret),
		publicBaseURL:   strings.TrimRight(strings.TrimSpace(publicBaseURL), "/"),
		checkoutBaseURL: checkoutBaseURL,
		httpClient:      &http.Client{Timeout: 15 * time.Second},
	}
}

// sanitizePhone ensures Cashfree gets a valid 10-digit number.
func sanitizePhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if len(digits) == 12 && strings.HasPrefix(digits, "91") {
		digits = digits[2:]
	}
	if len(digits) < 10 {
		return "9999999999"
	}
	if len(digits) > 10 {
		return digits[len(digits)-10:]
	}
	return digits
}

func (c *CashfreeClient) CreateOrder(ctx context.Context, bookingID string, userID int64, amount float64, currency string, customerEmail string, customerPhone string) (*ProviderOrder, error) {
	if c == nil || c.apiKey == "" || c.apiSecret == "" {
		return nil, fmt.Errorf("cashfree credentials not configured")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if strings.TrimSpace(currency) == "" {
		currency = "INR"
	}

	// Cashfree order_id allows <= 50 characters, alphanumeric + -_
	cleanID := strings.ReplaceAll(bookingID, "-", "")
	if len(cleanID) > 16 {
		cleanID = cleanID[:16]
	}
	merchantOrderID := fmt.Sprintf("ord_%s_%d", cleanID, time.Now().Unix())

	customerPhone = sanitizePhone(customerPhone)
	customerEmail = strings.TrimSpace(customerEmail)
	if customerEmail == "" {
		customerEmail = fmt.Sprintf("user%d@example.com", userID)
	}

	publicURL := strings.TrimRight(strings.TrimSpace(c.publicBaseURL), "/")
	if publicURL == "" || strings.Contains(publicURL, "localhost") || strings.Contains(publicURL, "127.0.0.1") {
		publicURL = "https://apnavenues.com"
	}
	returnURL := publicURL + "/payments/return"
	notifyURL := publicURL + "/api/v1/payments/webhook"

	payload := map[string]any{
		"order_id":       merchantOrderID,
		"order_amount":   amount,
		"order_currency": currency,
		"customer_details": map[string]any{
			"customer_id":    fmt.Sprintf("user-%d", userID),
			"customer_email": customerEmail,
			"customer_phone": customerPhone,
		},
		"order_meta": map[string]any{
			"return_url": returnURL,
			"notify_url": notifyURL,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/pg/orders", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-id", c.apiKey)
	req.Header.Set("x-client-secret", c.apiSecret)
	req.Header.Set("x-api-version", "2026-01-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		cleanBody := strings.TrimSpace(string(respBody))
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, apperr.BadGateway("PAYMENT_GATEWAY_AUTH_FAILED",
				"Cashfree authentication failed: please ensure your CASHFREE_CLIENT_ID and CASHFREE_CLIENT_SECRET in .env are generated from Payment Gateway (PG), not Secure ID")
		}
		return nil, apperr.BadGateway("PAYMENT_GATEWAY_ERROR", fmt.Sprintf("Cashfree error (%d): %s", resp.StatusCode, cleanBody))
	}

	var result struct {
		CFOrderID        string  `json:"cf_order_id"`
		OrderID          string  `json:"order_id"`
		OrderAmount      float64 `json:"order_amount"`
		OrderCurrency    string  `json:"order_currency"`
		PaymentSessionID string  `json:"payment_session_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	if result.OrderID == "" {
		result.OrderID = result.CFOrderID
	}
	if result.OrderID == "" || result.PaymentSessionID == "" {
		return nil, fmt.Errorf("cashfree returned an incomplete order")
	}
	return &ProviderOrder{
		OrderID:       result.OrderID,
		SessionID:     result.PaymentSessionID,
		Amount:        result.OrderAmount,
		Currency:      result.OrderCurrency,
		CheckoutURL:   c.checkoutBaseURL + "/pg/view?payment_session_id=" + url.QueryEscape(result.PaymentSessionID),
		CustomerEmail: customerEmail,
		CustomerPhone: customerPhone,
	}, nil
}

func (c *CashfreeClient) GetOrderStatus(ctx context.Context, orderID string) (*ProviderOrderStatus, error) {
	if c == nil || c.apiKey == "" || c.apiSecret == "" {
		return nil, fmt.Errorf("cashfree credentials not configured")
	}
	if strings.TrimSpace(orderID) == "" {
		return nil, fmt.Errorf("order id required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/pg/orders/%s", c.baseURL, orderID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-client-id", c.apiKey)
	req.Header.Set("x-client-secret", c.apiSecret)
	req.Header.Set("x-api-version", "2026-01-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("cashfree status error: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var out struct {
		OrderID       string  `json:"order_id"`
		CFOrderID     string  `json:"cf_order_id"`
		OrderAmount   float64 `json:"order_amount"`
		OrderCurrency string  `json:"order_currency"`
		OrderStatus   string  `json:"order_status"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.OrderID == "" {
		out.OrderID = out.CFOrderID
	}
	return &ProviderOrderStatus{OrderID: out.OrderID, Status: out.OrderStatus, Amount: out.OrderAmount, Currency: out.OrderCurrency}, nil
}

func (c *CashfreeClient) Refund(ctx context.Context, orderID, refundID string, amount float64, currency, reason string) (*ProviderRefund, error) {
	if c == nil || c.apiKey == "" || c.apiSecret == "" {
		return nil, fmt.Errorf("cashfree credentials not configured")
	}
	payload := map[string]any{
		"refund_id":     refundID,
		"refund_amount": amount,
		"refund_note":   strings.TrimSpace(reason),
		"refund_speed":  "STANDARD",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	reqURL := fmt.Sprintf("%s/pg/orders/%s/refunds", c.baseURL, orderID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-id", c.apiKey)
	req.Header.Set("x-client-secret", c.apiSecret)
	req.Header.Set("x-api-version", "2026-01-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("cashfree refund failed: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var out struct {
		CFRefundID string `json:"cf_refund_id"`
		RefundID   string `json:"refund_id"`
		Status     string `json:"refund_status"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, err
	}
	gatewayID := out.CFRefundID
	if gatewayID == "" {
		gatewayID = out.RefundID
	}
	if gatewayID == "" {
		gatewayID = refundID
	}
	return &ProviderRefund{RefundID: gatewayID, Status: out.Status}, nil
}

func (c *CashfreeClient) VerifyWebhook(body []byte, signature, timestamp string) bool {
	if c == nil {
		return false
	}
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return false
	}
	secrets := make([]string, 0, 2)
	if strings.TrimSpace(c.webhookSecret) != "" {
		secrets = append(secrets, c.webhookSecret)
	}
	if strings.TrimSpace(c.apiSecret) != "" && c.apiSecret != c.webhookSecret {
		secrets = append(secrets, c.apiSecret)
	}
	if len(secrets) == 0 {
		return false
	}

	for _, sec := range secrets {
		candidates := make([]string, 0, 2)
		if strings.TrimSpace(timestamp) != "" {
			h := hmac.New(sha256.New, []byte(sec))
			_, _ = h.Write([]byte(timestamp))
			_, _ = h.Write(body)
			candidates = append(candidates, base64.StdEncoding.EncodeToString(h.Sum(nil)))
		}
		h := hmac.New(sha256.New, []byte(sec))
		_, _ = h.Write(body)
		candidates = append(candidates, base64.StdEncoding.EncodeToString(h.Sum(nil)))
		for _, want := range candidates {
			if hmac.Equal([]byte(want), []byte(signature)) {
				return true
			}
		}
	}
	return false
}

// MockPaymentProvider supplies a complete, zero-dependency provider for testing and development.
type MockPaymentProvider struct {
	BaseURL      string
	MockStatus   string
	Secret       string
	CreatedOrder *ProviderOrder
}

func NewMockPaymentProvider() *MockPaymentProvider {
	return &MockPaymentProvider{
		BaseURL:    "https://sandbox.cashfree.com",
		MockStatus: "PAID",
		Secret:     "mock-cashfree-secret",
	}
}

func (m *MockPaymentProvider) CreateOrder(ctx context.Context, bookingID string, userID int64, amount float64, currency string, customerEmail string, customerPhone string) (*ProviderOrder, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if currency == "" {
		currency = "INR"
	}
	cleanID := strings.ReplaceAll(bookingID, "-", "")
	if len(cleanID) > 16 {
		cleanID = cleanID[:16]
	}
	now := time.Now().Unix()
	orderID := fmt.Sprintf("cf_mock_%s_%d", cleanID, now)
	sessionID := fmt.Sprintf("session_mock_%s_%d", cleanID, now)
	checkoutURL := fmt.Sprintf("%s/mock-checkout?payment_session_id=%s", m.BaseURL, sessionID)

	order := &ProviderOrder{
		OrderID:       orderID,
		SessionID:     sessionID,
		CheckoutURL:   checkoutURL,
		Amount:        amount,
		Currency:      currency,
		Status:        "ACTIVE",
		CustomerEmail: customerEmail,
		CustomerPhone: customerPhone,
	}
	m.CreatedOrder = order
	return order, nil
}

func (m *MockPaymentProvider) GetOrderStatus(ctx context.Context, orderID string) (*ProviderOrderStatus, error) {
	status := m.MockStatus
	if status == "" {
		status = "PAID"
	}
	return &ProviderOrderStatus{
		OrderID:  orderID,
		Status:   status,
		Currency: "INR",
	}, nil
}

func (m *MockPaymentProvider) Refund(ctx context.Context, orderID, refundID string, amount float64, currency, reason string) (*ProviderRefund, error) {
	return &ProviderRefund{
		RefundID: refundID,
		Status:   "SUCCESS",
	}, nil
}

func (m *MockPaymentProvider) VerifyWebhook(body []byte, signature, timestamp string) bool {
	if signature == "mock-valid-signature" || signature == "test-sig" {
		return true
	}
	if m.Secret == "" {
		return true
	}
	candidates := make([]string, 0, 2)
	if strings.TrimSpace(timestamp) != "" {
		h := hmac.New(sha256.New, []byte(m.Secret))
		_, _ = h.Write([]byte(timestamp))
		_, _ = h.Write(body)
		candidates = append(candidates, base64.StdEncoding.EncodeToString(h.Sum(nil)))
	}
	h := hmac.New(sha256.New, []byte(m.Secret))
	_, _ = h.Write(body)
	candidates = append(candidates, base64.StdEncoding.EncodeToString(h.Sum(nil)))
	for _, want := range candidates {
		if hmac.Equal([]byte(want), []byte(signature)) {
			return true
		}
	}
	return false
}
