package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	couponhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/coupon/handler"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/config"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestAdminCouponSingleActiveLimit(t *testing.T) {
	_ = os.Chdir("../../../")
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Skipf("Skipping live database test: unable to connect: %v", err)
	}
	defer pool.Close()

	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	adminToken, err := signer.Generate("admin@example.com", "1", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}

	h := couponhandler.New(pool, signer)
	mux := http.NewServeMux()
	h.Register(mux)

	// Clean up any existing admin test coupons before running test
	_, _ = pool.Exec(ctx, `DELETE FROM coupons WHERE code LIKE 'TEST_LIMIT_%'`)
	// Ensure any other active platform coupon is soft-deleted for test isolation
	_, _ = pool.Exec(ctx, `UPDATE coupons SET is_deleted = TRUE WHERE facility_type IS NOT NULL AND is_deleted = FALSE`)

	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM coupons WHERE code LIKE 'TEST_LIMIT_%'`)
	}()

	// 1. Create first admin coupon - should SUCCEED
	body1 := map[string]any{
		"code":          "TEST_LIMIT_1",
		"description":   "First test admin coupon",
		"discountType":  "PERCENT",
		"discountValue": 10.0,
		"appliesTo":     "ALL",
		"isActive":      true,
	}
	b1, _ := json.Marshal(body1)
	req1 := httptest.NewRequest("POST", "/api/v1/admin/coupons", bytes.NewReader(b1))
	req1.Header.Set("Authorization", "Bearer "+adminToken)
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	mux.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for first coupon, got %d: %s", rr1.Code, rr1.Body.String())
	}

	var resp1 struct {
		Success bool `json:"success"`
		Data    struct {
			ID   string `json:"id"`
			Code string `json:"code"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rr1.Body.Bytes(), &resp1)
	coupon1ID := resp1.Data.ID

	// 2. Attempt to create second admin coupon while first is active - should be REJECTED (400)
	body2 := map[string]any{
		"code":          "TEST_LIMIT_2",
		"description":   "Second test admin coupon",
		"discountType":  "FLAT",
		"discountValue": 500.0,
		"appliesTo":     "ALL",
		"isActive":      true,
	}
	b2, _ := json.Marshal(body2)
	req2 := httptest.NewRequest("POST", "/api/v1/admin/coupons", bytes.NewReader(b2))
	req2.Header.Set("Authorization", "Bearer "+adminToken)
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for second coupon, got %d: %s", rr2.Code, rr2.Body.String())
	}

	var errResp struct {
		Success   bool   `json:"success"`
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errResp.Success {
		t.Errorf("expected success to be false, got true")
	}
	if errResp.ErrorCode != "COUPON_LIMIT_EXCEEDED" {
		t.Errorf("expected ErrorCode 'COUPON_LIMIT_EXCEEDED', got %q", errResp.ErrorCode)
	}
	if errResp.Message == "" {
		t.Errorf("expected non-empty rejection message")
	}

	// 3. Delete the first coupon
	reqDel := httptest.NewRequest("DELETE", "/api/v1/admin/coupons/"+coupon1ID, nil)
	reqDel.Header.Set("Authorization", "Bearer "+adminToken)
	rrDel := httptest.NewRecorder()
	mux.ServeHTTP(rrDel, reqDel)

	if rrDel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on coupon delete, got %d: %s", rrDel.Code, rrDel.Body.String())
	}

	// 4. Now create second coupon again - should SUCCEED
	req3 := httptest.NewRequest("POST", "/api/v1/admin/coupons", bytes.NewReader(b2))
	req3.Header.Set("Authorization", "Bearer "+adminToken)
	req3.Header.Set("Content-Type", "application/json")
	rr3 := httptest.NewRecorder()
	mux.ServeHTTP(rr3, req3)

	if rr3.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for new coupon after deleting previous, got %d: %s", rr3.Code, rr3.Body.String())
	}
}
