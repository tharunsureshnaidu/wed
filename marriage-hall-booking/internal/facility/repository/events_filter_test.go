package repository_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/config"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
)

func TestEventsFilterIntegration(t *testing.T) {
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

	// 1. Without eventType
	allVenues, totalAll, err := repo.List(ctx, repository.ListFilter{
		Page: 0,
		Size: 50,
	})
	if err != nil {
		t.Fatalf("List without eventType failed: %v", err)
	}
	t.Logf("Total venues without filter: %d, items: %d", totalAll, len(allVenues))

	// 2. With eventType = "WEDDING"
	weddingVenues, totalWedding, err := repo.List(ctx, repository.ListFilter{
		EventType: "WEDDING",
		Page:      0,
		Size:      50,
	})
	if err != nil {
		t.Fatalf("List with eventType=WEDDING failed: %v", err)
	}
	t.Logf("Total venues for WEDDING: %d, items: %d", totalWedding, len(weddingVenues))

	// Check each returned venue satisfies rule:
	// either len(EventCodes) == 0 (no configured events) OR contains WEDDING
	for _, v := range weddingVenues {
		if len(v.EventCodes) > 0 {
			hasWedding := false
			for _, code := range v.EventCodes {
				if code == "WEDDING" {
					hasWedding = true
					break
				}
			}
			if !hasWedding {
				t.Errorf("Venue %s (%s) has events %v but neither empty nor contains WEDDING", v.ID, v.Name, v.EventCodes)
			}
		}
	}

	// 3. With eventType = "BIRTHDAY"
	birthdayVenues, totalBirthday, err := repo.List(ctx, repository.ListFilter{
		EventType: "BIRTHDAY",
		Page:      0,
		Size:      50,
	})
	if err != nil {
		t.Fatalf("List with eventType=BIRTHDAY failed: %v", err)
	}
	t.Logf("Total venues for BIRTHDAY: %d, items: %d", totalBirthday, len(birthdayVenues))

	for _, v := range birthdayVenues {
		if len(v.EventCodes) > 0 {
			hasBirthday := false
			for _, code := range v.EventCodes {
				if code == "BIRTHDAY" {
					hasBirthday = true
					break
				}
			}
			if !hasBirthday {
				t.Errorf("Venue %s (%s) has events %v but neither empty nor contains BIRTHDAY", v.ID, v.Name, v.EventCodes)
			}
		}
	}

	// 4. Combined with Type = "HOTEL"
	hotelWedding, totalHotelWedding, err := repo.List(ctx, repository.ListFilter{
		Type:      "HOTEL",
		EventType: "WEDDING",
		Page:      0,
		Size:      50,
	})
	if err != nil {
		t.Fatalf("List with Type=HOTEL & EventType=WEDDING failed: %v", err)
	}
	t.Logf("Total hotels for WEDDING: %d, items: %d", totalHotelWedding, len(hotelWedding))
	for _, v := range hotelWedding {
		if v.Type != "HOTEL" {
			t.Errorf("Expected Type HOTEL, got %s", v.Type)
		}
	}

	// 5. Combined with Type = "MARRIAGE_HALL"
	hallWedding, totalHallWedding, err := repo.List(ctx, repository.ListFilter{
		Type:      "MARRIAGE_HALL",
		EventType: "WEDDING",
		Page:      0,
		Size:      50,
	})
	if err != nil {
		t.Fatalf("List with Type=MARRIAGE_HALL & EventType=WEDDING failed: %v", err)
	}
	t.Logf("Total halls for WEDDING: %d, items: %d", totalHallWedding, len(hallWedding))
	for _, v := range hallWedding {
		if v.Type != "MARRIAGE_HALL" {
			t.Errorf("Expected Type MARRIAGE_HALL, got %s", v.Type)
		}
	}
}

