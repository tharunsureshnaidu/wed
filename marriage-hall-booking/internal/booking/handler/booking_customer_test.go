package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestBookingCustomerOnlyValidation(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	customerToken, _ := signer.Generate("customer@example.com", "10", domain.RoleCustomer)
	ownerToken, _ := signer.Generate("owner@example.com", "20", domain.RoleHallOwner)
	adminToken, _ := signer.Generate("admin@example.com", "30", domain.RoleAdmin)
	superAdminToken, _ := signer.Generate("super@example.com", "40", domain.RoleSuperAdmin)
	adminWithCustomerToken, _ := signer.Generate("admincust@example.com", "50", domain.RoleAdmin, domain.RoleCustomer)
	ownerWithCustomerToken, _ := signer.Generate("ownercust@example.com", "60", domain.RoleHallOwner, domain.RoleCustomer)

	endpoints := []string{
		"/api/v1/bookings/hotels",
		"/api/v1/bookings/halls",
		"/api/v1/bookings/venues",
		"/api/v1/bookings",
	}

	for _, ep := range endpoints {
		t.Run("Rejects Hall Owner on "+ep, func(t *testing.T) {
			req := httptest.NewRequest("POST", ep, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+ownerToken)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden for hall owner, got %d: %s", rr.Code, rr.Body.String())
			}
			var body map[string]any
			_ = json.Unmarshal(rr.Body.Bytes(), &body)
			if body["errorCode"] != "CUSTOMER_ONLY" {
				t.Errorf("expected CUSTOMER_ONLY error code, got %v", body["errorCode"])
			}
		})

		t.Run("Rejects Admin on "+ep, func(t *testing.T) {
			req := httptest.NewRequest("POST", ep, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+adminToken)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden for admin, got %d: %s", rr.Code, rr.Body.String())
			}
			var body map[string]any
			_ = json.Unmarshal(rr.Body.Bytes(), &body)
			if body["errorCode"] != "CUSTOMER_ONLY" {
				t.Errorf("expected CUSTOMER_ONLY error code, got %v", body["errorCode"])
			}
		})

		t.Run("Rejects Super Admin on "+ep, func(t *testing.T) {
			req := httptest.NewRequest("POST", ep, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+superAdminToken)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden for super admin, got %d: %s", rr.Code, rr.Body.String())
			}
		})

		t.Run("Rejects Admin Even With Customer Role on "+ep, func(t *testing.T) {
			req := httptest.NewRequest("POST", ep, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+adminWithCustomerToken)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden, got %d: %s", rr.Code, rr.Body.String())
			}
		})

		t.Run("Rejects Hall Owner Even With Customer Role on "+ep, func(t *testing.T) {
			req := httptest.NewRequest("POST", ep, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+ownerWithCustomerToken)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden, got %d: %s", rr.Code, rr.Body.String())
			}
		})

		t.Run("Allows Customer on "+ep, func(t *testing.T) {
			req := httptest.NewRequest("POST", ep, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+customerToken)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			// Customer passes role check; empty JSON fails with 400 Bad Request, NOT 403 Forbidden
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 Bad Request (validation failure, not forbidden), got %d: %s", rr.Code, rr.Body.String())
			}
			var body map[string]any
			_ = json.Unmarshal(rr.Body.Bytes(), &body)
			if body["errorCode"] == "CUSTOMER_ONLY" {
				t.Errorf("customer should not be rejected with CUSTOMER_ONLY")
			}
		})

		t.Run("Rejects Anonymous on "+ep, func(t *testing.T) {
			req := httptest.NewRequest("POST", ep, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 Unauthorized for anonymous, got %d", rr.Code)
			}
		})
	}
}
