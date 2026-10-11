package repository_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/config"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
)

func TestLiveDatabaseIntegration(t *testing.T) {
	_ = os.Chdir("../../../")
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Skipf("Skipping live database test: unable to connect: %v", err)
	}
	defer pool.Close()



	var totalFacilities int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM facilities").Scan(&totalFacilities)
	if err != nil {
		t.Fatalf("Query facilities failed: %v", err)
	}
	t.Logf("Total facilities in DB: %d", totalFacilities)

	rows, err := pool.Query(ctx, "SELECT id, name, type, status, is_deleted, lat, lng, avg_rating, review_count FROM facilities")
	if err != nil {
		t.Fatalf("Query facilities failed: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, typ, status string
		var isDeleted bool
		var lat, lng *float64
		var avgRating *float64
		var reviewCount *int
		if err := rows.Scan(&id, &name, &typ, &status, &isDeleted, &lat, &lng, &avgRating, &reviewCount); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		latVal, lngVal := 0.0, 0.0
		if lat != nil {
			latVal = *lat
		}
		if lng != nil {
			lngVal = *lng
		}
		t.Logf("Facility: id=%s name=%s type=%s status=%s lat=%f lng=%f", id, name, typ, status, latVal, lngVal)
	}

	// Approve and verify the facilities and set ratings so recommendation tests can return data
	_, err = pool.Exec(ctx, `
		UPDATE facilities SET status = 'APPROVED', is_verified = TRUE, avg_rating = 4.9, review_count = 125 WHERE id = 'c16d2078-e4e5-44d5-8204-af9d28d0abcd';
		UPDATE facilities SET status = 'APPROVED', is_verified = TRUE, avg_rating = 4.8, review_count = 98 WHERE id = '411fb21d-db3f-449f-a930-0f61d9870682';
		UPDATE facilities SET status = 'APPROVED', is_verified = TRUE, avg_rating = 4.7, review_count = 50 WHERE id = '569f6ddd-5b48-4692-83c4-4ae8c4adb9ed';
		UPDATE facilities SET status = 'APPROVED', is_verified = TRUE, avg_rating = 4.6, review_count = 30 WHERE id = 'be3a97d3-549e-4c64-a313-9c69b648b82c';
	`)
	if err != nil {
		t.Fatalf("Failed to approve facilities: %v", err)
	}

	repo := repository.New(pool)

	// 1. Test ALL
	itemsAll, countAll, err := repo.GetRecommendations(ctx, dto.RecommendationParams{
		Lat:      12.9716,
		Lng:      77.5946,
		Type:     "ALL",
		RadiusKm: 50,
		Page:     1,
		Size:     10,
	})
	if err != nil {
		t.Fatalf("GetRecommendations ALL error: %v", err)
	}
	t.Logf("=== TYPE=ALL: Found %d (total %d) ===", len(itemsAll), countAll)
	for i, it := range itemsAll {
		var dist float64
		if it.DistanceKm != nil {
			dist = *it.DistanceKm
		}
		t.Logf("[%d] %s (%s) - Rating: %.1f, Dist: %.2f km", i+1, it.Name, it.Type, it.AvgRating, dist)
		if !it.IsVerified {
			t.Errorf("Expected recommendation to have isVerified=true, got false for %s", it.Name)
		}
	}

	// 2. Test HALL
	itemsHall, countHall, err := repo.GetRecommendations(ctx, dto.RecommendationParams{
		Lat:      12.9716,
		Lng:      77.5946,
		Type:     "HALL",
		RadiusKm: 50,
		Page:     1,
		Size:     10,
	})
	if err != nil {
		t.Fatalf("GetRecommendations HALL error: %v", err)
	}
	t.Logf("=== TYPE=HALL: Found %d (total %d) ===", len(itemsHall), countHall)
	for i, it := range itemsHall {
		var dist float64
		if it.DistanceKm != nil {
			dist = *it.DistanceKm
		}
		t.Logf("[%d] %s (%s) - Rating: %.1f, Dist: %.2f km", i+1, it.Name, it.Type, it.AvgRating, dist)
		if it.Type != "HALL" {
			t.Errorf("Expected HALL, got %s", it.Type)
		}
	}

	// 3. Test HOTEL
	itemsHotel, countHotel, err := repo.GetRecommendations(ctx, dto.RecommendationParams{
		Lat:      12.9716,
		Lng:      77.5946,
		Type:     "HOTEL",
		RadiusKm: 50,
		Page:     1,
		Size:     10,
	})
	if err != nil {
		t.Fatalf("GetRecommendations HOTEL error: %v", err)
	}
	t.Logf("=== TYPE=HOTEL: Found %d (total %d) ===", len(itemsHotel), countHotel)
	for i, it := range itemsHotel {
		var dist float64
		if it.DistanceKm != nil {
			dist = *it.DistanceKm
		}
		t.Logf("[%d] %s (%s) - Rating: %.1f, Dist: %.2f km", i+1, it.Name, it.Type, it.AvgRating, dist)
		if it.Type != "HOTEL" {
			t.Errorf("Expected HOTEL, got %s", it.Type)
		}
	}

	// 4. Test with eventType = "WEDDING"
	itemsWed, countWed, err := repo.GetRecommendations(ctx, dto.RecommendationParams{
		Lat:       12.9716,
		Lng:       77.5946,
		Type:      "ALL",
		RadiusKm:  50,
		EventType: "WEDDING",
		Page:      1,
		Size:      10,
	})
	if err != nil {
		t.Fatalf("GetRecommendations WEDDING error: %v", err)
	}
	t.Logf("=== EVENT_TYPE=WEDDING: Found %d (total %d) ===", len(itemsWed), countWed)
	for _, it := range itemsWed {
		if len(it.EventCodes) > 0 {
			hasWedding := false
			for _, c := range it.EventCodes {
				if c == "WEDDING" {
					hasWedding = true
					break
				}
			}
			if !hasWedding {
				t.Errorf("Venue %s has events %v but neither empty nor contains WEDDING", it.Name, it.EventCodes)
			}
		}
	}
}

