package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestGetMyEntityReviewValidation(t *testing.T) {
	signer, err := jwt.NewSigner(testSecret, 24*time.Hour)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	h := New(nil, signer)
	mux := http.NewServeMux()
	h.Register(mux)

	custToken, err := signer.Generate("customer@example.com", "42", domain.RoleCustomer)
	if err != nil {
		t.Fatalf("failed to sign customer token: %v", err)
	}

	t.Run("Unauthorized request without token returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/reviews/me?type=hotel&id=d7f7c967-4ccb-4374-8c05-b027a7312427", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("got status %d, want 401 Unauthorized", rr.Code)
		}
	})

	t.Run("Missing type parameter returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/reviews/me?id=d7f7c967-4ccb-4374-8c05-b027a7312427", nil)
		req.Header.Set("Authorization", "Bearer "+custToken)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400 Bad Request", rr.Code)
		}

		var res map[string]any
		json.Unmarshal(rr.Body.Bytes(), &res)
		if res["errorCode"] != "VALIDATION_ERROR" {
			t.Errorf("got errorCode %v, want VALIDATION_ERROR", res["errorCode"])
		}
	})

	t.Run("Invalid type parameter returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/reviews/me?type=resort&id=d7f7c967-4ccb-4374-8c05-b027a7312427", nil)
		req.Header.Set("Authorization", "Bearer "+custToken)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400 Bad Request", rr.Code)
		}
	})

	t.Run("Missing id parameter returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/reviews/me?type=hotel", nil)
		req.Header.Set("Authorization", "Bearer "+custToken)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400 Bad Request", rr.Code)
		}
	})

	t.Run("Invalid UUID id parameter returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/reviews/me?type=hall&id=invalid-uuid", nil)
		req.Header.Set("Authorization", "Bearer "+custToken)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400 Bad Request", rr.Code)
		}
	})
}
