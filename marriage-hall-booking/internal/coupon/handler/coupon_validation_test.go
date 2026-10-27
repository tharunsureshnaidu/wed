package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestValidateCodeRejectsMoreThanTwoCoupons(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	userToken, _ := signer.Generate("customer@example.com", "10", domain.RoleCustomer)

	payload := map[string]any{
		"facilityId": "a0000000-0000-0000-0000-000000000001",
		"amount":     50000.0,
		"codes":      []string{"COUPON1", "COUPON2", "COUPON3"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/api/v1/coupons/validate", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := rr.Body.String()
	if !strings.Contains(resp, "COUPON_LIMIT_EXCEEDED") {
		t.Errorf("expected COUPON_LIMIT_EXCEEDED errorCode, got: %s", resp)
	}
	if !strings.Contains(resp, "Maximum 2 coupons can be applied per booking") {
		t.Errorf("expected 'Maximum 2 coupons can be applied per booking' message, got: %s", resp)
	}
}

func TestValidateCodeRequiresFacilityID(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	userToken, _ := signer.Generate("customer@example.com", "10", domain.RoleCustomer)

	payload := map[string]any{
		"amount": 50000.0,
		"codes":  []string{"COUPON1"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/api/v1/coupons/validate", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := rr.Body.String()
	if !strings.Contains(resp, "facilityId is required") {
		t.Errorf("expected 'facilityId is required', got: %s", resp)
	}
}

func TestVendorCouponCreationRequiresFacilityID(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	vendorToken, _ := signer.Generate("vendor@example.com", "20", domain.RoleHallOwner)

	payload := map[string]any{
		"code":          "MYDISCOUNT",
		"discountType":  "PERCENT",
		"discountValue": 10.0,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/api/v1/coupons", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+vendorToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := rr.Body.String()
	if !strings.Contains(strings.ToLower(resp), "facilityid is required") {
		t.Errorf("expected 'FacilityId is required', got: %s", resp)
	}
}
