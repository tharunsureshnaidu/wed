package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestFavouriteToggleValidation(t *testing.T) {
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	token, _ := signer.Generate("customer@example.com", "1", "ROLE_CUSTOMER")

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	validUUID := "72e046ac-2caa-4760-a9a1-92e19443d988"

	tests := []struct {
		name          string
		body          string
		token         string
		wantStatus    int
		wantCode      string
		wantMsgSubstr string
	}{
		{
			name:       "Missing auth token",
			body:       `{"entityId":"` + validUUID + `","type":"HALL","favorite":true}`,
			token:      "",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name:          "Missing entityId",
			body:          `{"type":"HALL","favorite":true}`,
			token:         token,
			wantStatus:    http.StatusBadRequest,
			wantCode:      "VALIDATION_ERROR",
			wantMsgSubstr: "entityId is required",
		},
		{
			name:          "Empty entityId",
			body:          `{"entityId":"   ","type":"HALL","favorite":true}`,
			token:         token,
			wantStatus:    http.StatusBadRequest,
			wantCode:      "VALIDATION_ERROR",
			wantMsgSubstr: "entityId is required",
		},
		{
			name:          "Invalid UUID entityId",
			body:          `{"entityId":"not-a-valid-uuid","type":"HALL","favorite":true}`,
			token:         token,
			wantStatus:    http.StatusBadRequest,
			wantCode:      "VALIDATION_ERROR",
			wantMsgSubstr: "Invalid entity id",
		},
		{
			name:          "Missing type",
			body:          `{"entityId":"` + validUUID + `","favorite":true}`,
			token:         token,
			wantStatus:    http.StatusBadRequest,
			wantCode:      "VALIDATION_ERROR",
			wantMsgSubstr: "type is required",
		},
		{
			name:          "Empty type",
			body:          `{"entityId":"` + validUUID + `","type":"  ","favorite":true}`,
			token:         token,
			wantStatus:    http.StatusBadRequest,
			wantCode:      "VALIDATION_ERROR",
			wantMsgSubstr: "type is required",
		},
		{
			name:          "Invalid type RESTAURANT",
			body:          `{"entityId":"` + validUUID + `","type":"RESTAURANT","favorite":true}`,
			token:         token,
			wantStatus:    http.StatusBadRequest,
			wantCode:      "VALIDATION_ERROR",
			wantMsgSubstr: "Invalid type. Supported types are HALL and HOTEL.",
		},
		{
			name:          "Missing favorite field",
			body:          `{"entityId":"` + validUUID + `","type":"HALL"}`,
			token:         token,
			wantStatus:    http.StatusBadRequest,
			wantCode:      "VALIDATION_ERROR",
			wantMsgSubstr: "favorite is required",
		},
		{
			name:       "Invalid favorite field string instead of bool",
			body:       `{"entityId":"` + validUUID + `","type":"HALL","favorite":"true"}`,
			token:      token,
			wantStatus: http.StatusBadRequest,
			wantCode:   "MALFORMED_JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/users/me/favourites", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d. Body: %s", tt.wantStatus, rr.Code, rr.Body.String())
			}

			var resp struct {
				Success   bool   `json:"success"`
				Message   string `json:"message"`
				ErrorCode string `json:"errorCode"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if tt.wantCode != "" && resp.ErrorCode != tt.wantCode {
				t.Errorf("expected errorCode %q, got %q", tt.wantCode, resp.ErrorCode)
			}
			if tt.wantMsgSubstr != "" && !bytes.Contains([]byte(resp.Message), []byte(tt.wantMsgSubstr)) {
				t.Errorf("expected message containing %q, got %q", tt.wantMsgSubstr, resp.Message)
			}
		})
	}
}

func TestListFavouritesTypeQueryValidation(t *testing.T) {
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	token, _ := signer.Generate("customer@example.com", "1", "ROLE_CUSTOMER")

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	for _, invalidType := range []string{"RESTAURANT", "BAR", "INVALID", "123"} {
		t.Run("invalid type "+invalidType, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/users/me/favourites?type="+invalidType, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 Bad Request, got %d", rr.Code)
			}

			var resp struct {
				Success   bool   `json:"success"`
				Message   string `json:"message"`
				ErrorCode string `json:"errorCode"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if resp.ErrorCode != "VALIDATION_ERROR" {
				t.Errorf("expected errorCode 'VALIDATION_ERROR', got %q", resp.ErrorCode)
			}
			if resp.Message != "Invalid type. Supported types are HALL and HOTEL." {
				t.Errorf("expected message 'Invalid type. Supported types are HALL and HOTEL.', got %q", resp.Message)
			}
		})
	}
}

func TestFavouriteToggleDataSerialization(t *testing.T) {
	data := favouriteToggleData{
		EntityID: "72e046ac-2caa-4760-a9a1-92e19443d988",
		Type:     "HALL",
		Favorite: true,
	}

	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed["entityId"] != "72e046ac-2caa-4760-a9a1-92e19443d988" {
		t.Errorf("expected entityId, got %v", parsed["entityId"])
	}
	if parsed["type"] != "HALL" {
		t.Errorf("expected type HALL, got %v", parsed["type"])
	}
	if parsed["favorite"] != true {
		t.Errorf("expected favorite true, got %v", parsed["favorite"])
	}
}
