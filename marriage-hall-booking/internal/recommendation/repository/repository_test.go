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

	// Approve the facilities and set ratings so recommendation tests can return data
	_, err = pool.Exec(ctx, `
		UPDATE facilities SET status = 'APPROVED', avg_rating = 4.9, review_count = 125 WHERE id = 'c16d2078-e4e5-44d5-8204-af9d28d0abcd';
		UPDATE facilities SET status = 'APPROVED', avg_rating = 4.8, review_count = 98 WHERE id = '411fb21d-db3f-449f-a930-0f61d9870682';
		UPDATE facilities SET status = 'APPROVED', avg_rating = 4.7, review_count = 50 WHERE id = '569f6ddd-5b48-4692-83c4-4ae8c4adb9ed';
		UPDATE facilities SET status = 'APPROVED', avg_rating = 4.6, review_count = 30 WHERE id = 'be3a97d3-549e-4c64-a313-9c69b648b82c';
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
		t.Logf("[%d] %s (%s) - Rating: %.1f, Dist: %.2f km", i+1, it.Name, it.Type, it.Rating, it.DistanceKm)
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
		t.Logf("[%d] %s (%s) - Rating: %.1f, Dist: %.2f km", i+1, it.Name, it.Type, it.Rating, it.DistanceKm)
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
		t.Logf("[%d] %s (%s) - Rating: %.1f, Dist: %.2f km", i+1, it.Name, it.Type, it.Rating, it.DistanceKm)
		if it.Type != "HOTEL" {
			t.Errorf("Expected HOTEL, got %s", it.Type)
		}
	}
}
