package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestListVenuesTypeValidation(t *testing.T) {
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	for _, invalidType := range []string{"INVALID", "RESTAURANT", "BAR", "CLUB", "123"} {
		t.Run("invalid type "+invalidType, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/venues?type="+invalidType, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 Bad Request for type %q, got %d", invalidType, rr.Code)
			}

			var body struct {
				Success   bool   `json:"success"`
				Message   string `json:"message"`
				ErrorCode string `json:"errorCode"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("failed to parse json response: %v", err)
			}

			if body.Success {
				t.Errorf("expected success false, got true")
			}
			if body.Message != "Invalid type. Supported types are HALL and HOTEL." {
				t.Errorf("expected message 'Invalid type. Supported types are HALL and HOTEL.', got %q", body.Message)
			}
			if body.ErrorCode != "VALIDATION_ERROR" {
				t.Errorf("expected errorCode 'VALIDATION_ERROR', got %q", body.ErrorCode)
			}
		})
	}
}
