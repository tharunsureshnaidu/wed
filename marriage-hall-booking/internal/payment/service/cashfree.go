package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PaymentProvider is the provider contract used by the booking payment flow.
type PaymentProvider interface {
	CreateOrder(ctx context.Context, bookingID string, userID int64, amount float64, currency string, customerEmail string, customerPhone string) (*ProviderOrder, error)
	GetOrderStatus(ctx context.Context, orderID string) (*ProviderOrderStatus, error)
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

type CashfreeClient struct {
	baseURL       string
	apiKey        string
	apiSecret     string
	webhookSecret string
	httpClient    *http.Client
}

func NewCashfreeClient(env, apiKey, apiSecret, webhookSecret, baseURL string) *CashfreeClient {
	if strings.TrimSpace(baseURL) == "" {
		if strings.EqualFold(env, "production") {
			baseURL = "https://api.cashfree.com"
		} else {
			baseURL = "https://sandbox.cashfree.com"
		}
	}
	return &CashfreeClient{
		baseURL:       strings.TrimRight(baseURL, "/"),
		apiKey:        strings.TrimSpace(apiKey),
		apiSecret:     strings.TrimSpace(apiSecret),
		webhookSecret: strings.TrimSpace(webhookSecret),
		httpClient:    &http.Client{Timeout: 15 * time.Second},
	}
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

	payload := map[string]any{
		"order_id":       fmt.Sprintf("booking-%s-%d", bookingID, time.Now().UnixNano()),
		"order_amount":   amount,
		"order_currency": currency,
		"customer_details": map[string]any{
			"customer_id":    fmt.Sprintf("user-%d", userID),
			"customer_email": customerEmail,
			"customer_phone": customerPhone,
		},
		"order_meta": map[string]any{
			"return_url":      "/payments/return",
			"notify_url":      "/api/v1/payments/webhook",
			"payment_methods": []string{"UPI", "CARD", "NETBANKING"},
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
	req.Header.Set("x-api-version", "2022-09-01")

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
		return nil, fmt.Errorf("cashfree: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		CFOrderID        string   `json:"cf_order_id"`
		OrderID          string   `json:"order_id"`
		OrderAmount      float64  `json:"order_amount"`
		OrderCurrency    string   `json:"order_currency"`
		PaymentSessionID string   `json:"payment_session_id"`
		PaymentMethods   []string `json:"payment_methods"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	if result.OrderID == "" {
		result.OrderID = result.CFOrderID
	}
	return &ProviderOrder{
		OrderID:        result.OrderID,
		SessionID:      result.PaymentSessionID,
		Amount:         result.OrderAmount,
		Currency:       result.OrderCurrency,
		PaymentMethods: result.PaymentMethods,
	}, nil
}

func (c *CashfreeClient) GetOrderStatus(ctx context.Context, orderID string) (*ProviderOrderStatus, error) {
	if c == nil || c.apiKey == "" || c.apiSecret == "" {
		return nil, fmt.Errorf("cashfree credentials not configured")
	}
	if strings.TrimSpace(orderID) == "" {
		return nil, fmt.Errorf("order id required")
	}
	url := fmt.Sprintf("%s/pg/orders/%s", c.baseURL, orderID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-client-id", c.apiKey)
	req.Header.Set("x-client-secret", c.apiSecret)
	req.Header.Set("x-api-version", "2022-09-01")

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

func (c *CashfreeClient) VerifyWebhook(body []byte, signature string) bool {
	if c == nil || strings.TrimSpace(c.webhookSecret) == "" {
		return false
	}
	if strings.TrimSpace(signature) == "" {
		return false
	}
	h := hmac.New(sha256.New, []byte(c.webhookSecret))
	_, _ = h.Write(body)
	want := hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(want), []byte(signature))
}
