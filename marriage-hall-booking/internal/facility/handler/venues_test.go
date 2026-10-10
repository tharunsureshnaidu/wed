package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/config"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
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

func TestListVenuesWithEventType(t *testing.T) {
	mux := http.NewServeMux()
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)

	_ = os.Chdir("../../../")
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Skipf("Skipping live database test: unable to connect: %v", err)
	}
	defer pool.Close()

	repo := repository.New(pool)
	h := New(repo, signer, nil)
	h.Register(mux)

	// Verify route handles request successfully with eventType parameter
	req := httptest.NewRequest("GET", "/api/v1/venues?type=HOTEL&eventType=WEDDING&page=0&size=10", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Content []repository.VenueResponse `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !body.Success {
		t.Errorf("expected success true, got false")
	}
}

