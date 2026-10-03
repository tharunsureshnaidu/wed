package repository

import (
	"context"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

// Dashboard is the customer home screen. The vendor and admin dashboards
// already existed; a customer had none, so the app had to assemble the screen
// from five calls.
type Dashboard struct {
	Stats     DashboardStats      `json:"stats"`
	Upcoming  []UpcomingBooking   `json:"upcomingBookings"`
	Actions   PendingActions      `json:"pendingActions"`
	Favourite []FavouriteFacility `json:"favourites"`
	Nearby    []NearbyVenue       `json:"recommended"`
	Coupons   []DashboardCoupon   `json:"coupons"`
}

type DashboardStats struct {
	TotalBookings     int64   `json:"totalBookings"`
	UpcomingBookings  int64   `json:"upcomingBookings"`
	CompletedBookings int64   `json:"completedBookings"`
	CancelledBookings int64   `json:"cancelledBookings"`
	TotalSpent        float64 `json:"totalSpent"`
	// SavedVenues and ReviewsWritten round out the profile strip.
	SavedVenues    int64 `json:"savedVenues"`
	ReviewsWritten int64 `json:"reviewsWritten"`
}

// PendingActions is what the customer still has to do. Each is a count, not a
// boolean: "2 unpaid" is actionable, "you have unpaid bookings" is not.
type PendingActions struct {
	UnpaidBookings      int64 `json:"unpaidBookings"`
	AwaitingReview      int64 `json:"awaitingReview"`
	OpenQuotes          int64 `json:"openQuotes"`
	UnreadNotifications int64 `json:"unreadNotifications"`
}

type UpcomingBooking struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	StartDate  *time.Time `json:"startDate"`
	EndDate    *time.Time `json:"endDate"`
	EventType  *string    `json:"eventType"`
	GuestCount *int       `json:"guestCount"`
	Total      float64    `json:"totalAmount"`
	Paid       float64    `json:"paidAmount"`
	FacilityID string     `json:"facilityId"`
	Facility   string     `json:"facilityName"`
	City       *string    `json:"city"`
	CoverImage *string    `json:"coverImage"`
}

type NearbyVenue struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	City          *string  `json:"city"`
	AvgRating     float64  `json:"avgRating"`
	StartingPrice *float64 `json:"startingPrice"`
	CoverImage    *string  `json:"coverImage"`
	// DistanceKm is nil when the venue or the caller has no coordinates -
	// never 0, which reads as "you are standing in it".
	DistanceKm *float64 `json:"distanceKm"`
}

type DashboardCoupon struct {
	Code          string     `json:"code"`
	Description   *string    `json:"description"`
	DiscountType  string     `json:"discountType"`
	DiscountValue float64    `json:"discountValue"`
	MaxDiscount   *float64   `json:"maxDiscount"`
	MinBooking    *float64   `json:"minBookingAmount"`
	ValidUntil    *time.Time `json:"validUntil"`
}

