package service

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/coupon"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

// holdWindow is how long an unpaid hotel booking keeps its rooms before the
// sweeper releases it. Long enough to finish a checkout, short enough that a
// dropped session does not block a night all day.
//
// A hall request has no expiry: it waits for the owner to confirm or reject.
// With 15 minutes, 241 hall requests expired and none was ever confirmed -
// owners could not answer in time.
const holdWindow = 15 * time.Minute

// Request size caps. Each day, night or line is one or two statements inside
// the transaction that holds the slot and coupon locks, so an unbounded
// request was a way to hold a hall for a year or stall the database.
const (
	maxHallDays   = 14
	maxHotelNight = 30
	maxLines      = 20
)

// ist is India Standard Time. Fixed, not LoadLocation: India has no DST, and a
// fixed zone needs no tzdata in the container.
var ist = time.FixedZone("IST", 5*3600+1800)

// Today is the current date in India, as a UTC-midnight date comparable with
// the YYYY-MM-DD dates requests parse to. time.Now().Truncate(24h) truncates
// to UTC midnight, so between 00:00 and 05:30 IST yesterday was still bookable.
func Today() time.Time {
	y, m, d := time.Now().In(ist).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Pool exposes the connection the service already holds, for read-only
// queries next to the booking flow (the price preview) that would otherwise
// need a second pool wired through main for no reason.
func (s *Service) Pool() *pgxpool.Pool { return s.db }

type Service struct {
	repo *repository.Repo
	db   *pgxpool.Pool
	// OnBookingCreated / OnBookingCancelled publish domain events. Funcs rather
	// than a direct dependency so booking does not import the events package.
	// actorID is who cancelled - the customer, the venue owner or an admin -
	// so the audit row answers "who" rather than always naming the customer.
	OnBookingCreated   func(ctx context.Context, b *repository.Booking)
	OnBookingCancelled func(ctx context.Context, b *repository.Booking, actorID int64)
	// OnBookingDecided fires when a venue owner accepts or rejects a request.
	// The customer is waiting on that answer, so it must reach them.
	OnBookingDecided func(ctx context.Context, b *repository.Booking, confirmed bool, reason string, actorID int64)
}

func New(repo *repository.Repo, db *pgxpool.Pool) *Service {
	return &Service{repo: repo, db: db}
}

type HallBookingRequest struct {
	FacilityID string
	// EventDate is the first day; EndDate the last. A single-day booking has
	// both equal, which is what the old single-date form produced.
	EventDate time.Time
	EndDate   time.Time
	// StartTime/EndTime are "HH:MM", empty when the caller booked by slot.
	StartTime  string
	EndTime    string
	GuestCount *int
	// RoomCount is how many guest rooms the customer needs alongside the hall.
	// Optional, and deliberately not priced - see migration 041.
	RoomCount     *int
	EventType     *string
	SlotType      string
	PackageIDs    []string
	Addons        []repository.AddonLine
	IdempotentKey string
	// CouponCode is optional. It comes off the price after the venue's own
	// advertised discount, and is redeemed in the booking's transaction.
	CouponCode string
	GuestName  *string
	GuestEmail *string
	GuestPhone *string
	// OverrideAmount is the price agreed through the quote flow. It is set only
	// by the quote conversion path, never from an HTTP request body - a client
	// that could set its own total would book a hall for whatever it liked.
	OverrideAmount *float64
}

// CreateHallBooking prices the booking server-side and claims the slot.
//
// Prices are always read from the database, never taken from the request body -
// a client that could send its own total would simply book a hall for ₹1.
func (s *Service) CreateHallBooking(ctx context.Context, userID int64, req HallBookingRequest) (*repository.Booking, error) {
	// An idempotent retry must return the original booking, not a second one.
	if req.IdempotentKey != "" {
		existing, err := s.repo.FindByIdempotencyKey(ctx, req.IdempotentKey)
		if err == nil {
			if existing.UserID != userID {
				return nil, apperr.Conflict("IDEMPOTENCY_KEY_CONFLICT",
					"Idempotency key already used by a different request")
			}
			return existing, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}

	if req.EventDate.Before(Today()) {
		return nil, apperr.BadRequest("INVALID_DATE", "Event date cannot be in the past")
	}
	if !req.EndDate.IsZero() && req.EndDate.Sub(req.EventDate) >= maxHallDays*24*time.Hour {
		return nil, apperr.BadRequest("VALIDATION_ERROR", "A hall booking can span at most 14 days")
	}
	if len(req.PackageIDs) > maxLines || len(req.Addons) > maxLines {
		return nil, apperr.BadRequest("VALIDATION_ERROR", "At most 20 packages and 20 add-ons per booking")
	}

	var basePrice, discountPct *float64
	var facilityType string
	err := s.db.QueryRow(ctx,
		`SELECT base_price_per_day, type, `+ActiveDiscountSQL+` FROM facilities f
		 WHERE id = $1 AND `+venuetype.LiveSQL("f"),
		req.FacilityID).Scan(&basePrice, &facilityType, &discountPct)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.BadRequest("INVALID_HALL", "Invalid hall")
	}
	if err != nil {
		return nil, err
	}
	if facilityType != "MARRIAGE_HALL" {
		return nil, apperr.BadRequest("INVALID_HALL", "Facility is not a marriage hall")
	}

	// A caller that gives no end date means a single-day event. Defaulting here
	// rather than in the HTTP handler covers every path into this service - the
	// quote-to-booking conversion and the tests reach it directly, and a zero
	// EndDate would otherwise be written as check_out and fail chk_dates.
	if req.EndDate.IsZero() || req.EndDate.Before(req.EventDate) {
		req.EndDate = req.EventDate
	}

	// The base price is per day, so a multi-day event pays for each day. Both
	// ends are inclusive: a Friday-to-Sunday booking occupies three days.
	days := 1
	if !req.EndDate.IsZero() && req.EndDate.After(req.EventDate) {
		days = int(req.EndDate.Sub(req.EventDate).Hours()/24) + 1
	}
	// The day rate is the discounted one the listing card shows, so checkout
	// charges what the customer was told. It used to charge the full base
	// price while the card advertised "15% off".
	total, venueDiscount := 0.0, 0.0
	if basePrice != nil {
		rate := DayRate(*basePrice, discountPct)
		total = rate * float64(days)
		venueDiscount = (*basePrice - rate) * float64(days)
	}

	// Packages and add-ons are priced from their own rows, and must belong to
	// this facility - otherwise a caller could attach a cheap package from
	// somewhere else.
	for _, id := range req.PackageIDs {
		var price float64
		err := s.db.QueryRow(ctx,
			`SELECT price FROM hall_packages
			 WHERE id = $1 AND facility_id = $2 AND is_deleted = FALSE`,
			id, req.FacilityID).Scan(&price)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.BadRequest("INVALID_PACKAGE", "Invalid package for this hall")
		}
		if err != nil {
			return nil, err
		}
		total += price
	}
	for i, a := range req.Addons {
		if a.Quantity <= 0 {
			return nil, apperr.BadRequest("INVALID_ADDON", "Add-on quantity must be positive")
		}
		var price float64
		err := s.db.QueryRow(ctx,
			`SELECT price FROM add_on_services
			 WHERE id = $1 AND facility_id = $2 AND is_deleted = FALSE`,
			a.AddonID, req.FacilityID).Scan(&price)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.BadRequest("INVALID_ADDON", "Invalid add-on for this hall")
		}
		if err != nil {
			return nil, err
		}
		req.Addons[i].Price = price
		total += price * float64(a.Quantity)
	}

	if req.OverrideAmount != nil {
		if *req.OverrideAmount < 0 {
			return nil, apperr.BadRequest("INVALID_AMOUNT", "Agreed amount cannot be negative")
		}
		// A price agreed through a quote is final: no advertised discount on
		// top of a negotiated one.
		total, venueDiscount = *req.OverrideAmount, 0
	}

	var couponID, couponCode string
	couponDiscount := 0.0
	if req.CouponCode != "" && req.OverrideAmount == nil {
		c, d, err := s.PriceCoupon(ctx, req.CouponCode, req.FacilityID, total)
		if err != nil {
			return nil, err
		}
		couponID, couponCode, couponDiscount = c.ID, c.Code, d
		total -= d
	}

	b, err := s.repo.CreateHallBooking(ctx, repository.HallBookingInput{
		UserID: userID, FacilityID: req.FacilityID,
		EventDate: req.EventDate, EndDate: req.EndDate,
		StartTime: req.StartTime, EndTime: req.EndTime,
		GuestCount: req.GuestCount, RoomCount: req.RoomCount, EventType: req.EventType,
		SlotType: req.SlotType, TotalAmount: total, IdempotentKey: req.IdempotentKey,
		GuestName: req.GuestName, GuestEmail: req.GuestEmail, GuestPhone: req.GuestPhone,
		HoldFor: 0, PackageIDs: req.PackageIDs, Addons: req.Addons, // no expiry: waits for the owner
		DiscountAmount: venueDiscount + couponDiscount,
		CouponID:       couponID, CouponCode: couponCode,
	})
	switch {
	case errors.Is(err, repository.ErrSlotTaken):
		return nil, apperr.Conflict("SLOT_UNAVAILABLE", "Hall slot is no longer available")
	case errors.Is(err, repository.ErrCouponUnavailable):
		// Ran out (or was switched off) between pricing and the insert.
		return nil, apperr.New(coupon.ErrExhausted.Status, coupon.ErrExhausted.Code,
			"This coupon is no longer available")
	case errors.Is(err, repository.ErrDuplicateKey):
		// Lost the race on the idempotency key: the winner's booking is the answer.
		if existing, e := s.repo.FindByIdempotencyKey(ctx, req.IdempotentKey); e == nil {
			if existing.UserID != userID {
				return nil, apperr.Conflict("IDEMPOTENCY_KEY_CONFLICT",
					"Idempotency key already used by a different request")
			}
			return existing, nil
		}
		return nil, apperr.Conflict("DUPLICATE_REQUEST", "Duplicate booking request")
	case err != nil:
		return nil, err
	}
	if s.OnBookingCreated != nil {
		s.OnBookingCreated(ctx, b)
	}
	return b, nil
}

