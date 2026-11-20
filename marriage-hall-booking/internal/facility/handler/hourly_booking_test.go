package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestHourlyBookingValidation(t *testing.T) {
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	h := &Handler{signer: signer}

	// 1. Backward compatibility: when hourly booking is omitted on hotel
	t.Run("omitted hourly booking produces no errors and not enabled", func(t *testing.T) {
		req := facilityReq{
			Name: "Luxury Hotel",
			Type: TypeHotel,
		}
		enabled, pkgs, errs := req.resolveHourlyBooking()
		if len(errs) != 0 {
			t.Fatalf("expected 0 errors, got: %v", errs)
		}
		if enabled {
			t.Errorf("expected enabled to be false")
		}
		if len(pkgs) != 0 {
			t.Errorf("expected 0 pkgs, got %d", len(pkgs))
		}
	})

	// 2. Backward compatibility: when hourly booking is explicitly disabled
	t.Run("explicitly disabled hourly booking", func(t *testing.T) {
		disabled := false
		req := facilityReq{
			Name: "Luxury Hotel",
			Type: TypeHotel,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &disabled,
			},
		}
		enabled, pkgs, errs := req.resolveHourlyBooking()
		if len(errs) != 0 {
			t.Fatalf("expected 0 errors, got: %v", errs)
		}
		if enabled {
			t.Errorf("expected enabled to be false")
		}
		if len(pkgs) != 0 {
			t.Errorf("expected 0 pkgs, got %d", len(pkgs))
		}
	})

	// 3. Hourly booking rejected on marriage halls
	t.Run("rejects hourly booking for marriage hall", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name: "Royal Palace",
			Type: TypeHall,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &enabledVal,
				Options: []HourlyOptionReq{
					{Hours: 2, Price: 1000},
				},
			},
		}
		_, _, errs := req.resolveHourlyBooking()
		if len(errs) == 0 {
			t.Fatalf("expected error for hall, got 0 errors")
		}
		found := false
		for _, e := range errs {
			if e == "Hourly booking is only available for hotels" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'Hourly booking is only available for hotels', got: %v", errs)
		}
	})

	// 4. Valid single hourly package (flexible: 1 option)
	t.Run("valid 1 hourly package for hotel", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name: "City Hotel",
			Type: TypeHotel,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &enabledVal,
				Options: []HourlyOptionReq{
					{Hours: 2, Price: 500},
				},
			},
		}
		enabled, pkgs, errs := req.resolveHourlyBooking()
		if len(errs) != 0 {
			t.Fatalf("expected 0 errors, got: %v", errs)
		}
		if !enabled {
			t.Errorf("expected enabled to be true")
		}
		if len(pkgs) != 1 {
			t.Fatalf("expected 1 pkg, got %d", len(pkgs))
		}
		if pkgs[0].Hours != 2 || pkgs[0].Price != 500 {
			t.Errorf("expected hours=2, price=500, got hours=%d, price=%f", pkgs[0].Hours, pkgs[0].Price)
		}
	})

	// 5. Valid up to 3 hourly packages with custom pricing (e.g., 2h, 4h, 6h)
	t.Run("valid 3 hourly packages with custom pricing", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name: "Grand Plaza Hotel",
			Type: TypeHotel,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &enabledVal,
				Options: []HourlyOptionReq{
					{Hours: 2, Price: 800},
					{Hours: 4, Price: 1500},
					{Hours: 6, Price: 2100},
				},
			},
		}
		enabled, pkgs, errs := req.resolveHourlyBooking()
		if len(errs) != 0 {
			t.Fatalf("expected 0 errors, got: %v", errs)
		}
		if !enabled {
			t.Errorf("expected enabled to be true")
		}
		if len(pkgs) != 3 {
			t.Fatalf("expected 3 pkgs, got %d", len(pkgs))
		}
		if pkgs[0].Hours != 2 || pkgs[0].Price != 800 {
			t.Errorf("pkg 0 mismatch: %+v", pkgs[0])
		}
		if pkgs[1].Hours != 4 || pkgs[1].Price != 1500 {
			t.Errorf("pkg 1 mismatch: %+v", pkgs[1])
		}
		if pkgs[2].Hours != 6 || pkgs[2].Price != 2100 {
			t.Errorf("pkg 2 mismatch: %+v", pkgs[2])
		}
	})

	// 6. Supports durationHours alias
	t.Run("supports durationHours alias in options", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name: "Comfort Inn",
			Type: TypeHotel,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &enabledVal,
				Options: []HourlyOptionReq{
					{DurationHours: 3, Price: 750},
				},
			},
		}
		enabled, pkgs, errs := req.resolveHourlyBooking()
		if len(errs) != 0 {
			t.Fatalf("expected 0 errors, got: %v", errs)
		}
		if !enabled || len(pkgs) != 1 || pkgs[0].Hours != 3 || pkgs[0].Price != 750 {
			t.Errorf("unexpected pkgs: %+v", pkgs)
		}
	})

	// 7. Supports top-level hourlyBookingOptions
	t.Run("supports top-level hourlyBookingOptions", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name:                 "Comfort Inn",
			Type:                 TypeHotel,
			HourlyBookingEnabled: &enabledVal,
			HourlyBookingOptions: []HourlyOptionReq{
				{Hours: 2, Price: 400},
				{Hours: 4, Price: 700},
			},
		}
		enabled, pkgs, errs := req.resolveHourlyBooking()
		if len(errs) != 0 {
			t.Fatalf("expected 0 errors, got: %v", errs)
		}
		if !enabled || len(pkgs) != 2 {
			t.Errorf("unexpected pkgs: %+v", pkgs)
		}
	})

	// 8. Rejects more than 3 hourly packages
	t.Run("rejects more than 3 hourly packages", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name: "Overloaded Hotel",
			Type: TypeHotel,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &enabledVal,
				Options: []HourlyOptionReq{
					{Hours: 1, Price: 200},
					{Hours: 2, Price: 400},
					{Hours: 4, Price: 800},
					{Hours: 6, Price: 1200},
				},
			},
		}
		_, _, errs := req.resolveHourlyBooking()
		if len(errs) == 0 {
			t.Fatalf("expected validation error for >3 options")
		}
		found := false
		for _, e := range errs {
			if e == "A maximum of 3 hourly booking options are allowed" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'A maximum of 3 hourly booking options are allowed', got: %v", errs)
		}
	})

	// 9. Rejects empty options when enabled is true
	t.Run("rejects enabled with 0 options", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name: "Empty Options Hotel",
			Type: TypeHotel,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &enabledVal,
				Options: []HourlyOptionReq{},
			},
		}
		_, _, errs := req.resolveHourlyBooking()
		if len(errs) == 0 {
			t.Fatalf("expected validation error for empty options")
		}
		found := false
		for _, e := range errs {
			if e == "At least one hourly booking option must be configured when hourly booking is enabled" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error for empty options, got: %v", errs)
		}
	})

	// 10. Rejects negative price
	t.Run("rejects negative price", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name: "Negative Price Hotel",
			Type: TypeHotel,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &enabledVal,
				Options: []HourlyOptionReq{
					{Hours: 2, Price: -50},
				},
			},
		}
		_, _, errs := req.resolveHourlyBooking()
		if len(errs) == 0 {
			t.Fatalf("expected validation error for negative price")
		}
	})

	// 11. Rejects invalid hours (<= 0 or > 24)
	t.Run("rejects invalid hours", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name: "Zero Hours Hotel",
			Type: TypeHotel,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &enabledVal,
				Options: []HourlyOptionReq{
					{Hours: 0, Price: 500},
				},
			},
		}
		_, _, errs := req.resolveHourlyBooking()
		if len(errs) == 0 {
			t.Fatalf("expected validation error for zero hours")
		}
	})

	// 12. Rejects duplicate durations
	t.Run("rejects duplicate durations", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name: "Duplicate Hours Hotel",
			Type: TypeHotel,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &enabledVal,
				Options: []HourlyOptionReq{
					{Hours: 2, Price: 500},
					{Hours: 2, Price: 600},
				},
			},
		}
		_, _, errs := req.resolveHourlyBooking()
		if len(errs) == 0 {
			t.Fatalf("expected validation error for duplicate hours")
		}
	})

	// 13. CreateInput conversion
	t.Run("toInput correctly populates CreateInput with hourly packages", func(t *testing.T) {
		enabledVal := true
		req := facilityReq{
			Name: "Grand Hotel",
			Type: TypeHotel,
			HourlyBooking: &HourlyBookingReq{
				Enabled: &enabledVal,
				Options: []HourlyOptionReq{
					{Hours: 2, Price: 800},
					{Hours: 4, Price: 1500},
				},
			},
		}
		in := req.toInput(42)
		if !in.HourlyBookingEnabled {
			t.Errorf("expected HourlyBookingEnabled to be true")
		}
		if len(in.HourlyPackages) != 2 {
			t.Fatalf("expected 2 HourlyPackages, got %d", len(in.HourlyPackages))
		}
		if in.HourlyPackages[0].Hours != 2 || in.HourlyPackages[0].Price != 800 {
			t.Errorf("HourlyPackages[0] mismatch: %+v", in.HourlyPackages[0])
		}
		if in.HourlyPackages[1].Hours != 4 || in.HourlyPackages[1].Price != 1500 {
			t.Errorf("HourlyPackages[1] mismatch: %+v", in.HourlyPackages[1])
		}
	})

	_ = h
}

