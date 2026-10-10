package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

type mockService struct {
	items []dto.RecommendationItem
	total int64
	err   error
}

func (m *mockService) GetRecommendations(ctx context.Context, params dto.RecommendationParams) (*dto.RecommendationData, string, error) {
	if m.err != nil {
		return nil, "", m.err
	}
	totalPages := 0
	if params.Size > 0 && m.total > 0 {
		totalPages = int((m.total + int64(params.Size) - 1) / int64(params.Size))
	}
	data := &dto.RecommendationData{
		Items: m.items,
		Pagination: dto.Pagination{
			Page:       params.Page,
			Size:       params.Size,
			TotalItems: m.total,
			TotalPages: totalPages,
		},
	}
	if len(m.items) == 0 {
		return data, "No recommendations found in this area", nil
	}
	return data, "Recommendations fetched successfully", nil
}

func TestValidationErrors(t *testing.T) {
	h := New(&mockService{})
	mux := http.NewServeMux()
	h.Register(mux)

	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "Missing latitude",
			query:      "?lng=77.5946&type=ALL",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Invalid latitude",
		},
		{
			name:       "Out of bounds latitude",
			query:      "?lat=999&lng=77.5946&type=ALL",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Invalid latitude",
		},
		{
			name:       "Missing longitude",
			query:      "?lat=12.9716&type=ALL",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Invalid longitude",
		},
		{
			name:       "Out of bounds longitude",
			query:      "?lat=12.9716&lng=-200&type=ALL",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Invalid longitude",
		},
		{
			name:       "Missing venue type",
			query:      "?lat=12.9716&lng=77.5946",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Invalid venue type. Allowed values: ALL, HALL, HOTEL",
		},
		{
			name:       "Invalid venue type RESTAURANT",
			query:      "?lat=12.9716&lng=77.5946&type=RESTAURANT",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Invalid venue type. Allowed values: ALL, HALL, HOTEL",
		},
		{
			name:       "Negative radiusKm",
			query:      "?lat=12.9716&lng=77.5946&type=ALL&radiusKm=-10",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Invalid radiusKm",
		},
		{
			name:       "Excessive radiusKm",
			query:      "?lat=12.9716&lng=77.5946&type=ALL&radiusKm=600",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Invalid radiusKm",
		},
		{
			name:       "Invalid page -1",
			query:      "?lat=12.9716&lng=77.5946&type=ALL&page=-1",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Invalid page number",
		},
		{
			name:       "Invalid size negative",
			query:      "?lat=12.9716&lng=77.5946&type=ALL&size=-5",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Invalid page size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/public/recommendations"+tt.query, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			var env response.Envelope
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if env.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", env.Message, tt.wantMsg)
			}
		})
	}
}

func TestSuccessPublicNoToken(t *testing.T) {
	img := "https://example.com/cover.jpg"
	mock := &mockService{
		items: []dto.RecommendationItem{
			{
				ID:           "hall-1",
				Type:         "HALL",
				Name:         "Royal Palace Hall",
				Rating:       4.9,
				TotalReviews: 120,
				DistanceKm:   4.5,
				Latitude:     12.9750,
				Longitude:    77.5950,
				Location:     "Bangalore",
				Image:        &img,
			},
			{
				ID:           "hotel-1",
				Type:         "HOTEL",
				Name:         "Grand Royal Hotel",
				Rating:       4.8,
				TotalReviews: 95,
				DistanceKm:   6.2,
				Latitude:     12.9650,
				Longitude:    77.6010,
				Location:     "Bangalore",
				Image:        &img,
			},
		},
		total: 2,
	}

	h := New(mock)
	mux := http.NewServeMux()
	h.Register(mux)

	// Call WITHOUT Authorization header to prove public access
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/recommendations?lat=12.9716&lng=77.5946&type=ALL&radiusKm=50", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var env struct {
		Success bool                   `json:"success"`
		Message string                 `json:"message"`
		Data    dto.RecommendationData `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !env.Success {
		t.Errorf("expected success = true")
	}
	if env.Message != "Recommendations fetched successfully" {
		t.Errorf("message = %q, want 'Recommendations fetched successfully'", env.Message)
	}
	if len(env.Data.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(env.Data.Items))
	}
	if env.Data.Items[0].Type != "HALL" || env.Data.Items[1].Type != "HOTEL" {
		t.Errorf("unexpected types in items: %v, %v", env.Data.Items[0].Type, env.Data.Items[1].Type)
	}
	if env.Data.Pagination.TotalItems != 2 || env.Data.Pagination.TotalPages != 1 {
		t.Errorf("pagination = %+v, want TotalItems=2, TotalPages=1", env.Data.Pagination)
	}
}

func TestEmptyResults(t *testing.T) {
	mock := &mockService{
		items: []dto.RecommendationItem{},
		total: 0,
	}

	h := New(mock)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/recommendations?lat=12.9716&lng=77.5946&type=ALL&radiusKm=5", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var env struct {
		Success bool                   `json:"success"`
		Message string                 `json:"message"`
		Data    dto.RecommendationData `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !env.Success {
		t.Errorf("expected success = true")
	}
	if env.Message != "No recommendations found in this area" {
		t.Errorf("message = %q, want 'No recommendations found in this area'", env.Message)
	}
	if len(env.Data.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(env.Data.Items))
	}
	if env.Data.Pagination.TotalItems != 0 || env.Data.Pagination.TotalPages != 0 {
		t.Errorf("pagination = %+v, want TotalItems=0, TotalPages=0", env.Data.Pagination)
	}
}