func TestEventsFilterExactRules(t *testing.T) {
	_ = os.Chdir("../../../")
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Skipf("Skipping live database test: unable to connect: %v", err)
	}
	defer pool.Close()

	// Pick 3 existing venues or create a temp scenario in a transaction / rollback
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	// Get an existing user id to use as owner
	var ownerID int64
	if err := tx.QueryRow(ctx, "SELECT id FROM users LIMIT 1").Scan(&ownerID); err != nil {
		t.Skipf("No user found in DB: %v", err)
	}

	// Insert 3 test facilities:
	// venueEmpty: has NO facility_events
	// venueWed: has WEDDING, RECEPTION
	// venueBirth: has BIRTHDAY
	var idEmpty, idWed, idBirth string
	err = tx.QueryRow(ctx, `INSERT INTO facilities (owner_id, name, type, city, status)
		VALUES ($1, 'Test Hotel Empty Events', 'HOTEL', 'TestCity', 'APPROVED') RETURNING id`, ownerID).Scan(&idEmpty)
	if err != nil {
		t.Fatalf("Failed to create venueEmpty: %v", err)
	}

	err = tx.QueryRow(ctx, `INSERT INTO facilities (owner_id, name, type, city, status)
		VALUES ($1, 'Test Hotel Wedding Events', 'HOTEL', 'TestCity', 'APPROVED') RETURNING id`, ownerID).Scan(&idWed)
	if err != nil {
		t.Fatalf("Failed to create venueWed: %v", err)
	}

	err = tx.QueryRow(ctx, `INSERT INTO facilities (owner_id, name, type, city, status)
		VALUES ($1, 'Test Hotel Birthday Events', 'HOTEL', 'TestCity', 'APPROVED') RETURNING id`, ownerID).Scan(&idBirth)
	if err != nil {
		t.Fatalf("Failed to create venueBirth: %v", err)
	}

	// Insert facility_events for venueWed and venueBirth
	_, err = tx.Exec(ctx, `INSERT INTO facility_events (facility_id, event_code) VALUES
		($1, 'WEDDING'), ($1, 'RECEPTION'), ($2, 'BIRTHDAY')`, idWed, idBirth)
	if err != nil {
		t.Fatalf("Failed to insert facility_events: %v", err)
	}

	// Helper to check query results in transaction
	queryVenues := func(eventType string) map[string]bool {
		filterClause := ` WHERE f.is_deleted = FALSE
			AND f.city = 'TestCity'
			AND ($1 = '' OR EXISTS (
				SELECT 1 FROM facility_events fe
				 WHERE fe.facility_id = f.id AND fe.event_code = $1)
			      OR NOT EXISTS (
				SELECT 1 FROM facility_events fe WHERE fe.facility_id = f.id))`
		rows, err := tx.Query(ctx, "SELECT f.id::text FROM facilities f"+filterClause, eventType)
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		defer rows.Close()
		found := map[string]bool{}
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			found[id] = true
		}
		return found
	}

	// Case 1 & 2: eventType = "WEDDING"
	// Should include idEmpty (no events configured) AND idWed (has WEDDING)
	// Should EXCLUDE idBirth (configured exclusively for BIRTHDAY)
	wedRes := queryVenues("WEDDING")
	if !wedRes[idEmpty] {
		t.Errorf("Expected idEmpty (no events) to be INCLUDED for WEDDING")
	}
	if !wedRes[idWed] {
		t.Errorf("Expected idWed (has WEDDING) to be INCLUDED for WEDDING")
	}
	if wedRes[idBirth] {
		t.Errorf("Expected idBirth (has BIRTHDAY) to be EXCLUDED for WEDDING")
	}

	// Case 2: eventType = "RECEPTION"
	// Should include idEmpty AND idWed, exclude idBirth
	recRes := queryVenues("RECEPTION")
	if !recRes[idEmpty] || !recRes[idWed] || recRes[idBirth] {
		t.Errorf("RECEPTION filter unexpected result: empty=%v, wed=%v, birth=%v", recRes[idEmpty], recRes[idWed], recRes[idBirth])
	}

	// Case 2: eventType = "BIRTHDAY"
	// Should include idEmpty AND idBirth, exclude idWed
	birthRes := queryVenues("BIRTHDAY")
	if !birthRes[idEmpty] {
		t.Errorf("Expected idEmpty to be INCLUDED for BIRTHDAY")
	}
	if !birthRes[idBirth] {
		t.Errorf("Expected idBirth to be INCLUDED for BIRTHDAY")
	}
	if birthRes[idWed] {
		t.Errorf("Expected idWed to be EXCLUDED for BIRTHDAY")
	}

	// Case: eventType = "CONFERENCE" (none of the configured venues have CONFERENCE)
	// Should include idEmpty, exclude idWed and idBirth
	confRes := queryVenues("CONFERENCE")
	if !confRes[idEmpty] {
		t.Errorf("Expected idEmpty to be INCLUDED for CONFERENCE")
	}
	if confRes[idWed] || confRes[idBirth] {
		t.Errorf("Expected both idWed and idBirth to be EXCLUDED for CONFERENCE")
	}

	// Case 3: eventType = "" (omitted)
	// Should include all 3
	allRes := queryVenues("")
	if !allRes[idEmpty] || !allRes[idWed] || !allRes[idBirth] {
		t.Errorf("Expected all 3 venues when eventType is empty: empty=%v, wed=%v, birth=%v", allRes[idEmpty], allRes[idWed], allRes[idBirth])
	}
}
