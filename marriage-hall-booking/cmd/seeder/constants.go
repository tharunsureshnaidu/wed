package main

// =============================================================================
// ⚙️ SEEDER CONFIGURATION & CONSTANTS
//
// Yahan se aap kisi bhi API ya entity ka count (number) manually badal sakte hain.
// Jaise 10 Marriage Halls ko 20 karna ho, ya Hotels ko 15 karna ho, bas yahan number
// change karke command chalayein:
//
//     go run ./cmd/seeder
//
// =============================================================================

const (
	// -------------------------------------------------------------------------
	// 👤 1. Demo User Account Credentials
	// -------------------------------------------------------------------------
	DemoUserEmail    = "demo@gmail.com"
	DemoUserPassword = "demo@123"
	DemoUserName     = "Demo User"
	DemoUserPhone    = "+919876543210"

	// -------------------------------------------------------------------------
	// 🏢 2. Venue Counts (Hotels & Marriage Halls)
	// Change these numbers whenever you want more or fewer venues in DB
	// -------------------------------------------------------------------------
	MarriageHallCount = 10 // 👈 Set to 20, 30, etc. anytime!
	HotelCount        = 10 // 👈 Set to 20, 30, etc. anytime!

	// -------------------------------------------------------------------------
	// 📱 3. Postman Collection APIs Data Counts (for demo@gmail.com)
	// -------------------------------------------------------------------------
	UserBookingsCount       = 5  // /api/v1/bookings (Upcoming & Past bookings)
	UserFavouritesCount     = 6  // /api/v1/users/me/favourites (Halls & Hotels)
	UserReviewsCount        = 5  // /api/v1/reviews/my-reviews (Customer reviews)
	RecentSearchEventsCount = 6  // /api/v1/search/recent & trending queries
	HelpCenterMsgCount      = 3  // /api/v1/help/messages (Support tickets)
	AppFeedbackCount        = 3  // /api/v1/feedback (App reviews & ratings)
	FaqCount                = 10 // /api/v1/public/faqs & /api/v1/admin/faqs

	// -------------------------------------------------------------------------
	// 🎯 4. Default UUIDs matching NEW_APIS_POSTMAN_COLLECTION.json & .md
	// In UUIDs se Postman ke collection variables bina edit kiye direct chalte hain!
	// -------------------------------------------------------------------------
	DefaultVenueID = "b24d1e6c-4258-4262-81c2-53dd34401517" // Matches {{venueId}}
	SecondVenueID  = "c35e2f7d-5369-5373-92d3-64ee45512628" // Matches Compare API 2nd ID
	DefaultFaqID   = "f585a0fa-75cb-4670-bc90-2fc0ab54c029" // Matches {{faqId}}

	// -------------------------------------------------------------------------
	// 📍 5. Geographic Anchor (Bengaluru Center matching Postman queries)
	// -------------------------------------------------------------------------
	DefaultCity = "Bengaluru"
	DefaultLat  = 12.9716
	DefaultLng  = 77.5946
)
