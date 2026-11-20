package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSlotForOptionalTimes(t *testing.T) {
	tests := []struct {
		start, end string
		want       string
	}{
		{"", "", "FULL_DAY"},
		{"10:00", "18:00", "FULL_DAY"},
		{"08:00", "12:00", "MORNING"},
		{"13:00", "17:00", "EVENING"},
		{"10:00", "", "MORNING"},
		{"15:00", "", "EVENING"},
		{"", "11:00", "MORNING"},
		{"", "16:00", "EVENING"},
		{"20:00", "02:00", "FULL_DAY"}, // crosses midnight
	}

	for _, tt := range tests {
		got := slotFor(tt.start, tt.end)
		if got != tt.want {
			t.Errorf("slotFor(%q, %q) = %q, want %q", tt.start, tt.end, got, tt.want)
		}
	}
}

func TestVenueReqOptionalFieldsDecoding(t *testing.T) {
	// Payload with all optional fields omitted
	rawJSON := `{
		"venueId": "91cb728c-ee13-4ef9-bb99-923cb8d82d49",
		"startDate": "2027-06-25",
		"endDate": "2027-06-26"
	}`

	var req venueReq
	if err := json.Unmarshal([]byte(rawJSON), &req); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if req.VenueID != "91cb728c-ee13-4ef9-bb99-923cb8d82d49" {
		t.Errorf("want venueId, got %v", req.VenueID)
	}
	if req.StartTime != "" || req.EndTime != "" {
		t.Errorf("startTime and endTime should be empty, got %q, %q", req.StartTime, req.EndTime)
	}
	if req.SlotType != "" {
		t.Errorf("slotType should be empty, got %q", req.SlotType)
	}
	if req.RoomCount != nil {
		t.Errorf("roomCount should be nil, got %v", req.RoomCount)
	}
}

func TestVenueReqWithOptionalKeysProvided(t *testing.T) {
	rawJSON := `{
		"venueId": "91cb728c-ee13-4ef9-bb99-923cb8d82d49",
		"startDate": "2027-06-25",
		"endDate": "2027-06-26",
		"startTime": "10:00",
		"endTime": "18:00",
		"slotType": "FULL_DAY",
		"roomCount": 2
	}`

	var req venueReq
	if err := json.Unmarshal([]byte(rawJSON), &req); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if req.StartTime != "10:00" {
		t.Errorf("want startTime 10:00, got %q", req.StartTime)
	}
	if req.EndTime != "18:00" {
		t.Errorf("want endTime 18:00, got %q", req.EndTime)
	}
	if req.SlotType != "FULL_DAY" {
		t.Errorf("want slotType FULL_DAY, got %q", req.SlotType)
	}
	if req.RoomCount == nil || *req.RoomCount != 2 {
		t.Errorf("want roomCount 2, got %v", req.RoomCount)
	}
}

func TestRoutesRegistered(t *testing.T) {
	h := &Handler{}
	mux := http.NewServeMux()
	h.Register(mux)

	// Verify that the router recognizes all paths (venues, bookings, halls)
	endpoints := []string{
		"/api/v1/bookings/venues",
		"/api/v1/bookings",
		"/api/v1/bookings/halls",
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest("POST", ep, strings.NewReader(`{}`))
		_, pattern := mux.Handler(req)
		if pattern == "" || !strings.Contains(pattern, ep) {
			t.Errorf("route %s not registered in mux, got pattern %q", ep, pattern)
		}
	}
}
