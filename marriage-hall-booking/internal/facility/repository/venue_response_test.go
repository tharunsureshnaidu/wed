package repository

import (
	"encoding/json"
	"testing"
)

func TestVenueResponseOmitsPrivateOwnerInfo(t *testing.T) {
	phone := "9876543210"
	vendorID := "vendor-uuid-123"
	lat, lng := 12.9716, 77.5946
	city := "Bengaluru"
	pricePerDay := 150000.0

	f := Facility{
		ID:               "venue-1",
		OwnerID:          42,
		OwnerPhoneNumber: &phone,
		VendorID:         &vendorID,
		Name:             "Grand Palace",
		Type:             "MARRIAGE_HALL",
		City:             &city,
		Lat:              &lat,
		Lng:              &lng,
		AvgRating:        4.8,
		ReviewCount:      25,
		CapacityPax:      ptr(800),
		BasePricePerDay:  &pricePerDay,
		StartingPrice:    &pricePerDay,
	}

	resp := f.ToVenueResponse()
	if resp.Type != "HALL" {
		t.Fatalf("expected Type 'HALL', got %q", resp.Type)
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal VenueResponse: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	// Must contain public fields
	if m["id"] != "venue-1" {
		t.Errorf("expected id 'venue-1', got %v", m["id"])
	}
	if m["name"] != "Grand Palace" {
		t.Errorf("expected name 'Grand Palace', got %v", m["name"])
	}
	if m["type"] != "HALL" {
		t.Errorf("expected type 'HALL', got %v", m["type"])
	}
	if m["city"] != "Bengaluru" {
		t.Errorf("expected city 'Bengaluru', got %v", m["city"])
	}
	if m["avgRating"] != 4.8 {
		t.Errorf("expected avgRating 4.8, got %v", m["avgRating"])
	}

	// MUST NOT expose private owner/vendor info
	for _, forbidden := range []string{"ownerId", "ownerPhoneNumber", "vendorId", "ownerPhone", "ownerEmail"} {
		if _, exists := m[forbidden]; exists {
			t.Errorf("VenueResponse should NOT expose private field %q", forbidden)
		}
	}

	// MUST NOT expose nested location, rating, price, coordinates objects
	for _, forbidden := range []string{"location", "rating", "price", "coordinates"} {
		if _, exists := m[forbidden]; exists {
			t.Errorf("VenueResponse should NOT expose nested object field %q", forbidden)
		}
	}
}

func TestVenueResponseHotelType(t *testing.T) {
	city := "Bengaluru"
	startPrice := 5000.0

	f := Facility{
		ID:            "hotel-1",
		Name:          "Grand Bengaluru Hotel",
		Type:          "HOTEL",
		City:          &city,
		AvgRating:     4.7,
		StartingPrice: &startPrice,
	}

	resp := f.ToVenueResponse()
	if resp.Type != "HOTEL" {
		t.Fatalf("expected Type 'HOTEL', got %q", resp.Type)
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal VenueResponse: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if m["type"] != "HOTEL" {
		t.Errorf("expected type 'HOTEL', got %v", m["type"])
	}
}
