package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
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

func TestAdminCouponAcceptsStartDateAndEndDate(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}

	// 1. JSON test
	jsonBody := `{"code":"TESTJSON","discountType":"PERCENT","discountValue":10,"start_date":"2026-09-01T00:00:00Z","end_date":"2027-12-31T23:59:59Z"}`
	reqJSON := httptest.NewRequest("POST", "/api/v1/admin/coupons", strings.NewReader(jsonBody))
	reqJSON.Header.Set("Content-Type", "application/json")
	wJSON := httptest.NewRecorder()

	decodedJSON, _, ok := h.decodeCouponReq(wJSON, reqJSON)
	if !ok {
		t.Fatalf("failed to decode JSON coupon request: %s", wJSON.Body.String())
	}
	if decodedJSON.ValidFrom == nil || *decodedJSON.ValidFrom != "2026-09-01T00:00:00Z" {
		t.Errorf("expected ValidFrom to be 2026-09-01T00:00:00Z, got %v", decodedJSON.ValidFrom)
	}
	if decodedJSON.ValidUntil == nil || *decodedJSON.ValidUntil != "2027-12-31T23:59:59Z" {
		t.Errorf("expected ValidUntil to be 2027-12-31T23:59:59Z, got %v", decodedJSON.ValidUntil)
	}

	// 2. Multipart form test
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("code", "TESTFORM")
	_ = writer.WriteField("discountType", "PERCENT")
	_ = writer.WriteField("discountValue", "10")
	_ = writer.WriteField("start_date", "2026-09-01T00:00:00Z")
	_ = writer.WriteField("end_date", "2027-12-31T23:59:59Z")
	_ = writer.Close()

	reqForm := httptest.NewRequest("POST", "/api/v1/admin/coupons", &buf)
	reqForm.Header.Set("Content-Type", writer.FormDataContentType())
	wForm := httptest.NewRecorder()

	decodedForm, _, ok := h.decodeCouponReq(wForm, reqForm)
	if !ok {
		t.Fatalf("failed to decode form coupon request: %s", wForm.Body.String())
	}
	if decodedForm.ValidFrom == nil || *decodedForm.ValidFrom != "2026-09-01T00:00:00Z" {
		t.Errorf("expected ValidFrom from form to be 2026-09-01T00:00:00Z, got %v", decodedForm.ValidFrom)
	}
	if decodedForm.ValidUntil == nil || *decodedForm.ValidUntil != "2027-12-31T23:59:59Z" {
		t.Errorf("expected ValidUntil from form to be 2027-12-31T23:59:59Z, got %v", decodedForm.ValidUntil)
	}
}