func TestRecommendationsDiscountFields(t *testing.T) {
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

	// Clean up any test coupons
	_, _ = pool.Exec(ctx, `DELETE FROM coupons WHERE code LIKE 'TEST_%' OR code LIKE 'ADMIN_REC_%' OR code LIKE 'SUPER_REC_%'`)
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM coupons WHERE code LIKE 'TEST_%' OR code LIKE 'ADMIN_REC_%' OR code LIKE 'SUPER_REC_%'`)
	}()

	fac1 := "c16d2078-e4e5-44d5-8204-af9d28d0abcd" // Hall
	fac2 := "411fb21d-db3f-449f-a930-0f61d9870682" // Hotel
	fac3 := "569f6ddd-5b48-4692-83c4-4ae8c4adb9ed" // Hall

	// fac1: Admin coupon only
	_, err = pool.Exec(ctx, `
		INSERT INTO coupons (code, description, facility_id, discount_type, discount_value, is_active, is_deleted)
		VALUES ('ADMIN_REC_1', 'Admin 10% coupon', $1, 'PERCENT', 10, true, false)
	`, fac1)
	if err != nil {
		t.Fatalf("failed to insert admin coupon on fac1: %v", err)
	}

	// fac2: Super Admin coupon only
	_, err = pool.Exec(ctx, `
		INSERT INTO coupons (code, description, facility_id, discount_type, discount_value, is_active, is_deleted)
		VALUES ('SUPER_REC_2', 'Super Admin 20% coupon', $1, 'PERCENT', 20, true, false)
	`, fac2)
	if err != nil {
		t.Fatalf("failed to insert super admin coupon on fac2: %v", err)
	}

	// fac3: Both Admin and Super Admin coupons
	_, err = pool.Exec(ctx, `
		INSERT INTO coupons (code, description, facility_id, discount_type, discount_value, is_active, is_deleted)
		VALUES ('ADMIN_REC_BOTH', 'Admin 15% coupon', $1, 'PERCENT', 15, true, false)
	`, fac3)
	if err != nil {
		t.Fatalf("failed to insert admin coupon on fac3: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO coupons (code, description, facility_id, discount_type, discount_value, is_active, is_deleted)
		VALUES ('SUPER_REC_BOTH', 'Super 25% coupon', $1, 'PERCENT', 25, true, false)
	`, fac3)
	if err != nil {
		t.Fatalf("failed to insert super admin coupon on fac3: %v", err)
	}

	items, _, err := repo.GetRecommendations(ctx, dto.RecommendationParams{
		Lat:      12.9716,
		Lng:      77.5946,
		Type:     "ALL",
		RadiusKm: 50,
		Page:     1,
		Size:     10,
	})
	if err != nil {
		t.Fatalf("GetRecommendations error: %v", err)
	}

	var foundFac1, foundFac2, foundFac3, foundNone bool
	for _, v := range items {
		switch v.ID {
		case fac1:
			foundFac1 = true
			if v.AdminDiscountCouponCode == nil || *v.AdminDiscountCouponCode != "ADMIN_REC_1" {
				t.Errorf("rec fac1: expected AdminDiscountCouponCode 'ADMIN_REC_1', got %v", v.AdminDiscountCouponCode)
			}
			if v.AdminDiscountPercentage == nil || *v.AdminDiscountPercentage != 10 {
				t.Errorf("rec fac1: expected AdminDiscountPercentage 10, got %v", v.AdminDiscountPercentage)
			}
			if v.AdminDiscountPrice == nil || *v.AdminDiscountPrice <= 0 {
				t.Errorf("rec fac1: expected positive AdminDiscountPrice, got %v", v.AdminDiscountPrice)
			}
		case fac2:
			foundFac2 = true
			if v.AdminDiscountCouponCode == nil || *v.AdminDiscountCouponCode != "SUPER_REC_2" {
				t.Errorf("rec fac2: expected AdminDiscountCouponCode 'SUPER_REC_2', got %v", v.AdminDiscountCouponCode)
			}
			if v.AdminDiscountPercentage == nil || *v.AdminDiscountPercentage != 20 {
				t.Errorf("rec fac2: expected AdminDiscountPercentage 20, got %v", v.AdminDiscountPercentage)
			}
			if v.StartingPrice != nil && *v.StartingPrice > 0 {
				if v.AdminDiscountPrice == nil || *v.AdminDiscountPrice <= 0 {
					t.Errorf("rec fac2: expected positive AdminDiscountPrice, got %v", v.AdminDiscountPrice)
				}
			}
		case fac3:
			foundFac3 = true
			if v.AdminDiscountCouponCode == nil || *v.AdminDiscountCouponCode != "SUPER_REC_BOTH" {
				t.Errorf("rec fac3: expected AdminDiscountCouponCode 'SUPER_REC_BOTH', got %v", v.AdminDiscountCouponCode)
			}
			if v.AdminDiscountPercentage == nil || *v.AdminDiscountPercentage != 25 {
				t.Errorf("rec fac3: expected AdminDiscountPercentage 25, got %v", v.AdminDiscountPercentage)
			}
		default:
			if v.AdminDiscountCouponCode == nil || (v.AdminDiscountCouponCode != nil && *v.AdminDiscountCouponCode == "ALLHALLSDIWALI") {
				foundNone = true
			}
		}
	}

	if !foundFac1 {
		t.Errorf("recommendation fac1 (%s) not found in response", fac1)
	}
	if !foundFac2 {
		t.Errorf("recommendation fac2 (%s) not found in response", fac2)
	}
	if !foundFac3 {
		t.Errorf("recommendation fac3 (%s) not found in response", fac3)
	}
	if !foundNone {
		t.Errorf("expected at least one recommendation venue with no discounts")
	}
}

