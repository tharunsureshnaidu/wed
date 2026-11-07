package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestListVenuesTypeValidation(t *testing.T) {
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	for _, invalidType := range []string{"INVALID", "RESTAURANT", "BAR", "CLUB", "123"} {
		t.Run("invalid type "+invalidType, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/venues?type="+invalidType, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 Bad Request for type %q, got %d", invalidType, rr.Code)
			}

			var body struct {
				Success   bool   `json:"success"`
				Message   string `json:"message"`
				ErrorCode string `json:"errorCode"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("failed to parse json response: %v", err)
			}

			if body.Success {
				t.Errorf("expected success false, got true")
			}
			if body.Message != "Invalid type. Supported types are HALL and HOTEL." {
				t.Errorf("expected message 'Invalid type. Supported types are HALL and HOTEL.', got %q", body.Message)
			}
			if body.ErrorCode != "VALIDATION_ERROR" {
				t.Errorf("expected errorCode 'VALIDATION_ERROR', got %q", body.ErrorCode)
			}
		})
	}
}

func TestListVenuesWithEventType(t *testing.T) {
	mux := http.NewServeMux()
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)

	_ = os.Chdir("../../../")
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.New(ctx, dbURL)
	if err != nil {
		t.Skipf("Skipping live database test: unable to connect: %v", err)
	}
	defer pool.Close()

	repo := repository.New(pool)
	h := New(repo, signer, nil)
	h.Register(mux)

	// Verify route handles request successfully with eventType parameter
	req := httptest.NewRequest("GET", "/api/v1/venues?type=HOTEL&eventType=WEDDING&page=0&size=10", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Content []repository.VenueResponse `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	for _, v := range body.Data.Content {
		if v.Status != "APPROVED" {
			t.Errorf("expected venue status to be APPROVED, got %q for venue %s", v.Status, v.ID)
		}
	}
}

func TestListVenuesOnlyApprovedStatus(t *testing.T) {
	mux := http.NewServeMux()
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)

	_ = os.Chdir("../../../")
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.New(ctx, dbURL)
	if err != nil {
		t.Skipf("Skipping live database test: unable to connect: %v", err)
	}
	defer pool.Close()

	repo := repository.New(pool)
	h := New(repo, signer, nil)
	h.Register(mux)

	req := httptest.NewRequest("GET", "/api/v1/venues?page=0&size=50", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Content []repository.VenueResponse `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !body.Success {
		t.Errorf("expected success true, got false")
	}

	for _, v := range body.Data.Content {
		if v.Status != "APPROVED" {
			t.Errorf("expected all returned venues to have status 'APPROVED', but got %q for venue %s (%s)", v.Status, v.ID, v.Name)
		}
	}
}

func TestListVenuesDiscountFields(t *testing.T) {
	mux := http.NewServeMux()
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)

	_ = os.Chdir("../../../")
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.New(ctx, dbURL)
	if err != nil {
		t.Skipf("Skipping live database test: unable to connect: %v", err)
	}
	defer pool.Close()

	repo := repository.New(pool)
	h := New(repo, signer, nil)
	h.Register(mux)

	// Clean up any test coupons or leftover platform coupons
	_, _ = pool.Exec(ctx, `DELETE FROM coupons WHERE code LIKE 'TEST_%' OR code LIKE 'ADMIN_DISC_%' OR code LIKE 'SUPER_DISC_%'`)
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM coupons WHERE code LIKE 'TEST_%' OR code LIKE 'ADMIN_DISC_%' OR code LIKE 'SUPER_DISC_%'`)
	}()

	fac1 := "c16d2078-e4e5-44d5-8204-af9d28d0abcd"
	fac2 := "411fb21d-db3f-449f-a930-0f61d9870682"
	fac3 := "569f6ddd-5b48-4692-83c4-4ae8c4adb9ed"

	// fac1: Admin coupon only
	_, err = pool.Exec(ctx, `
		INSERT INTO coupons (code, description, facility_id, discount_type, discount_value, is_active, is_deleted)
		VALUES ('ADMIN_DISC_1', 'Admin 10% coupon', $1, 'PERCENT', 10, true, false)
	`, fac1)
	if err != nil {
		t.Fatalf("failed to insert admin coupon: %v", err)
	}

	// fac2: Super Admin coupon only
	_, err = pool.Exec(ctx, `
		INSERT INTO coupons (code, description, facility_id, discount_type, discount_value, is_active, is_deleted)
		VALUES ('SUPER_DISC_2', 'Super Admin 20% coupon', $1, 'PERCENT', 20, true, false)
	`, fac2)
	if err != nil {
		t.Fatalf("failed to insert super admin coupon: %v", err)
	}

	// fac3: Both Admin and Super Admin coupons
	_, err = pool.Exec(ctx, `
		INSERT INTO coupons (code, description, facility_id, discount_type, discount_value, is_active, is_deleted)
		VALUES ('ADMIN_DISC_BOTH', 'Admin 15% coupon', $1, 'PERCENT', 15, true, false)
	`, fac3)
	if err != nil {
		t.Fatalf("failed to insert admin coupon on fac3: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO coupons (code, description, facility_id, discount_type, discount_value, is_active, is_deleted)
		VALUES ('SUPER_DISC_BOTH', 'Super 25% coupon', $1, 'PERCENT', 25, true, false)
	`, fac3)
	if err != nil {
		t.Fatalf("failed to insert super admin coupon on fac3: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/venues?page=0&size=50", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Content []repository.VenueResponse `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	var foundFac1, foundFac2, foundFac3, foundNone bool
	for _, v := range body.Data.Content {
		switch v.ID {
		case fac1:
			foundFac1 = true
			if v.AdminDiscountCouponCode == nil || *v.AdminDiscountCouponCode != "ADMIN_DISC_1" {
				t.Errorf("fac1: expected AdminDiscountCouponCode 'ADMIN_DISC_1', got %v", v.AdminDiscountCouponCode)
			}
			if v.AdminDiscountPercentage == nil || *v.AdminDiscountPercentage != 10 {
				t.Errorf("fac1: expected AdminDiscountPercentage 10, got %v", v.AdminDiscountPercentage)
			}
			if v.AdminDiscountPrice == nil || *v.AdminDiscountPrice <= 0 {
				t.Errorf("fac1: expected positive AdminDiscountPrice, got %v", v.AdminDiscountPrice)
			}
		case fac2:
			foundFac2 = true
			if v.AdminDiscountCouponCode == nil || *v.AdminDiscountCouponCode != "SUPER_DISC_2" {
				t.Errorf("fac2: expected AdminDiscountCouponCode 'SUPER_DISC_2', got %v", v.AdminDiscountCouponCode)
			}
			if v.AdminDiscountPercentage == nil || *v.AdminDiscountPercentage != 20 {
				t.Errorf("fac2: expected AdminDiscountPercentage 20, got %v", v.AdminDiscountPercentage)
			}
			if v.StartingPrice != nil && *v.StartingPrice > 0 {
				if v.AdminDiscountPrice == nil || *v.AdminDiscountPrice <= 0 {
					t.Errorf("fac2: expected positive AdminDiscountPrice, got %v", v.AdminDiscountPrice)
				}
			}
		case fac3:
			foundFac3 = true
			if v.AdminDiscountCouponCode == nil || *v.AdminDiscountCouponCode != "SUPER_DISC_BOTH" {
				t.Errorf("fac3: expected AdminDiscountCouponCode 'SUPER_DISC_BOTH', got %v", v.AdminDiscountCouponCode)
			}
			if v.AdminDiscountPercentage == nil || *v.AdminDiscountPercentage != 25 {
				t.Errorf("fac3: expected AdminDiscountPercentage 25, got %v", v.AdminDiscountPercentage)
			}
		default:
			// Venues with no discounts or only ALLHALLSDIWALI
			if v.AdminDiscountCouponCode == nil || (v.AdminDiscountCouponCode != nil && *v.AdminDiscountCouponCode == "ALLHALLSDIWALI") {
				foundNone = true
			}
		}
	}

	if !foundFac1 {
		t.Errorf("facility 1 (%s) not found in response", fac1)
	}
	if !foundFac2 {
		t.Errorf("facility 2 (%s) not found in response", fac2)
	}
	if !foundFac3 {
		t.Errorf("facility 3 (%s) not found in response", fac3)
	}
	if !foundNone {
		t.Errorf("expected at least one venue with no discounts")
	}
}

func TestPlatformOffersCouponInVenues(t *testing.T) {
	mux := http.NewServeMux()
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)

	_ = os.Chdir("../../../")
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.New(ctx, dbURL)
	if err != nil {
		t.Skipf("Skipping live database test: unable to connect: %v", err)
	}
	defer pool.Close()

	repo := repository.New(pool)
	h := New(repo, signer, nil)
	h.Register(mux)

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

	req := httptest.NewRequest("GET", "/api/v1/venues?lat=12.9716&lng=77.5946&eventType=WEDDING&page=0&size=10", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Content []repository.VenueResponse `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(body.Data.Content) == 0 {
		t.Fatalf("expected venues in response")
	}

	for _, v := range body.Data.Content {
		if v.AdminDiscountCouponCode == nil || *v.AdminDiscountCouponCode != "TEST_ALLHALLSDIWALI" {
			t.Errorf("venue %s (%s): expected AdminDiscountCouponCode 'TEST_ALLHALLSDIWALI', got %v", v.ID, v.Name, v.AdminDiscountCouponCode)
		}
		if v.AdminDiscountPercentage == nil || *v.AdminDiscountPercentage < 10 {
			t.Errorf("venue %s: expected AdminDiscountPercentage >= 10, got %v", v.ID, v.AdminDiscountPercentage)
		}
		if v.StartingPrice != nil && *v.StartingPrice > 0 {
			if v.AdminDiscountPrice == nil || *v.AdminDiscountPrice <= 0 {
				t.Errorf("venue %s: expected positive AdminDiscountPrice, got %v", v.ID, v.AdminDiscountPrice)
			}
		}
	}
}

func TestRameshGrandPalaceWithAllHallsDiwali(t *testing.T) {
	mux := http.NewServeMux()
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)

	_ = os.Chdir("../../../")
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.New(ctx, dbURL)
	if err != nil {
		t.Skipf("Skipping live database test: unable to connect: %v", err)
	}
	defer pool.Close()

	// Ensure ALLHALLSDIWALI exists active and not deleted
	_, _ = pool.Exec(ctx, `UPDATE coupons SET is_active = true, is_deleted = false, facility_type = 'ALL', discount_type = 'PERCENT', discount_value = 10, max_discount = 5000 WHERE code = 'ALLHALLSDIWALI'`)
	_, _ = pool.Exec(ctx, `INSERT INTO coupons (code, description, facility_type, discount_type, discount_value, max_discount, is_active, is_deleted) VALUES ('ALLHALLSDIWALI', '10% off at any venue', 'ALL', 'PERCENT', 10, 5000, true, false) ON CONFLICT DO NOTHING`)

	repo := repository.New(pool)
	h := New(repo, signer, nil)
	h.Register(mux)

	req := httptest.NewRequest("GET", "/api/v1/venues?lat=12.9716&lng=77.5946&eventType=WEDDING&type=hall", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}

	data := raw["data"].(map[string]any)
	content := data["content"].([]any)

	var foundRamesh bool
	for _, item := range content {
		v := item.(map[string]any)
		if v["name"] == "Ramesh Grand Premium Palace" || v["id"] == "9d65b67d-63c9-455c-9e6b-305deb6ecb9b" {
			foundRamesh = true
			if v["adminDiscountCouponCode"] != "ALLHALLSDIWALI" {
				t.Errorf("expected adminDiscountCouponCode 'ALLHALLSDIWALI', got %v", v["adminDiscountCouponCode"])
			}
			if v["adminDiscountPrice"] != 18750.0 {
				t.Errorf("expected adminDiscountPrice 18750, got %v", v["adminDiscountPrice"])
			}
			if v["adminDiscountPercentage"] != 15.0 {
				t.Errorf("expected adminDiscountPercentage 15, got %v", v["adminDiscountPercentage"])
			}
			if _, exists := v["superAdminDiscountCouponCode"]; exists {
				t.Errorf("superAdminDiscountCouponCode should NOT exist in response")
			}
			if _, exists := v["superAdminDiscountPrice"]; exists {
				t.Errorf("superAdminDiscountPrice should NOT exist in response")
			}
			if _, exists := v["superAdminDiscountPercentage"]; exists {
				t.Errorf("superAdminDiscountPercentage should NOT exist in response")
			}
		}
	}
	if !foundRamesh {
		t.Errorf("Ramesh Grand Premium Palace not found in response")
	}
}

func TestCreateFacilityImageValidation(t *testing.T) {
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	token, _ := signer.Generate("admin@example.com", "1", "ROLE_ADMIN")
	mux := http.NewServeMux()
	h := &Handler{signer: signer}
	h.Register(mux)

	t.Run("Rejects facility creation when no images provided", func(t *testing.T) {
		payload := `{"name":"Test Hall Palace","type":"HALL"}`
		req := httptest.NewRequest("POST", "/api/v1/facilities", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request when no images provided, got %d: %s", rr.Code, rr.Body.String())
		}

		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		msg, _ := body["message"].(string)
		if !strings.Contains(msg, "At least one image is required") {
			t.Errorf("expected message to mention 'At least one image is required', got %q", msg)
		}
	})

	t.Run("Rejects hall alias when no images provided", func(t *testing.T) {
		payload := `{"name":"Test Hall Palace"}`
		req := httptest.NewRequest("POST", "/api/v1/halls", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request on /api/v1/halls when no images, got %d: %s", rr.Code, rr.Body.String())
		}
	})
}
