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

func TestVenueBookingRejectsMoreThanTwoCoupons(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	customerToken, _ := signer.Generate("customer@example.com", "10", domain.RoleCustomer)

	payload := map[string]any{
		"venueId":     "a0000000-0000-0000-0000-000000000001",
		"startDate":   "2026-11-01",
		"endDate":     "2026-11-01",
		"couponCodes": []string{"COUPON1", "COUPON2", "COUPON3"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/api/v1/bookings/halls", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+customerToken)
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

func TestHotelBookingRejectsMoreThanTwoCoupons(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	customerToken, _ := signer.Generate("customer@example.com", "10", domain.RoleCustomer)

	payload := map[string]any{
		"hotelId":  "a0000000-0000-0000-0000-000000000001",
		"checkIn":  "2026-11-01",
		"checkOut": "2026-11-02",
		"rooms": []map[string]any{
			{"roomTypeId": "a0000000-0000-0000-0000-000000000002", "quantity": 1},
		},
		"couponCodes": []string{"COUPON1", "COUPON2", "COUPON3"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/api/v1/bookings/hotels", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+customerToken)
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