func TestPlatformOffersCouponInRecommendations(t *testing.T) {
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

	// Clean up any test coupons
	_, _ = pool.Exec(ctx, `DELETE FROM coupons WHERE code LIKE 'TEST_%'`)
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM coupons WHERE code LIKE 'TEST_%'`)
	}()

	// Insert platform-wide ALL-venues coupon matching user's exact example
	_, err = pool.Exec(ctx, `
		INSERT INTO coupons (code, description, facility_type, discount_type, discount_value, max_discount, is_active, is_deleted)
		VALUES ('TEST_ALLHALLSDIWALI', '10% off at any venue', 'ALL', 'PERCENT', 10, 5000, true, false)
	`)
	if err != nil {
		t.Fatalf("failed to insert test coupon: %v", err)
	}

	items, _, err := repo.GetRecommendations(ctx, dto.RecommendationParams{
		Lat:      12.9716,
		Lng:      77.5946,
		Type:     "ALL",
		RadiusKm: 50,
		Page:     1,
		Size:     10,
	})
	if err != nil {
		t.Fatalf("GetRecommendations error: %v", err)
	}

	if len(items) == 0 {
		t.Fatalf("expected recommendation items in response")
	}

	for _, v := range items {
		if v.AdminDiscountCouponCode == nil || (*v.AdminDiscountCouponCode != "TEST_ALLHALLSDIWALI" && *v.AdminDiscountCouponCode != "ALLHALLSDIWALI") {
			t.Errorf("recommendation item %s (%s): expected AdminDiscountCouponCode 'TEST_ALLHALLSDIWALI' or 'ALLHALLSDIWALI', got %v", v.ID, v.Name, *v.AdminDiscountCouponCode)
		}
		if v.AdminDiscountPercentage == nil || *v.AdminDiscountPercentage < 10 {
			t.Errorf("recommendation item %s: expected AdminDiscountPercentage >= 10, got %v", v.ID, v.AdminDiscountPercentage)
		}
		if v.StartingPrice != nil && *v.StartingPrice > 0 {
			if v.AdminDiscountPrice == nil || *v.AdminDiscountPrice <= 0 {
				t.Errorf("recommendation item %s: expected positive AdminDiscountPrice, got %v", v.ID, v.AdminDiscountPrice)
			}
		}
	}
}