func TestFacilityJSONSerializationWithHourlyBooking(t *testing.T) {
	// Verify JSON marshaling includes hourlyBooking when configured
	fac := repository.Facility{
		ID:   "hotel-123",
		Type: "HOTEL",
		Name: "Sunrise Hotel",
		HourlyBooking: &repository.HourlyBookingConfig{
			Enabled: true,
			Options: []repository.HourlyBookingOption{
				{ID: "opt-1", Hours: 2, DurationHours: 2, Price: 600},
				{ID: "opt-2", Hours: 4, DurationHours: 4, Price: 1100},
				{ID: "opt-3", Hours: 6, DurationHours: 6, Price: 1500},
			},
		},
	}

	data, err := json.Marshal(fac)
	if err != nil {
		t.Fatalf("failed to marshal facility: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	hbRaw, ok := parsed["hourlyBooking"]
	if !ok || hbRaw == nil {
		t.Fatalf("expected hourlyBooking in JSON, got: %s", string(data))
	}

	hbMap := hbRaw.(map[string]any)
	if hbMap["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", hbMap["enabled"])
	}

	optionsRaw, ok := hbMap["options"].([]any)
	if !ok || len(optionsRaw) != 3 {
		t.Fatalf("expected 3 options, got %v", hbMap["options"])
	}

	opt0 := optionsRaw[0].(map[string]any)
	if opt0["hours"] != float64(2) || opt0["price"] != float64(600) {
		t.Errorf("opt0 mismatch: %v", opt0)
	}

	// Verify omitempty works when HourlyBooking is nil (backward compatibility)
	facNoHourly := repository.Facility{
		ID:   "hotel-456",
		Type: "HOTEL",
		Name: "Standard Hotel",
	}

	dataNoHourly, err := json.Marshal(facNoHourly)
	if err != nil {
		t.Fatalf("failed to marshal facility: %v", err)
	}

	var parsedNoHourly map[string]any
	if err := json.Unmarshal(dataNoHourly, &parsedNoHourly); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if _, exists := parsedNoHourly["hourlyBooking"]; exists {
		t.Errorf("expected hourlyBooking to be omitted when nil, got: %v", parsedNoHourly["hourlyBooking"])
	}
}

func TestCreateHotelWithHourlyBookingRejectsMoreThanThreeOptionsViaHTTP(t *testing.T) {
	mux := http.NewServeMux()
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	h := New(nil, signer, nil)
	mux.Handle("POST /api/v1/facilities", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.create(w, r)
	}))

	token, _ := signer.Generate("owner@example.com", "1", "ROLE_HALL_OWNER")

	payload := map[string]any{
		"name": "Excessive Options Hotel",
		"type": "HOTEL",
		"imageUrl": "https://example.com/cover.jpg",
		"hourlyBooking": map[string]any{
			"enabled": true,
			"options": []map[string]any{
				{"hours": 1, "price": 100},
				{"hours": 2, "price": 200},
				{"hours": 3, "price": 300},
				{"hours": 4, "price": 400},
			},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/v1/facilities", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d: %s", rr.Code, rr.Body.String())
	}

	var res struct {
		Success   bool   `json:"success"`
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	if res.ErrorCode != "VALIDATION_ERROR" {
		t.Errorf("expected VALIDATION_ERROR, got %s", res.ErrorCode)
	}
}