// Dashboard loads the customer home screen.
//
// Six queries, not one: the counts are scalar aggregates while the rest are
// lists, and folding lists into a single statement would need either repeated
// joins or array_agg of whole rows. Each is indexed and small - the lists are
// capped - so the round trips cost less than the SQL would.
//
// lat/lng is the caller's live location when the app sent one. Falling back to
// the saved profile location keeps the recommendations working for someone who
// denied the permission but set a city earlier.
func (r *Repo) Dashboard(ctx context.Context, userID int64, lat, lng *float64) (*Dashboard, error) {
	d := &Dashboard{
		Upcoming: []UpcomingBooking{}, Favourite: []FavouriteFacility{},
		Nearby: []NearbyVenue{}, Coupons: []DashboardCoupon{},
	}

	if err := r.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM bookings WHERE user_id = $1 AND is_deleted = FALSE),
		       (SELECT count(*) FROM bookings WHERE user_id = $1 AND is_deleted = FALSE
		          AND status IN ('PENDING','CONFIRMED') AND check_out >= CURRENT_DATE),
		       (SELECT count(*) FROM bookings WHERE user_id = $1 AND is_deleted = FALSE AND status = 'COMPLETED'),
		       (SELECT count(*) FROM bookings WHERE user_id = $1 AND is_deleted = FALSE AND status = 'CANCELLED'),
		       (SELECT COALESCE(sum(p.amount),0) FROM payments p JOIN bookings b ON b.id = p.booking_id
		          WHERE b.user_id = $1 AND p.status = 'SUCCESS'),
		       (SELECT count(*) FROM favourite_facilities WHERE user_profile_id = $1),
		       (SELECT count(*) FROM reviews WHERE user_id = $1 AND is_deleted = FALSE)`,
		userID).Scan(&d.Stats.TotalBookings, &d.Stats.UpcomingBookings,
		&d.Stats.CompletedBookings, &d.Stats.CancelledBookings, &d.Stats.TotalSpent,
		&d.Stats.SavedVenues, &d.Stats.ReviewsWritten); err != nil {
		return nil, err
	}

	// awaitingReview mirrors what POST /reviews enforces - a CONFIRMED or
	// COMPLETED stay, unique per (user, facility) - or the dashboard would
	// prompt for a review whose submission 403s.
	if err := r.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM bookings WHERE user_id = $1 AND is_deleted = FALSE
		          AND status IN ('PENDING','CONFIRMED') AND paid_amount < total_amount),
		       (SELECT count(DISTINCT b.target_id) FROM bookings b
		          WHERE b.user_id = $1 AND b.is_deleted = FALSE
		            AND b.status IN ('CONFIRMED','COMPLETED')
		            AND NOT EXISTS (SELECT 1 FROM reviews rv
		                             WHERE rv.user_id = $1 AND rv.facility_id = b.target_id
		                               AND rv.is_deleted = FALSE)),
		       (SELECT count(*) FROM quotes WHERE customer_id = $1
		          AND status IN ('REPLIED','COUNTERED')),
		       (SELECT count(*) FROM notifications WHERE recipient_user_id = $1
		          AND read_at IS NULL)`,
		userID).Scan(&d.Actions.UnpaidBookings, &d.Actions.AwaitingReview,
		&d.Actions.OpenQuotes, &d.Actions.UnreadNotifications); err != nil {
		return nil, err
	}

	rows, err := r.db.Query(ctx, `
		SELECT b.id::text, b.status, b.check_in, b.check_out, b.event_type,
		       b.guest_count, b.total_amount, b.paid_amount,
		       f.id::text, f.name, f.city,
		       (SELECT url FROM facility_images WHERE facility_id = f.id
		         ORDER BY sort_order LIMIT 1)
		  FROM bookings b JOIN facilities f ON f.id = b.target_id
		 WHERE b.user_id = $1 AND b.is_deleted = FALSE
		   AND b.status IN ('PENDING','CONFIRMED')
		   AND b.check_out >= CURRENT_DATE
		 ORDER BY b.check_in
		 LIMIT 5`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u UpcomingBooking
		if err := rows.Scan(&u.ID, &u.Status, &u.StartDate, &u.EndDate, &u.EventType,
			&u.GuestCount, &u.Total, &u.Paid, &u.FacilityID, &u.Facility,
			&u.City, &u.CoverImage); err != nil {
			rows.Close()
			return nil, err
		}
		d.Upcoming = append(d.Upcoming, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if d.Favourite, err = r.listFavouritesLimited(ctx, userID, 5); err != nil {
		return nil, err
	}
	if d.Nearby, err = r.recommended(ctx, userID, lat, lng, 10); err != nil {
		return nil, err
	}
	if d.Coupons, err = r.dashboardCoupons(ctx, 10); err != nil {
		return nil, err
	}
	return d, nil
}

func (r *Repo) listFavouritesLimited(ctx context.Context, userID int64, limit int) ([]FavouriteFacility, error) {
	rows, err := r.db.Query(ctx,
		`SELECT f.id, f.name, f.type, f.city, COALESCE(f.avg_rating, 0)
		   FROM favourite_facilities ff JOIN facilities f ON f.id = ff.facility_id
		  WHERE ff.user_profile_id = $1 AND f.is_deleted = FALSE
		  ORDER BY ff.created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FavouriteFacility{}
	for rows.Next() {
		var f FavouriteFacility
		if err := rows.Scan(&f.ID, &f.Name, &f.Type, &f.City, &f.AvgRating); err != nil {
			return nil, err
		}
		f.Type = venuetype.API(f.Type)
		out = append(out, f)
	}
	return out, rows.Err()
}

// recommended is marriage halls near the caller, best rated first.
//
// Only 36 of 506 profiles have coordinates, so "near" has to degrade: with no
// usable location this returns the top-rated approved halls instead of an
// empty list. An empty recommendations strip looks like a broken screen.
func (r *Repo) recommended(ctx context.Context, userID int64, lat, lng *float64, limit int) ([]NearbyVenue, error) {
	rows, err := r.db.Query(ctx, `
		WITH me AS (
		    SELECT COALESCE($2::double precision, p.lat) AS lat,
		           COALESCE($3::double precision, p.lng) AS lng
		      FROM user_profiles p WHERE p.id = $1
		    UNION ALL
		    SELECT $2::double precision, $3::double precision
		     WHERE NOT EXISTS (SELECT 1 FROM user_profiles WHERE id = $1)
		    LIMIT 1
		)
		SELECT f.id::text, f.name, f.type, f.city, COALESCE(f.avg_rating,0),
		       f.base_price_per_day,
		       (SELECT url FROM facility_images WHERE facility_id = f.id
		         ORDER BY sort_order LIMIT 1),
		       CASE WHEN me.lat IS NOT NULL AND me.lng IS NOT NULL
		                 AND f.lat IS NOT NULL AND f.lng IS NOT NULL
		            THEN round((earth_distance(ll_to_earth(f.lat, f.lng),
		                                       ll_to_earth(me.lat, me.lng)) / 1000.0)::numeric, 2)
		       END
		  FROM facilities f CROSS JOIN me
		 WHERE f.is_deleted = FALSE AND f.status = 'APPROVED'
		   AND f.type = 'MARRIAGE_HALL'
		 ORDER BY
		   CASE WHEN me.lat IS NOT NULL AND f.lat IS NOT NULL
		        THEN earth_distance(ll_to_earth(f.lat, f.lng), ll_to_earth(me.lat, me.lng))
		   END NULLS LAST,
		   f.avg_rating DESC NULLS LAST
		 LIMIT $4`, userID, lat, lng, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NearbyVenue{}
	for rows.Next() {
		var v NearbyVenue
		if err := rows.Scan(&v.ID, &v.Name, &v.Type, &v.City, &v.AvgRating,
			&v.StartingPrice, &v.CoverImage, &v.DistanceKm); err != nil {
			return nil, err
		}
		v.Type = venuetype.API(v.Type)
		out = append(out, v)
	}
	return out, rows.Err()
}

// dashboardCoupons is the platform-wide offers strip: admin coupons usable
// today. Venue- and vendor-scoped codes are left out - they need a venue to
// make sense of, and the venue page already lists them.
func (r *Repo) dashboardCoupons(ctx context.Context, limit int) ([]DashboardCoupon, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.code, c.description, c.discount_type, c.discount_value,
		       c.max_discount, c.min_booking_amount, c.valid_until
		  FROM coupons c
		 WHERE c.is_deleted = FALSE AND c.is_active
		   AND c.facility_type IS NOT NULL
		   AND (c.valid_from IS NULL OR c.valid_from <= CURRENT_TIMESTAMP)
		   AND (c.valid_until IS NULL OR c.valid_until >= CURRENT_TIMESTAMP)
		   AND (c.usage_limit IS NULL OR c.used_count < c.usage_limit)
		 ORDER BY c.valid_until NULLS LAST, c.created_at DESC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DashboardCoupon{}
	for rows.Next() {
		var c DashboardCoupon
		if err := rows.Scan(&c.Code, &c.Description, &c.DiscountType, &c.DiscountValue,
			&c.MaxDiscount, &c.MinBooking, &c.ValidUntil); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