// ActiveDiscountSQL is a facility's advertised discount percent, or NULL once
// it has expired - the same rule the listing applies, so an expired offer is
// neither shown nor charged.
const ActiveDiscountSQL = `CASE WHEN discount_valid_until IS NULL OR discount_valid_until > CURRENT_TIMESTAMP
	THEN discount_percent END`

// DayRate is a hall's day rate after its advertised discount, rounded to whole
// units exactly as the listing's discountedPrice is. Charging the unrounded
// figure would put a different number at checkout than on the card.
func DayRate(base float64, pct *float64) float64 {
	if pct == nil || *pct <= 0 {
		return base
	}
	return math.Round(base * (100 - *pct) / 100)
}

// PriceCoupon checks a code against a venue and an amount and returns what it
// takes off. Shared by the booking and the price preview, so the preview can
// never promise a discount the booking then refuses.
func (s *Service) PriceCoupon(ctx context.Context, code, facilityID string, amount float64) (*coupon.Coupon, float64, error) {
	c, err := coupon.Load(ctx, s.db, code, facilityID)
	if err == nil {
		err = c.Check(amount)
	}
	var ce *coupon.Error
	if errors.As(err, &ce) {
		return nil, 0, apperr.New(ce.Status, ce.Code, ce.Message)
	}
	if err != nil {
		return nil, 0, err
	}
	return c, c.Discount(amount), nil
}

