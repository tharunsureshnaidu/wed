package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/migrations"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/config"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
)

func main() {
	// Command line flags (defaults come directly from constants.go)
	hallsCount := flag.Int("halls", MarriageHallCount, "Number of marriage halls to seed")
	hotelsCount := flag.Int("hotels", HotelCount, "Number of hotels to seed")
	bookingsCount := flag.Int("bookings", UserBookingsCount, "Number of bookings for demo user")
	favsCount := flag.Int("favourites", UserFavouritesCount, "Number of favourites for demo user")
	reviewsCount := flag.Int("reviews", UserReviewsCount, "Number of reviews for demo user")
	faqsCount := flag.Int("faqs", FaqCount, "Number of platform FAQs to seed")
	dbURL := flag.String("db", "", "PostgreSQL database URL override")
	flag.Parse()

	ctx := context.Background()

	// Load configuration and connect
	cfg := config.Load()
	url := cfg.DatabaseURL
	if *dbURL != "" {
		url = *dbURL
	}

	fmt.Println("=================================================================")
	fmt.Println("🚀 ApnaVenue Database Seeder Starting...")
	fmt.Println("=================================================================")
	fmt.Printf("Connecting to database: %s\n", maskDBURL(url))

	db, err := database.New(ctx, url)
	if err != nil {
		fmt.Printf("❌ Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Ensure all migrations are applied first
	fmt.Println("Applying database migrations if pending...")
	if err := database.Migrate(ctx, db, migrations.FS, "."); err != nil {
		fmt.Printf("❌ Migration error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Migrations up to date.")

	// 1. Setup Roles
	fmt.Println("\n[1/8] Verifying roles in database...")
	roleCustomer, err := ensureRoles(ctx, db)
	if err != nil {
		fmt.Printf("❌ Failed to verify roles: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Roles ready (ROLE_CUSTOMER, ROLE_ADMIN, ROLE_HALL_OWNER).")

	// 2. Setup Venue Owner / Vendor Account
	fmt.Println("\n[2/8] Setting up Venue Owner / Vendor account...")
	ownerID, err := ensureVendorOwner(ctx, db)
	if err != nil {
		fmt.Printf("❌ Failed to ensure vendor owner: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Vendor owner ready (User ID: %d).\n", ownerID)

	// 3. Setup Demo User Account (demo@gmail.com / demo@123)
	fmt.Println("\n[3/8] Setting up Demo Customer account...")
	demoUserID, err := setupDemoUser(ctx, db, roleCustomer)
	if err != nil {
		fmt.Printf("❌ Failed to setup demo user: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Demo user verified & active: %s (Password: %s, User ID: %d)\n", DemoUserEmail, DemoUserPassword, demoUserID)

	// Fetch available amenities
	amenityIDs, err := getAmenityMap(ctx, db)
	if err != nil {
		fmt.Printf("❌ Failed to load amenities: %v\n", err)
		os.Exit(1)
	}

	// 4. Seed Marriage Halls
	fmt.Printf("\n[4/8] Seeding %d Marriage Halls...\n", *hallsCount)
	hallIDs, err := seedMarriageHalls(ctx, db, ownerID, *hallsCount, amenityIDs)
	if err != nil {
		fmt.Printf("❌ Failed to seed marriage halls: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Successfully created/updated %d marriage halls.\n", len(hallIDs))

	// 5. Seed Hotels
	fmt.Printf("\n[5/8] Seeding %d Hotels...\n", *hotelsCount)
	hotelIDs, err := seedHotels(ctx, db, ownerID, *hotelsCount, amenityIDs)
	if err != nil {
		fmt.Printf("❌ Failed to seed hotels: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Successfully created/updated %d hotels.\n", len(hotelIDs))

	// 6. Seed User Data for Demo User (Bookings, Favourites, Reviews)
	fmt.Println("\n[6/8] Seeding demo user data for NEW_APIS_POSTMAN_COLLECTION...")
	if err := seedDemoUserData(ctx, db, demoUserID, hallIDs, hotelIDs, *bookingsCount, *favsCount, *reviewsCount); err != nil {
		fmt.Printf("❌ Failed to seed demo user data: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Populated Bookings (%d), Favourites (%d), and Reviews (%d).\n", *bookingsCount, *favsCount, *reviewsCount)

	// 7. Seed Help Center, Feedback & Recent Search Events
	fmt.Println("\n[7/8] Seeding Help Center messages, App Feedback & Recent Searches...")
	if err := seedSupportAndSearches(ctx, db, demoUserID, hallIDs); err != nil {
		fmt.Printf("❌ Failed to seed support & search events: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Help messages, App feedback, and Search events recorded.")

	// 8. Seed Platform FAQs
	fmt.Printf("\n[8/8] Seeding %d Platform FAQs...\n", *faqsCount)
	if err := seedPlatformFAQs(ctx, db, *faqsCount); err != nil {
		fmt.Printf("❌ Failed to seed FAQs: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %d Platform FAQs seeded.\n", *faqsCount)

	fmt.Println("\n=================================================================")
	fmt.Println("🎉 DATABASE POPULATION COMPLETE!")
	fmt.Println("=================================================================")
	fmt.Printf("• User Account : %s\n", DemoUserEmail)
	fmt.Printf("• Password     : %s\n", DemoUserPassword)
	fmt.Printf("• Venues       : %d Marriage Halls + %d Hotels\n", len(hallIDs), len(hotelIDs))
	fmt.Printf("• Test Venue ID: %s (Default in Postman collection)\n", DefaultVenueID)
	fmt.Printf("• Test FAQ ID  : %s (Default in Postman collection)\n", DefaultFaqID)
	fmt.Println("All 15 APIs in NEW_APIS_POSTMAN_COLLECTION are now ready to test!")
	fmt.Println("=================================================================")
}

// -----------------------------------------------------------------------------
// Database Helper Implementations
// -----------------------------------------------------------------------------

func ensureRoles(ctx context.Context, db *pgxpool.Pool) (int, error) {
	roles := []string{"ROLE_CUSTOMER", "ROLE_ADMIN", "ROLE_SUPER_ADMIN", "ROLE_HALL_OWNER"}
	for _, r := range roles {
		_, err := db.Exec(ctx, `INSERT INTO roles (role_name) VALUES ($1) ON CONFLICT (role_name) DO NOTHING`, r)
		if err != nil {
			return 0, err
		}
	}
	var custRoleID int
	err := db.QueryRow(ctx, `SELECT id FROM roles WHERE role_name = 'ROLE_CUSTOMER'`).Scan(&custRoleID)
	return custRoleID, err
}

func ensureVendorOwner(ctx context.Context, db *pgxpool.Pool) (int64, error) {
	ownerEmail := "vendor@apnavenue.com"
	hash, err := bcrypt.GenerateFromPassword([]byte("Vendor@123"), 12)
	if err != nil {
		return 0, err
	}

	var ownerID int64
	err = db.QueryRow(ctx, `
		INSERT INTO users (full_name, email, password_hash, status, is_email_verified, is_phone_verified)
		VALUES ('ApnaVenue Venue Partner', $1, $2, 'ACTIVE', TRUE, TRUE)
		ON CONFLICT (email) DO UPDATE
		SET status = 'ACTIVE', is_email_verified = TRUE, updated_at = CURRENT_TIMESTAMP
		RETURNING id`, ownerEmail, string(hash)).Scan(&ownerID)
	if err != nil {
		return 0, err
	}

	// Assign ROLE_HALL_OWNER
	_, _ = db.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, r.id FROM roles r WHERE r.role_name = 'ROLE_HALL_OWNER'
		ON CONFLICT DO NOTHING`, ownerID)

	// User Profile
	_, _ = db.Exec(ctx, `
		INSERT INTO user_profiles (id, first_name, last_name, bio)
		VALUES ($1, 'ApnaVenue', 'Host', 'Official ApnaVenue Verified Property Partner')
		ON CONFLICT (id) DO NOTHING`, ownerID)

	// Vendor Profile
	_, _ = db.Exec(ctx, `
		INSERT INTO vendors (user_id, business_name, business_address, business_description, kyc_status)
		VALUES ($1, 'ApnaVenue Premier Partner Network', 'MG Road, Bengaluru, Karnataka', 'Verified hospitality & banquet vendor', 'APPROVED')
		ON CONFLICT (user_id) DO NOTHING`, ownerID)

	return ownerID, nil
}

func setupDemoUser(ctx context.Context, db *pgxpool.Pool, custRoleID int) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(DemoUserPassword), 12)
	if err != nil {
		return 0, err
	}

	var userID int64
	err = db.QueryRow(ctx, `
		INSERT INTO users (full_name, email, phone_number, password_hash, status, is_email_verified, is_phone_verified)
		VALUES ($1, $2, $3, $4, 'ACTIVE', TRUE, TRUE)
		ON CONFLICT (email) DO UPDATE
		SET password_hash = EXCLUDED.password_hash,
		    status = 'ACTIVE',
		    is_email_verified = TRUE,
		    is_phone_verified = TRUE,
		    failed_login_attempts = 0,
		    account_locked_until = NULL,
		    is_deleted = FALSE,
		    updated_at = CURRENT_TIMESTAMP
		RETURNING id`, DemoUserName, DemoUserEmail, DemoUserPhone, string(hash)).Scan(&userID)
	if err != nil {
		return 0, err
	}

	// Strictly keep ONLY ROLE_CUSTOMER for demo@gmail.com
	// (so booking check isCustomer passes without forbidden error)
	_, _ = db.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID)
	_, err = db.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, custRoleID)
	if err != nil {
		return 0, err
	}

	// Ensure user profile exists
	_, err = db.Exec(ctx, `
		INSERT INTO user_profiles (id, first_name, last_name, bio, kyc_status)
		VALUES ($1, 'Demo', 'Customer', 'Frequent event planner and wedding booker.', 'APPROVED')
		ON CONFLICT (id) DO UPDATE
		SET first_name = 'Demo', last_name = 'Customer', updated_at = CURRENT_TIMESTAMP`, userID)
	if err != nil {
		return 0, err
	}

	return userID, nil
}

func getAmenityMap(ctx context.Context, db *pgxpool.Pool) (map[string]string, error) {
	rows, err := db.Query(ctx, `SELECT name, id::text FROM amenities`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]string)
	for rows.Next() {
		var name, id string
		if err := rows.Scan(&name, &id); err == nil {
			res[name] = id
		}
	}
	return res, nil
}

// -----------------------------------------------------------------------------
// Seed Marriage Halls
// -----------------------------------------------------------------------------

var hallNames = []string{
	"Rajesh Hall Palace", // Exactly matches Postman query test: search=Rajesh Hall Palace
	"Grand Royal Mandapam & Convention",
	"Venkateshwara Kalyana Mantapa",
	"The Emerald Peacock Grand Ballroom",
	"Sri Krishna Convention Centre",
	"Shagun Celebration & Banquet Hall",
	"Mayura Kalyana Vedika Palace",
	"Leela Palace Convention Hall",
	"Sagar Grand Royal Banquets",
	"Utsav Majestic Wedding Grounds",
	"Seven Seas Royal Banquet",
	"Golden Feather Celebration Palace",
	"Sanskriti Heritage Mandapam",
	"Swastik Divine Grand Hall",
	"Ashirwad Royal Wedding Arena",
}

var hallImages = []string{
	"https://images.unsplash.com/photo-1519167758481-83f550bb49b3?auto=format&fit=crop&w=1200&q=80",
	"https://images.unsplash.com/photo-1511795409834-ef04bbd61622?auto=format&fit=crop&w=1200&q=80",
	"https://images.unsplash.com/photo-1464366400600-7168b8af9bc3?auto=format&fit=crop&w=1200&q=80",
	"https://images.unsplash.com/photo-1545232979-8bf68ee9b1af?auto=format&fit=crop&w=1200&q=80",
}

func seedMarriageHalls(ctx context.Context, db *pgxpool.Pool, ownerID int64, count int, amenities map[string]string) ([]string, error) {
	var ids []string

	for i := 1; i <= count; i++ {
		// 1st hall uses DefaultVenueID, 2nd uses SecondVenueID, rest generate deterministic/random UUIDs
		venueUUID := DefaultVenueID
		if i == 2 {
			venueUUID = SecondVenueID
		} else if i > 2 {
			venueUUID = fmt.Sprintf("b24d1e6c-4258-4262-81c2-%012d", i)
		}

		name := fmt.Sprintf("Grand Hall %d", i)
		if i-1 < len(hallNames) {
			name = hallNames[i-1]
		}

		// Coordinates around Bengaluru Center
		lat := DefaultLat + float64(i)*0.0035
		lng := DefaultLng + float64(i)*0.0025

		capacityPax := 300 + i*50
		seatingCap := 200 + i*30
		floatingCap := capacityPax + 150
		basePrice := 150000.00 + float64(i)*15000.00
		areaSqft := 5000 + i*500

		desc := fmt.Sprintf("Premier luxury marriage hall located in %s with world-class facilities, bridal suites, and grand stage.", DefaultCity)

		// Insert or update facility
		_, err := db.Exec(ctx, `
			INSERT INTO facilities (
				id, owner_id, name, description, type, city, full_address, state, zipcode, country,
				lat, lng, status, is_verified, is_featured, avg_rating, review_count, is_deleted,
				capacity_pax, area_sqft, base_price_per_day, seating_capacity, floating_capacity, min_booking_size
			) VALUES (
				$1::uuid, $2, $3, $4, 'MARRIAGE_HALL', $5, 'Bannerghatta Main Road', 'Karnataka', '560076', 'India',
				$6, $7, 'APPROVED', TRUE, TRUE, 4.8, 12, FALSE,
				$8, $9, $10, $11, $12, 100
			)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				status = 'APPROVED',
				is_verified = TRUE,
				is_featured = TRUE,
				capacity_pax = EXCLUDED.capacity_pax,
				base_price_per_day = EXCLUDED.base_price_per_day,
				updated_at = CURRENT_TIMESTAMP`,
			venueUUID, ownerID, name, desc, DefaultCity,
			lat, lng, capacityPax, areaSqft, totalPrice(basePrice), seatingCap, floatingCap)
		if err != nil {
			return nil, fmt.Errorf("insert hall %s: %w", name, err)
		}

		// Facility Images
		_, _ = db.Exec(ctx, `DELETE FROM facility_images WHERE facility_id = $1::uuid`, venueUUID)
		for imgIdx, imgURL := range hallImages {
			_, _ = db.Exec(ctx, `
				INSERT INTO facility_images (facility_id, url, is_cover, sort_order)
				VALUES ($1::uuid, $2, $3, $4)`,
				venueUUID, imgURL, imgIdx == 0, imgIdx)
		}

		// Facility Events
		events := []string{"WEDDING", "RECEPTION", "ENGAGEMENT", "ANNIVERSARY", "BIRTHDAY"}
		for _, ev := range events {
			_, _ = db.Exec(ctx, `
				INSERT INTO facility_events (facility_id, event_code)
				VALUES ($1::uuid, $2) ON CONFLICT DO NOTHING`,
				venueUUID, ev)
		}

		// Facility Amenities
		hallAmenities := []string{
			"Parking", "Air Conditioning", "WiFi", "Power Backup", "Stage",
			"DJ / Sound System", "Bride & Groom Rooms", "Catering", "Lift", "CCTV Security",
		}
		for _, amName := range hallAmenities {
			if amID, ok := amenities[amName]; ok {
				_, _ = db.Exec(ctx, `
					INSERT INTO facility_amenities (facility_id, amenity_id)
					VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
					venueUUID, amID)
			}
		}

		// Facility Policies
		_, _ = db.Exec(ctx, `DELETE FROM facility_policies WHERE facility_id = $1::uuid`, venueUUID)
		_, _ = db.Exec(ctx, `
			INSERT INTO facility_policies (facility_id, policy_type, description)
			VALUES 
				($1::uuid, 'CATERING', 'In-house and approved external catering permitted.'),
				($1::uuid, 'DECORATION', 'Dedicated in-house decorators available with custom wedding themes.'),
				($1::uuid, 'ALCOHOL', 'Permitted with state excise event licence.'),
				($1::uuid, 'CANCELLATION', 'Full refund if cancelled 30 days prior to event date.')`,
			venueUUID)

		// Hall Packages
		_, _ = db.Exec(ctx, `DELETE FROM hall_packages WHERE facility_id = $1::uuid`, venueUUID)
		_, _ = db.Exec(ctx, `
			INSERT INTO hall_packages (facility_id, name, description, price, guest_capacity, includes_catering)
			VALUES 
				($1::uuid, 'Silver Classic Package', 'Hall booking + basic lighting + stage setup', $2, $3, FALSE),
				($1::uuid, 'Royal Maharaja Package', 'Hall + full AC + luxury floral decor + DJ setup', $4, $5, TRUE)`,
			venueUUID, totalPrice(basePrice), seatingCap, totalPrice(basePrice*1.5), floatingCap)

		// Hall Availability (Upcoming 30 days)
		now := time.Now()
		for d := 1; d <= 30; d++ {
			dateStr := now.AddDate(0, 0, d).Format("2006-01-02")
			for _, slot := range []string{"MORNING", "EVENING", "FULL_DAY"} {
				_, _ = db.Exec(ctx, `
					INSERT INTO hall_availability (facility_id, date, slot_type, status)
					VALUES ($1::uuid, $2, $3, 'AVAILABLE')
					ON CONFLICT (facility_id, date, slot_type) DO NOTHING`,
					venueUUID, dateStr, slot)
			}
		}

		// Venue FAQs
		_, _ = db.Exec(ctx, `DELETE FROM facility_faqs WHERE facility_id = $1::uuid`, venueUUID)
		_, _ = db.Exec(ctx, `
			INSERT INTO facility_faqs (facility_id, question, answer, sort_order)
			VALUES 
				($1::uuid, 'What is the parking capacity of this hall?', 'The venue has dedicated valet and basement parking for over 150 four-wheelers.', 1),
				($1::uuid, 'Is outside decoration allowed?', 'Yes, outside floral and lighting decorators are allowed with prior management approval.', 2)`,
			venueUUID)

		ids = append(ids, venueUUID)
	}

	return ids, nil
}

// -----------------------------------------------------------------------------
// Seed Hotels
// -----------------------------------------------------------------------------

var hotelNames = []string{
	"The Grand Orchid Hotel & Convention",
	"ITC Gardenia Luxury Hotel & Suites",
	"JW Marriott Executive Hotel",
	"The Leela Palace Hotel Bangalore",
	"Taj Vivanta Luxury Resort",
	"Radisson Blu City Center Hotel",
	"Hilton Embassy GolfLinks Hotel",
	"The Ritz Carlton Luxury Suites",
	"Sheraton Grand Banquet & Hotel",
	"Hyatt Centric MG Road Hotel",
	"Novotel Techpark Hotel",
	"Shangri-La Horizon Hotel",
	"Oakwood Premier Prestige Suites",
	"The Chancery Pavilion Hotel",
	"Courtyard by Marriott City Suites",
}

var hotelImages = []string{
	"https://images.unsplash.com/photo-1566073771259-6a8506099945?auto=format&fit=crop&w=1200&q=80",
	"https://images.unsplash.com/photo-1582719508461-905c673771fd?auto=format&fit=crop&w=1200&q=80",
	"https://images.unsplash.com/photo-1590490360182-c33d57733427?auto=format&fit=crop&w=1200&q=80",
	"https://images.unsplash.com/photo-1578683010236-d716f9a3f461?auto=format&fit=crop&w=1200&q=80",
}

func seedHotels(ctx context.Context, db *pgxpool.Pool, ownerID int64, count int, amenities map[string]string) ([]string, error) {
	var ids []string

	for i := 1; i <= count; i++ {
		hotelUUID := fmt.Sprintf("d46f3a8e-647a-6484-93e4-%012d", i)

		name := fmt.Sprintf("Grand Hotel %d", i)
		if i-1 < len(hotelNames) {
			name = hotelNames[i-1]
		}

		lat := DefaultLat - float64(i)*0.0030
		lng := DefaultLng - float64(i)*0.0020

		starRating := 4
		if i%2 == 0 {
			starRating = 5
		}

		desc := fmt.Sprintf("5-star luxury hotel in %s featuring executive suites, infinity pool, 24/7 dining, and grand banquet facilities.", DefaultCity)

		// Insert or update hotel facility
		_, err := db.Exec(ctx, `
			INSERT INTO facilities (
				id, owner_id, name, description, type, city, full_address, state, zipcode, country,
				lat, lng, status, is_verified, is_featured, avg_rating, review_count, is_deleted,
				star_rating, check_in_time, check_out_time, base_price_per_day
			) VALUES (
				$1::uuid, $2, $3, $4, 'HOTEL', $5, 'Residency Road', 'Karnataka', '560025', 'India',
				$6, $7, 'APPROVED', TRUE, TRUE, 4.9, 18, FALSE,
				$8, '14:00', '11:00', $9
			)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				status = 'APPROVED',
				is_verified = TRUE,
				is_featured = TRUE,
				star_rating = EXCLUDED.star_rating,
				base_price_per_day = EXCLUDED.base_price_per_day,
				updated_at = CURRENT_TIMESTAMP`,
			hotelUUID, ownerID, name, desc, DefaultCity,
			lat, lng, starRating, 4500.00+float64(i)*500.00)
		if err != nil {
			return nil, fmt.Errorf("insert hotel %s: %w", name, err)
		}

		// Hotel Images
		_, _ = db.Exec(ctx, `DELETE FROM facility_images WHERE facility_id = $1::uuid`, hotelUUID)
		for imgIdx, imgURL := range hotelImages {
			_, _ = db.Exec(ctx, `
				INSERT INTO facility_images (facility_id, url, is_cover, sort_order)
				VALUES ($1::uuid, $2, $3, $4)`,
				hotelUUID, imgURL, imgIdx == 0, imgIdx)
		}

		// Hotel Amenities
		hotelAmenities := []string{
			"Parking", "Air Conditioning", "WiFi", "Restaurant", "Room Service",
			"Swimming Pool", "Gym / Fitness Center", "Spa", "Breakfast Included", "24/7 Front Desk",
		}
		for _, amName := range hotelAmenities {
			if amID, ok := amenities[amName]; ok {
				_, _ = db.Exec(ctx, `
					INSERT INTO facility_amenities (facility_id, amenity_id)
					VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
					hotelUUID, amID)
			}
		}

		// Room Types
		_, _ = db.Exec(ctx, `DELETE FROM room_types WHERE facility_id = $1::uuid`, hotelUUID)
		var deluxeRoomID, suiteRoomID string
		_ = db.QueryRow(ctx, `
			INSERT INTO room_types (facility_id, name, description, capacity_adults, capacity_children, base_price_per_night, total_rooms)
			VALUES ($1::uuid, 'Deluxe King Room', 'Spacious luxury bedroom with city view and king bed.', 2, 1, 4500.00, 15)
			RETURNING id::text`, hotelUUID).Scan(&deluxeRoomID)

		_ = db.QueryRow(ctx, `
			INSERT INTO room_types (facility_id, name, description, capacity_adults, capacity_children, base_price_per_night, total_rooms)
			VALUES ($1::uuid, 'Executive Club Suite', 'Luxury suite with private lounge access, jacuzzi, and complimentary breakfast.', 3, 2, 8500.00, 8)
			RETURNING id::text`, hotelUUID).Scan(&suiteRoomID)

		// Room Availability (Next 14 days)
		now := time.Now()
		for d := 1; d <= 14; d++ {
			dateStr := now.AddDate(0, 0, d).Format("2006-01-02")
			if deluxeRoomID != "" {
				_, _ = db.Exec(ctx, `
					INSERT INTO room_availability (room_type_id, date, total_rooms, booked_rooms)
					VALUES ($1::uuid, $2, 15, 0) ON CONFLICT (room_type_id, date) DO NOTHING`,
					deluxeRoomID, dateStr)
			}
			if suiteRoomID != "" {
				_, _ = db.Exec(ctx, `
					INSERT INTO room_availability (room_type_id, date, total_rooms, booked_rooms)
					VALUES ($1::uuid, $2, 8, 0) ON CONFLICT (room_type_id, date) DO NOTHING`,
					suiteRoomID, dateStr)
			}
		}

		// Hotel Policies
		_, _ = db.Exec(ctx, `DELETE FROM facility_policies WHERE facility_id = $1::uuid`, hotelUUID)
		_, _ = db.Exec(ctx, `
			INSERT INTO facility_policies (facility_id, policy_type, description)
			VALUES 
				($1::uuid, 'CHECK_IN', 'Standard check-in is 2:00 PM. Early check-in subject to availability.'),
				($1::uuid, 'CANCELLATION', 'Free cancellation up to 24 hours prior to arrival.')`,
			hotelUUID)

		ids = append(ids, hotelUUID)
	}

	return ids, nil
}

// -----------------------------------------------------------------------------
// Seed Demo User Data (Bookings, Favourites, Reviews)
// -----------------------------------------------------------------------------

func seedDemoUserData(ctx context.Context, db *pgxpool.Pool, userID int64, halls, hotels []string, bCount, fCount, rCount int) error {
	now := time.Now()

	// 1. Favourites (Both Halls & Hotels)
	_, _ = db.Exec(ctx, `DELETE FROM favourite_facilities WHERE user_profile_id = $1`, userID)
	addedFavs := 0
	for i := 0; i < len(halls) && addedFavs < fCount/2; i++ {
		_, _ = db.Exec(ctx, `
			INSERT INTO favourite_facilities (user_profile_id, facility_id)
			VALUES ($1, $2::uuid) ON CONFLICT DO NOTHING`,
			userID, halls[i])
		addedFavs++
	}
	for i := 0; i < len(hotels) && addedFavs < fCount; i++ {
		_, _ = db.Exec(ctx, `
			INSERT INTO favourite_facilities (user_profile_id, facility_id)
			VALUES ($1, $2::uuid) ON CONFLICT DO NOTHING`,
			userID, hotels[i])
		addedFavs++
	}

	// 2. Bookings (Upcoming and Past)
	_, _ = db.Exec(ctx, `DELETE FROM bookings WHERE user_id = $1`, userID)
	for i := 1; i <= bCount; i++ {
		isHall := i%2 != 0
		var targetID string
		targetType := "HALL"
		amount := 185000.00
		if isHall && len(halls) > 0 {
			targetID = halls[(i-1)%len(halls)]
			targetType = "HALL"
			amount = 220000.00
		} else if len(hotels) > 0 {
			targetID = hotels[(i-1)%len(hotels)]
			targetType = "HOTEL"
			amount = 9000.00
		} else {
			continue
		}

		checkIn := now.AddDate(0, 0, i*7)
		checkOut := checkIn.AddDate(0, 0, 1)
		status := "CONFIRMED"
		if i == 1 {
			status = "PENDING"
		} else if i == bCount {
			status = "COMPLETED"
			checkIn = now.AddDate(0, -1, -i)
			checkOut = checkIn.AddDate(0, 0, 1)
		}

		var bookingID string
		err := db.QueryRow(ctx, `
			INSERT INTO bookings (
				user_id, target_type, target_id, check_in, check_out, slot_type,
				total_amount, paid_amount, status, guest_name, guest_email, guest_phone
			) VALUES (
				$1, $2, $3::uuid, $4, $5, 'FULL_DAY',
				$6, $6, $7, $8, $9, $10
			) RETURNING id::text`,
			userID, targetType, targetID, checkIn.Format("2006-01-02"), checkOut.Format("2006-01-02"),
			amount, status, DemoUserName, DemoUserEmail, DemoUserPhone).Scan(&bookingID)
		if err == nil && targetType == "HOTEL" {
			// Link room
			var roomTypeID string
			if err := db.QueryRow(ctx, `SELECT id FROM room_types WHERE facility_id = $1::uuid LIMIT 1`, targetID).Scan(&roomTypeID); err == nil {
				_, _ = db.Exec(ctx, `
					INSERT INTO booking_rooms (booking_id, room_type_id, quantity, price_at_booking)
					VALUES ($1::uuid, $2::uuid, 1, 4500.00)`, bookingID, roomTypeID)
			}
		}
	}

	// 3. Reviews (With status = 'APPROVED')
	_, _ = db.Exec(ctx, `DELETE FROM reviews WHERE user_id = $1`, userID)
	reviewComments := []string{
		"Absolutely stunning marriage hall! The stage decoration and lighting exceeded our expectations.",
		"Had an amazing stay at the hotel. Excellent room service and pristine swimming pool.",
		"The banquet hall capacity was perfect for our 500+ guests. Valet parking was flawless.",
		"Very cooperative staff, high level of hygiene, and delicious in-house catering.",
		"Seamless booking experience on ApnaVenue! Highly recommend this venue for wedding receptions.",
	}

	allVenues := append(halls, hotels...)
	for i := 0; i < len(allVenues) && i < rCount; i++ {
		comm := reviewComments[i%len(reviewComments)]
		title := "Exceptional Experience!"
		rating := 5
		if i%2 == 1 {
			rating = 4
			title = "Great Ambience and Service"
		}

		_, _ = db.Exec(ctx, `
			INSERT INTO reviews (facility_id, user_id, rating, title, comment, status)
			VALUES ($1::uuid, $2, $3, $4, $5, 'APPROVED')
			ON CONFLICT (user_id, facility_id) DO UPDATE
			SET rating = EXCLUDED.rating, comment = EXCLUDED.comment, status = 'APPROVED'`,
			allVenues[i], userID, rating, title, comm)

		// Recalculate facility avg_rating
		_, _ = db.Exec(ctx, `
			UPDATE facilities f
			SET avg_rating = COALESCE((SELECT round(avg(rating)::numeric, 1) FROM reviews r WHERE r.facility_id = f.id AND r.status = 'APPROVED'), 4.8),
			    review_count = (SELECT count(*) FROM reviews r WHERE r.facility_id = f.id AND r.status = 'APPROVED')
			WHERE f.id = $1::uuid`, allVenues[i])
	}

	return nil
}

// -----------------------------------------------------------------------------
// Seed Support, Feedback & Recent Search Events
// -----------------------------------------------------------------------------

func seedSupportAndSearches(ctx context.Context, db *pgxpool.Pool, userID int64, halls []string) error {
	// Help Center Messages
	_, _ = db.Exec(ctx, `DELETE FROM help_center_messages WHERE user_id = $1`, userID)
	helpMsgs := []string{
		"Hello, I need an invoice copy for my upcoming wedding hall reservation.",
		"Is it possible to extend the checkout time by 2 hours for our banquet hall?",
		"Do you offer event insurance for large marriage functions?",
	}
	for _, m := range helpMsgs {
		_, _ = db.Exec(ctx, `
			INSERT INTO help_center_messages (user_id, user_name, user_email, user_phone, message, status)
			VALUES ($1, $2, $3, $4, $5, 'NEW')`,
			userID, DemoUserName, DemoUserEmail, DemoUserPhone, m)
	}

	// App Feedback
	_, _ = db.Exec(ctx, `DELETE FROM app_feedback WHERE user_id = $1`, userID)
	feedbacks := []string{
		"Booking flow was smooth and venue comparison feature is super helpful!",
		"Great app! Filters for price and capacity make finding banquet halls effortless.",
		"Loved the verified venue photos and high-resolution hall view.",
	}
	for idx, fb := range feedbacks {
		_, _ = db.Exec(ctx, `
			INSERT INTO app_feedback (user_id, rating, message, app_version, platform, status)
			VALUES ($1, $2, $3, '1.4.2', 'ANDROID', 'NEW')`,
			userID, 5-(idx%2), fb)
	}

	// Search Events (for Trending & Search Page)
	searchQueries := []string{
		"Rajesh Hall Palace",
		"Bengaluru Marriage Halls",
		"Grand Royal Mandapam",
		"Banquet Halls with AC",
		"Luxury Wedding Resort",
		"5 Star Hotel Banquet",
	}
	var clickVenue string
	if len(halls) > 0 {
		clickVenue = halls[0]
	}

	for _, q := range searchQueries {
		_, _ = db.Exec(ctx, `
			INSERT INTO search_events (user_id, query_text, result_count, clicked_venue_id, time_taken_ms, city)
			VALUES ($1, $2, 12, $3::uuid, 42, $4)`,
			fmt.Sprintf("%d", userID), q, clickVenue, DefaultCity)
	}

	return nil
}

// -----------------------------------------------------------------------------
// Seed Platform FAQs
// -----------------------------------------------------------------------------

func seedPlatformFAQs(ctx context.Context, db *pgxpool.Pool, count int) error {
	faqs := []struct {
		Q string
		A string
	}{
		{
			Q: "How can I book a marriage hall on ApnaVenue?",
			A: "<p>To book a marriage hall, search available venues by city, choose your event dates and guest size, review packages, and click <strong>Book Now</strong> to place your reservation.</p>",
		},
		{
			Q: "Can I compare multiple marriage halls and hotels side-by-side?",
			A: "<p>Yes! Use our <strong>Compare Screen</strong> feature to select up to 3 venues and compare prices, capacities, amenities, and policies instantly.</p>",
		},
		{
			Q: "What payment methods are supported on ApnaVenue?",
			A: "<p>We support all major payment modes including UPI (Google Pay, PhonePe, Paytm), Net Banking, Debit/Credit Cards, and structured milestone deposits.</p>",
		},
		{
			Q: "What is the cancellation and refund policy?",
			A: "<p>Cancellation terms depend on the venue owner's policy. Most verified banquet halls provide 100% refund when cancelled at least 30 days prior to the event.</p>",
		},
		{
			Q: "Are outside decorators and caterers allowed?",
			A: "<p>Each venue detail page lists its in-house vs. outside catering policy. Many halls permit certified outside caterers upon payment of kitchen usage fees.</p>",
		},
		{
			Q: "How do I schedule an in-person venue visit?",
			A: "<p>You can request a venue visit directly through our customer care support tab or by submitting an inquiry message under the venue detail page.</p>",
		},
		{
			Q: "Are all venues on ApnaVenue verified?",
			A: "<p>Yes, all listed properties undergo physical verification of amenities, licences, safety compliance, and seating capacities by our operations team.</p>",
		},
		{
			Q: "Can I book hotel rooms along with the banquet hall for wedding guests?",
			A: "<p>Yes, ApnaVenue supports unified booking for combined marriage halls and residential guest rooms in one booking flow.</p>",
		},
		{
			Q: "How do I apply discount coupons to my booking?",
			A: "<p>On the booking checkout screen, enter your promo code in the coupon field and click Apply to instantly redeem discounts on your total amount.</p>",
		},
		{
			Q: "How can I contact customer support if I face an issue?",
			A: "<p>You can reach our dedicated support team via the Help & Support screen in the app, or email us directly at support@apnavenue.com.</p>",
		},
	}

	for i := 1; i <= count && i <= len(faqs); i++ {
		faqUUID := DefaultFaqID
		if i > 1 {
			faqUUID = fmt.Sprintf("f585a0fa-75cb-4670-bc90-%012d", i)
		}

		item := faqs[i-1]
		_, err := db.Exec(ctx, `
			INSERT INTO faqs (id, question, answer, display_order, is_active, is_deleted)
			VALUES ($1::uuid, $2, $3, $4, TRUE, FALSE)
			ON CONFLICT (id) DO UPDATE SET
				question = EXCLUDED.question,
				answer = EXCLUDED.answer,
				display_order = EXCLUDED.display_order,
				is_active = TRUE,
				is_deleted = FALSE,
				updated_at = CURRENT_TIMESTAMP`,
			faqUUID, item.Q, item.A, i)
		if err != nil {
			return err
		}
	}

	return nil
}

func totalPrice(val float64) float64 {
	return float64(int(val*100)) / 100
}

func maskDBURL(u string) string {
	parts := strings.Split(u, "@")
	if len(parts) < 2 {
		return u
	}
	return "postgres://***:***@" + parts[1]
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
