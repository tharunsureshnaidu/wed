package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestReferenceRoutesRegistered(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	// Check reference route
	req := httptest.NewRequest("GET", "/api/v1/bookings/reference/BK-A1B2C3D4E5", nil)
	_, pattern := mux.Handler(req)
	if pattern != "GET /api/v1/bookings/reference/{ref}" {
		t.Errorf("expected pattern 'GET /api/v1/bookings/reference/{ref}', got %q", pattern)
	}

	// Check standard ID route
	req2 := httptest.NewRequest("GET", "/api/v1/bookings/BK-A1B2C3D4E5", nil)
	_, pattern2 := mux.Handler(req2)
	if pattern2 != "GET /api/v1/bookings/{id}" {
		t.Errorf("expected pattern 'GET /api/v1/bookings/{id}', got %q", pattern2)
	}
}

func TestGetBookingAllowsNonUUIDReferenceFormat(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	adminToken, _ := signer.Generate("admin@example.com", "1", domain.RoleAdmin)

	// Calling GET /api/v1/bookings/BK-A1B2C3D4E5 with nil svc should not fail on UUID validation.
	// It reaches the service call and will panic or error on nil svc (not return 400 VALIDATION_ERROR "Invalid booking id").
	defer func() {
		// A nil pointer dereference on h.svc is expected and proves it passed UUID validation
		if r := recover(); r != nil {
			// passed UUID validation to svc.Get
		}
	}()

	req := httptest.NewRequest("GET", "/api/v1/bookings/BK-A1B2C3D4E5", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// If it didn't panic, it should NOT be 400 VALIDATION_ERROR
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "VALIDATION_ERROR") {
		t.Errorf("GET /api/v1/bookings/{id} rejected reference ID with validation error: %s", rr.Body.String())
	}
}