type HotelBookingRequest struct {
	FacilityID    string
	CheckIn       time.Time
	CheckOut      time.Time
	Rooms         []repository.RoomLine
	IdempotentKey string
	GuestName     *string
	GuestEmail    *string
	GuestPhone    *string
	// CouponCode is optional, applied as for a hall: after the venue discount.
	CouponCode string
}

func (s *Service) CreateHotelBooking(ctx context.Context, userID int64, req HotelBookingRequest) (*repository.Booking, error) {
	if req.IdempotentKey != "" {
		existing, err := s.repo.FindByIdempotencyKey(ctx, req.IdempotentKey)
		if err == nil {
			if existing.UserID != userID {
				return nil, apperr.Conflict("IDEMPOTENCY_KEY_CONFLICT",
					"Idempotency key already used by a different request")
			}
			return existing, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}

	if !req.CheckOut.After(req.CheckIn) {
		return nil, apperr.BadRequest("INVALID_DATES", "Check-out must be after check-in")
	}
	if req.CheckIn.Before(Today()) {
		return nil, apperr.BadRequest("INVALID_DATES", "Check-in cannot be in the past")
	}
	if len(req.Rooms) == 0 {
		return nil, apperr.BadRequest("NO_ROOMS", "At least one room is required")
	}
	nights := int(req.CheckOut.Sub(req.CheckIn).Hours() / 24)
	if nights > maxHotelNight {
		return nil, apperr.BadRequest("VALIDATION_ERROR", "A hotel stay can be at most 30 nights")
	}
	if len(req.Rooms) > maxLines {
		return nil, apperr.BadRequest("VALIDATION_ERROR", "At most 20 room lines per booking")
	}

	// The same live-venue rule as a hall. This path used to check nothing, so
	// a deleted or rejected hotel was bookable by id.
	var facilityType string
	var discountPct *float64
	err := s.db.QueryRow(ctx,
		`SELECT type, `+ActiveDiscountSQL+` FROM facilities f WHERE id = $1 AND `+venuetype.LiveSQL("f"),
		req.FacilityID).Scan(&facilityType, &discountPct)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && facilityType != venuetype.Hotel) {
		return nil, apperr.BadRequest("INVALID_HOTEL", "Invalid hotel")
	}
	if err != nil {
		return nil, err
	}

	// Each night is charged at the discounted rate the card advertises, as for
	// a hall's day rate; checkout used to charge the full room rate.
	total, venueDiscount := 0.0, 0.0
	for i, room := range req.Rooms {
		if room.Quantity <= 0 {
			return nil, apperr.BadRequest("INVALID_ROOM", "Room quantity must be positive")
		}
		var price float64
		err := s.db.QueryRow(ctx,
			`SELECT base_price_per_night FROM room_types
			 WHERE id = $1 AND facility_id = $2 AND is_deleted = FALSE`,
			room.RoomTypeID, req.FacilityID).Scan(&price)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.BadRequest("INVALID_ROOM", "Invalid room type")
		}
		if err != nil {
			return nil, err
		}
		req.Rooms[i].Price = price
		units := float64(room.Quantity) * float64(nights)
		rate := DayRate(price, discountPct)
		total += rate * units
		venueDiscount += (price - rate) * units
	}

	var couponID, couponCode string
	couponDiscount := 0.0
	if req.CouponCode != "" {
		c, d, err := s.PriceCoupon(ctx, req.CouponCode, req.FacilityID, total)
		if err != nil {
			return nil, err
		}
		couponID, couponCode, couponDiscount = c.ID, c.Code, d
		total -= d
	}

	b, err := s.repo.CreateHotelBooking(ctx, repository.HotelBookingInput{
		UserID: userID, FacilityID: req.FacilityID, CheckIn: req.CheckIn,
		CheckOut: req.CheckOut, TotalAmount: total, IdempotentKey: req.IdempotentKey,
		GuestName: req.GuestName, GuestEmail: req.GuestEmail, GuestPhone: req.GuestPhone,
		HoldFor: holdWindow, Rooms: req.Rooms,
		DiscountAmount: venueDiscount + couponDiscount,
		CouponID:       couponID, CouponCode: couponCode,
	})
	switch {
	case errors.Is(err, repository.ErrNoInventory):
		return nil, apperr.Conflict("INVENTORY_UNAVAILABLE",
			"Rooms are no longer available for the selected dates")
	case errors.Is(err, repository.ErrCouponUnavailable):
		return nil, apperr.New(coupon.ErrExhausted.Status, coupon.ErrExhausted.Code,
			"This coupon is no longer available")
	case errors.Is(err, repository.ErrDuplicateKey):
		if existing, e := s.repo.FindByIdempotencyKey(ctx, req.IdempotentKey); e == nil {
			if existing.UserID != userID {
				return nil, apperr.Conflict("IDEMPOTENCY_KEY_CONFLICT",
					"Idempotency key already used by a different request")
			}
			return existing, nil
		}
		return nil, apperr.Conflict("DUPLICATE_REQUEST", "Duplicate booking request")
	case err != nil:
		return nil, err
	}
	if s.OnBookingCreated != nil {
		s.OnBookingCreated(ctx, b)
	}
	return b, nil
}

