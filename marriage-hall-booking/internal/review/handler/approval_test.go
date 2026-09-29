package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

const testSecret = "Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3"

func TestReviewApprovalRoutesRegistration(t *testing.T) {
	signer, err := jwt.NewSigner(testSecret, 24*time.Hour)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	h := New(nil, signer)
	mux := http.NewServeMux()
	h.Register(mux)
	h.RegisterAdmin(mux)

	// Admin token
	adminToken, err := signer.Generate("admin@example.com", "1", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("failed to sign admin token: %v", err)
	}

	// Customer token
	custToken, err := signer.Generate("customer@example.com", "2", domain.RoleCustomer)
	if err != nil {
		t.Fatalf("failed to sign customer token: %v", err)
	}

	t.Run("Unauthorized approval attempt without token returns 401", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/admin/reviews/d7f7c967-4ccb-4374-8c05-b027a7312427/approve", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("got status %d, want 401 Unauthorized", rr.Code)
		}
	})

	t.Run("Non-admin role approval attempt returns 403", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/admin/reviews/d7f7c967-4ccb-4374-8c05-b027a7312427/approve", nil)
		req.Header.Set("Authorization", "Bearer "+custToken)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("got status %d, want 403 Forbidden", rr.Code)
		}
	})

	t.Run("Admin with invalid UUID review ID returns 400 Validation Error", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/admin/reviews/invalid-uuid/approve", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400 Bad Request", rr.Code)
		}
	})

	t.Run("Admin reject invalid UUID returns 400 Validation Error", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/admin/reviews/invalid-uuid/reject", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400 Bad Request", rr.Code)
		}
	})
}

func TestCreateReviewReqValidation(t *testing.T) {
	signer, _ := jwt.NewSigner(testSecret, 24*time.Hour)
	h := New(nil, signer)
	mux := http.NewServeMux()
	h.Register(mux)

	custToken, _ := signer.Generate("customer@example.com", "2", domain.RoleCustomer)

	t.Run("Invalid facility ID returns 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"facilityId": "invalid-uuid",
			"rating":     5,
		})
		req := httptest.NewRequest("POST", "/api/v1/reviews", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+custToken)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400 Bad Request", rr.Code)
		}
	})
}
