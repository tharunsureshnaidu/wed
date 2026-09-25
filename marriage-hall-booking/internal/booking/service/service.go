package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
)

// holdWindow is how long an unpaid booking keeps its slot before the sweeper
// releases it. Long enough to finish a checkout, short enough that a dropped
// session does not block a date all day.
const holdWindow = 15 * time.Minute

// Pool exposes the connection the service already holds, for read-only
// queries next to the booking flow (the price preview) that would otherwise
// need a second pool wired through main for no reason.
func (s *Service) Pool() *pgxpool.Pool { return s.db }

type Service struct {
	repo *repository.Repo
	db   *pgxpool.Pool
	// OnBookingCreated / OnBookingCancelled publish domain events. Funcs rather
	// than a direct dependency so booking does not import the events package.
	OnBookingCreated   func(ctx context.Context, b *repository.Booking)
	OnBookingCancelled func(ctx context.Context, b *repository.Booking)
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
	GuestName     *string
	GuestEmail    *string
	GuestPhone    *string
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

	if req.EventDate.Before(time.Now().Truncate(24 * time.Hour)) {
		return nil, apperr.BadRequest("INVALID_DATE", "Event date cannot be in the past")
	}

	var basePrice *float64
	var facilityType string
	err := s.db.QueryRow(ctx,
		`SELECT base_price_per_day, type FROM facilities
		 WHERE id = $1 AND is_deleted = FALSE AND status <> 'BLOCKED'`,
		req.FacilityID).Scan(&basePrice, &facilityType)
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
	total := 0.0
	if basePrice != nil {
		total = *basePrice * float64(days)
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
		total = *req.OverrideAmount
	}

	b, err := s.repo.CreateHallBooking(ctx, repository.HallBookingInput{
		UserID: userID, FacilityID: req.FacilityID,
		EventDate: req.EventDate, EndDate: req.EndDate,
		StartTime: req.StartTime, EndTime: req.EndTime,
		GuestCount: req.GuestCount, RoomCount: req.RoomCount, EventType: req.EventType,
		SlotType: req.SlotType, TotalAmount: total, IdempotentKey: req.IdempotentKey,
		GuestName: req.GuestName, GuestEmail: req.GuestEmail, GuestPhone: req.GuestPhone,
		HoldFor: holdWindow, PackageIDs: req.PackageIDs, Addons: req.Addons,
	})
	switch {
	case errors.Is(err, repository.ErrSlotTaken):
		return nil, apperr.Conflict("SLOT_UNAVAILABLE", "Hall slot is no longer available")
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

type HotelBookingRequest struct {
	FacilityID    string
	CheckIn       time.Time
	CheckOut      time.Time
	Rooms         []repository.RoomLine
	IdempotentKey string
	GuestName     *string
	GuestEmail    *string
	GuestPhone    *string
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
	if req.CheckIn.Before(time.Now().Truncate(24 * time.Hour)) {
		return nil, apperr.BadRequest("INVALID_DATES", "Check-in cannot be in the past")
	}
	if len(req.Rooms) == 0 {
		return nil, apperr.BadRequest("NO_ROOMS", "At least one room is required")
	}

	nights := int(req.CheckOut.Sub(req.CheckIn).Hours() / 24)
	total := 0.0
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
		total += price * float64(room.Quantity) * float64(nights)
	}

	b, err := s.repo.CreateHotelBooking(ctx, repository.HotelBookingInput{
		UserID: userID, FacilityID: req.FacilityID, CheckIn: req.CheckIn,
		CheckOut: req.CheckOut, TotalAmount: total, IdempotentKey: req.IdempotentKey,
		GuestName: req.GuestName, GuestEmail: req.GuestEmail, GuestPhone: req.GuestPhone,
		HoldFor: holdWindow, Rooms: req.Rooms,
	})
	switch {
	case errors.Is(err, repository.ErrNoInventory):
		return nil, apperr.Conflict("INVENTORY_UNAVAILABLE",
			"Rooms are no longer available for the selected dates")
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
	if b.UserID != userID && !isAdmin {
		var owner int64
		if err := s.db.QueryRow(ctx,
			`SELECT owner_id FROM facilities WHERE id = $1`, b.TargetID).Scan(&owner); err != nil {
			return nil, err
		}
		if owner != userID {
			return nil, apperr.Forbidden("NOT_BOOKING_OWNER", "You cannot view this booking")
		}
	}
	return b, nil
}

func (s *Service) Cancel(ctx context.Context, id string, userID int64, isAdmin bool) error {
	b, err := s.Get(ctx, id, userID, isAdmin)
	if err != nil {
		return err
	}
	// Release first: if SetStatus then fails, the sweeper still reconciles, whereas
	// the reverse order could leave a cancelled booking holding its slot forever.
	if err := s.repo.ReleaseInventory(ctx, b.ID); err != nil {
		return err
	}
	err = s.repo.SetStatus(ctx, b.ID, "CANCELLED", "Cancelled by user", "PENDING", "CONFIRMED")
	if errors.Is(err, repository.ErrNotFound) {
		return apperr.Conflict("INVALID_STATE", "Booking cannot be cancelled in its current state")
	}
	if err == nil && s.OnBookingCancelled != nil {
		s.OnBookingCancelled(ctx, b)
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

func (s *Service) ExpireHolds(ctx context.Context) (int, error) {
	return s.repo.ExpireHolds(ctx)
}
