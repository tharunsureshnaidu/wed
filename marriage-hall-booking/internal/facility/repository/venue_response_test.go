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

	// MUST NOT expose duplicate verified/featured
	for _, forbidden := range []string{"verified", "featured"} {
		if _, exists := m[forbidden]; exists {
			t.Errorf("VenueResponse should NOT expose duplicate field %q", forbidden)
		}
	}

	// MUST NOT expose nested location, rating, price, coordinates objects
	for _, forbidden := range []string{"location", "rating", "price", "coordinates"} {
		if _, exists := m[forbidden]; exists {
			t.Errorf("VenueResponse should NOT expose nested object field %q", forbidden)
		}
	}

	// MUST expose isVerified and isFeatured
	if _, exists := m["isVerified"]; !exists {
		t.Errorf("VenueResponse should expose 'isVerified'")
	}
	if _, exists := m["isFeatured"]; !exists {
		t.Errorf("VenueResponse should expose 'isFeatured'")
	}

	// MUST expose policies, faqs, reviews, amenities, and images
	for _, reqField := range []string{"policies", "faqs", "reviews", "amenities", "images"} {
		if val, exists := m[reqField]; !exists || val == nil {
			t.Errorf("VenueResponse should expose %q array, got %v", reqField, val)
		}
	}

	// MUST expose coverImage (key present, value null when empty)
	if _, exists := m["coverImage"]; !exists {
		t.Errorf("VenueResponse should expose 'coverImage' key")
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

func TestVenueResponseDiscountScenarios(t *testing.T) {
	couponCode := "ALLHALLSDIWALI"
	couponPrice := 18750.0
	couponPct := 15.0

	t.Run("Admin Discount Populated and SuperAdmin Fields Removed", func(t *testing.T) {
		f := Facility{
			ID:                      "venue-admin-discount",
			Name:                    "Ramesh Grand Premium Palace",
			Type:                    "MARRIAGE_HALL",
			AdminDiscountCouponCode: &couponCode,
			AdminDiscountPrice:      &couponPrice,
			AdminDiscountPercentage: &couponPct,
		}

		resp := f.ToVenueResponse()
		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}

		var m map[string]any
		_ = json.Unmarshal(b, &m)

		if m["adminDiscountCouponCode"] != "ALLHALLSDIWALI" {
			t.Errorf("expected adminDiscountCouponCode 'ALLHALLSDIWALI', got %v", m["adminDiscountCouponCode"])
		}
		if m["adminDiscountPrice"] != 18750.0 {
			t.Errorf("expected adminDiscountPrice 18750, got %v", m["adminDiscountPrice"])
		}
		if m["adminDiscountPercentage"] != 15.0 {
			t.Errorf("expected adminDiscountPercentage 15, got %v", m["adminDiscountPercentage"])
		}

		for _, superField := range []string{
			"superAdminDiscountCouponCode",
			"superAdminDiscountPrice",
			"superAdminDiscountPercentage",
		} {
			if _, exists := m[superField]; exists {
				t.Errorf("expected field %q to be removed from json response, but it was present", superField)
			}
		}
	})

	t.Run("No-Discount Scenario", func(t *testing.T) {
		f := Facility{
			ID:   "venue-none",
			Name: "Regular Venue",
			Type: "MARRIAGE_HALL",
		}

		resp := f.ToVenueResponse()
		b, _ := json.Marshal(resp)
		var m map[string]any
		_ = json.Unmarshal(b, &m)

		for _, field := range []string{
			"adminDiscountCouponCode", "adminDiscountPrice", "adminDiscountPercentage",
		} {
			val, exists := m[field]
			if !exists {
				t.Errorf("expected field %q to be present in json", field)
			}
			if val != nil {
				t.Errorf("expected field %q to be null, got %v", field, val)
			}
		}

		for _, superField := range []string{
			"superAdminDiscountCouponCode",
			"superAdminDiscountPrice",
			"superAdminDiscountPercentage",
		} {
			if _, exists := m[superField]; exists {
				t.Errorf("expected field %q to be removed from json response, but it was present", superField)
			}
		}
	})
}

func TestVenueResponseCoverImageScenarios(t *testing.T) {
	t.Run("Explicit Cover Image", func(t *testing.T) {
		f := Facility{
			ID:   "v-1",
			Name: "Venue With Cover",
			Type: "MARRIAGE_HALL",
			Images: []Image{
				{ID: "img-1", URL: "https://example.com/normal.jpg", IsCover: false, SortOrder: 0},
				{ID: "img-2", URL: "https://example.com/cover.jpg", IsCover: true, SortOrder: 1},
			},
		}

		resp := f.ToVenueResponse()
		if resp.CoverImage == nil || *resp.CoverImage != "https://example.com/cover.jpg" {
			t.Errorf("expected coverImage 'https://example.com/cover.jpg', got %v", resp.CoverImage)
		}

		b, _ := json.Marshal(resp)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["coverImage"] != "https://example.com/cover.jpg" {
			t.Errorf("expected json coverImage 'https://example.com/cover.jpg', got %v", m["coverImage"])
		}
	})

	t.Run("Fallback to First Image When None Explicitly Flagged", func(t *testing.T) {
		f := Facility{
			ID:   "v-2",
			Name: "Venue Without Explicit Cover",
			Type: "MARRIAGE_HALL",
			Images: []Image{
				{ID: "img-1", URL: "https://example.com/first.jpg", IsCover: false, SortOrder: 0},
				{ID: "img-2", URL: "https://example.com/second.jpg", IsCover: false, SortOrder: 1},
			},
		}

		resp := f.ToVenueResponse()
		if resp.CoverImage == nil || *resp.CoverImage != "https://example.com/first.jpg" {
			t.Errorf("expected fallback coverImage 'https://example.com/first.jpg', got %v", resp.CoverImage)
		}
	})

	t.Run("Null Cover Image When No Images", func(t *testing.T) {
		f := Facility{
			ID:     "v-3",
			Name:   "Venue With No Images",
			Type:   "MARRIAGE_HALL",
			Images: []Image{},
		}

		resp := f.ToVenueResponse()
		if resp.CoverImage != nil {
			t.Errorf("expected nil coverImage, got %v", resp.CoverImage)
		}

		b, _ := json.Marshal(resp)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if val, exists := m["coverImage"]; !exists || val != nil {
			t.Errorf("expected json coverImage null, got %v (exists=%v)", val, exists)
		}
	})
}