func (s *Service) Get(ctx context.Context, id string, userID int64, isAdmin bool) (*repository.Booking, error) {
	b, err := s.repo.Get(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, "BOOKING_NOT_FOUND", "Booking not found")
	}
	if err != nil {
		return nil, err
	}
	// Enriched for the same reason as List: the detail screen shows the venue,
	// and a Rate button that depends on review state.
	if err := s.repo.Enrich(ctx, userID, []*repository.Booking{b}); err != nil {
		logger.Error("bookings: enrich detail", "bookingId", id, logger.Err(err))
	}
	// A booking is readable by the customer who made it, the facility owner, or an admin.
	isOwnerOrAdmin := isAdmin
	if b.UserID != userID && !isAdmin {
		var owner int64
		if err := s.db.QueryRow(ctx,
			`SELECT owner_id FROM facilities WHERE id = $1`, b.TargetID).Scan(&owner); err != nil {
			return nil, err
		}
		if owner != userID {
			return nil, apperr.Forbidden("NOT_BOOKING_OWNER", "You cannot view this booking")
		}
		isOwnerOrAdmin = true
	}
	if isOwnerOrAdmin {
		_ = s.repo.EnrichUsers(ctx, []*repository.Booking{b})
	}
	return b, nil
}

func (s *Service) Cancel(ctx context.Context, id string, userID int64, isAdmin bool) error {
	b, err := s.Get(ctx, id, userID, isAdmin)
	if err != nil {
		return err
	}
	// SetStatus frees the dates or rooms in the same transaction, and only on a
	// real PENDING/CONFIRMED -> CANCELLED move, so a retried cancel frees nothing.
	err = s.repo.SetStatus(ctx, b.ID, "CANCELLED", "Cancelled by user", "PENDING", "CONFIRMED")
	if errors.Is(err, repository.ErrNotFound) {
		return apperr.Conflict("INVALID_STATE", "Booking cannot be cancelled in its current state")
	}
	if err == nil && s.OnBookingCancelled != nil {
		s.OnBookingCancelled(ctx, b, userID)
	}
	return err
}

// List is the "my bookings" screen. Each row carries its venue summary and
// review state so the client renders the whole list from one response.
func (s *Service) List(ctx context.Context, userID int64, page, size int) ([]repository.Booking, int64, error) {
	items, total, err := s.repo.ListForUser(ctx, userID, page, size)
	if err != nil {
		return nil, 0, err
	}
	ptrs := make([]*repository.Booking, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}
	if err := s.repo.Enrich(ctx, userID, ptrs); err != nil {
		// The bookings themselves are correct; only the venue summary is
		// missing. Returning an error here would blank the whole screen.
		logger.Error("bookings: enrich list", "userId", userID, logger.Err(err))
	}
	return items, total, nil
}

func (s *Service) ExpireHolds(ctx context.Context) ([]string, error) {
	return s.repo.ExpireHolds(ctx)
}
