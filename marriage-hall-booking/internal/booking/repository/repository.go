package repository

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/coupon"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrSlotTaken    = errors.New("slot already booked")
	ErrNoInventory  = errors.New("insufficient inventory")
	ErrDuplicateKey = errors.New("idempotency key already used")
	// ErrCouponUnavailable: the coupon was valid when priced but could not be
	// redeemed - its last use went to a concurrent booking, or it was switched off.
	ErrCouponUnavailable = errors.New("coupon no longer available")
)

type Repo struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Repo { return &Repo{db: db} }

type BookingUser struct {
	UserID      int64   `json:"userId"`
	Name        string  `json:"name"`
	Email       *string `json:"email"`
	PhoneNumber *string `json:"phoneNumber"`
	Address     *string `json:"address"`
}

type Booking struct {
	ID         string       `json:"id"`
	UserID     int64        `json:"userId"`
	User       *BookingUser `json:"user,omitempty"`
	TargetType string       `json:"targetType"`
	TargetID   string       `json:"targetId"`
	CheckIn    time.Time    `json:"checkIn"`
	// StartDate/EndDate are checkIn/checkOut under the names the booking screen
	// uses; both spellings are returned so neither client has to translate.
	StartDate  time.Time `json:"startDate"`
	CheckOut   time.Time `json:"checkOut"`
	EndDate    time.Time `json:"endDate"`
	StartTime  *string   `json:"startTime"`
	EndTime    *string   `json:"endTime"`
	GuestCount *int      `json:"guestCount"`
	RoomCount  *int      `json:"roomCount"`

	// Facility and review state are joined in for the "my bookings" screen:
	// without them a client holds a bare targetId and has to fetch each venue
	// separately to render a list.
	Facility *BookingFacility `json:"facility,omitempty"`
	// CanReview is true when the stay is reviewable and the user has not
	// already reviewed this venue. Reviews are unique per (user, facility),
	// not per booking, so a second booking at the same hall is not a second
	// chance to review it.
	CanReview       bool       `json:"canReview"`
	HasReviewed     bool       `json:"hasReviewed"`
	MyRating        *int       `json:"myRating"`
	MyReviewID      *string    `json:"myReviewId"`
	EventType       *string    `json:"eventType"`
	SlotType        *string    `json:"slotType,omitempty"`
	TotalAmount     float64    `json:"totalAmount"`
	DiscountAmount  float64    `json:"discountAmount"`
	CouponCode      *string    `json:"couponCode"`
	PaidAmount      float64    `json:"paidAmount"`
	Status          string     `json:"status"`
	RejectionReason *string    `json:"rejectionReason"`
	IdempotentKey   *string    `json:"idempotentKey,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	GuestName       *string    `json:"guestName,omitempty"`
	GuestEmail      *string    `json:"guestEmail,omitempty"`
	GuestPhone      *string    `json:"guestPhone,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

const cols = `id, user_id, target_type, target_id, check_in, check_out, slot_type,
	total_amount, COALESCE(discount_amount,0), COALESCE(paid_amount,0), status,
	idempotent_key, expires_at, guest_name, guest_email, guest_phone, created_at,
	to_char(start_time,'HH24:MI'), to_char(end_time,'HH24:MI'), guest_count, event_type,
	room_count, coupon_code,
	CASE WHEN status = 'REJECTED' THEN COALESCE(rejection_reason, (
		SELECT reason FROM booking_status_history WHERE booking_id = bookings.id AND to_status = 'REJECTED' ORDER BY id DESC LIMIT 1
	)) ELSE NULL END`

// ownerCols is cols with the bookings alias, for the owner listing's join
// against facilities. Written out rather than derived: a column added to cols
// and not here is a compile-time scan mismatch, which is a louder failure than
// a string rewriter quietly mangling a function name.
const ownerCols = `b.id, b.user_id, b.target_type, b.target_id, b.check_in, b.check_out, b.slot_type,
	b.total_amount, COALESCE(b.discount_amount,0), COALESCE(b.paid_amount,0), b.status,
	b.idempotent_key, b.expires_at, b.guest_name, b.guest_email, b.guest_phone, b.created_at,
	to_char(b.start_time,'HH24:MI'), to_char(b.end_time,'HH24:MI'), b.guest_count, b.event_type,
	b.room_count, b.coupon_code,
	CASE WHEN b.status = 'REJECTED' THEN COALESCE(b.rejection_reason, (
		SELECT reason FROM booking_status_history WHERE booking_id = b.id AND to_status = 'REJECTED' ORDER BY id DESC LIMIT 1
	)) ELSE NULL END`

func scan(row pgx.Row) (*Booking, error) {
	var b Booking
	err := row.Scan(&b.ID, &b.UserID, &b.TargetType, &b.TargetID, &b.CheckIn, &b.CheckOut,
		&b.SlotType, &b.TotalAmount, &b.DiscountAmount, &b.PaidAmount, &b.Status,
		&b.IdempotentKey, &b.ExpiresAt, &b.GuestName, &b.GuestEmail, &b.GuestPhone, &b.CreatedAt,
		&b.StartTime, &b.EndTime, &b.GuestCount, &b.EventType, &b.RoomCount, &b.CouponCode,
		&b.RejectionReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	b.StartDate, b.EndDate = b.CheckIn, b.CheckOut
	return &b, err
}

func (r *Repo) Get(ctx context.Context, id string) (*Booking, error) {
	return scan(r.db.QueryRow(ctx, `SELECT `+cols+` FROM bookings WHERE id = $1 AND is_deleted = FALSE`, id))
}

func (r *Repo) FindByIdempotencyKey(ctx context.Context, key string) (*Booking, error) {
	// $1 <> '' guards the empty key: rows written before NULLIF (below) stored
	// '', and matching one would hand a stranger's booking to the next caller
	// who omits the key entirely.
	return scan(r.db.QueryRow(ctx,
		`SELECT `+cols+` FROM bookings WHERE idempotent_key = $1 AND $1 <> ''`, key))
}

// BookingFacility is the venue summary a bookings list needs to render a row.
type BookingFacility struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	City       *string  `json:"city"`
	Address    *string  `json:"fullAddress"`
	CoverImage *string  `json:"coverImage"`
	Phone      *string  `json:"contactPhone"`
	AvgRating  float64  `json:"avgRating"`
	Lat        *float64 `json:"lat"`
	Lng        *float64 `json:"lng"`
}

type HallBookingInput struct {
	UserID     int64
	FacilityID string
	EventDate  time.Time
	// EndDate is the last day of the event; equal to EventDate for a
	// single-day booking.
	EndDate    time.Time
	StartTime  string
	EndTime    string
	GuestCount *int
	// RoomCount is how many guest rooms the customer needs alongside the hall.
	// Optional and unpriced - see migration 041.
	RoomCount     *int
	EventType     *string
	SlotType      string
	TotalAmount   float64
	IdempotentKey string
	GuestName     *string
	GuestEmail    *string
	GuestPhone    *string
	HoldFor       time.Duration
	PackageIDs    []string
	Addons        []AddonLine
	// DiscountAmount is everything taken off: the venue's advertised discount
	// plus the coupon. TotalAmount is already net of it.
	DiscountAmount float64
	CouponID       string
	CouponCode     string
}

type AddonLine struct {
	AddonID  string
	Quantity int
	Price    float64
}

// CreateHallBooking claims the slot and creates the booking in ONE transaction.
//
// The whole concurrency guarantee is the conditional INSERT below: hall_availability
// has a UNIQUE index on (facility_id, date, slot_type), so two racing requests for
// the same slot both attempt the same insert and exactly one wins. The loser's
// ON CONFLICT ... WHERE clause matches no row, RowsAffected is 0, and it gets a
// clean ErrSlotTaken instead of a constraint-violation 500.
//
// The Java version read the row, checked status in application code, then wrote -
// a read-then-write that only held because a unique index happened to backstop it,
// and which surfaced lost races as 500s.
func (r *Repo) CreateHallBooking(ctx context.Context, in HallBookingInput) (*Booking, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var bookingID string
	var expiresAt *time.Time
	if in.HoldFor > 0 {
		t := time.Now().Add(in.HoldFor)
		expiresAt = &t
	}

	err = tx.QueryRow(ctx,
		`INSERT INTO bookings (user_id, target_type, target_id, check_in, check_out,
		    slot_type, total_amount, idempotent_key, expires_at, guest_name, guest_email,
		    guest_phone, start_time, end_time, guest_count, event_type, room_count,
		    discount_amount, coupon_id, coupon_code)
		 VALUES ($1,'HALL',$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,
		         NULLIF($12,'')::time, NULLIF($13,'')::time, $14, $15, $16,
		         $17, NULLIF($18,'')::uuid, NULLIF($19,'')) RETURNING id`,
		in.UserID, in.FacilityID, in.EventDate, in.EndDate, in.SlotType, in.TotalAmount,
		in.IdempotentKey, expiresAt, in.GuestName, in.GuestEmail, in.GuestPhone,
		in.StartTime, in.EndTime, in.GuestCount, in.EventType, in.RoomCount,
		in.DiscountAmount, in.CouponID, in.CouponCode).Scan(&bookingID)
	if err != nil {
		if isUnique(err, "idx_bookings_idempotent") {
			return nil, ErrDuplicateKey
		}
		return nil, err
	}

	// Claim EVERY date in the range, not just the first. Availability is one
	// row per (facility, date, slot), so a three-day event that claimed only
	// day one would leave days two and three bookable by someone else.
	//
	// Each claim succeeds only if no row exists or the existing row is free
	// (AVAILABLE, or an expired hold never paid for). The whole thing is inside
	// the transaction, so a clash on day three rolls back days one and two as
	// well - a partially booked event is worse than a rejected one.
	end := in.EndDate
	if end.Before(in.EventDate) {
		end = in.EventDate
	}
	for d := in.EventDate; !d.After(end); d = d.AddDate(0, 0, 1) {
		// FULL_DAY overlaps both halves, but the unique index is per slot, so
		// FULL_DAY and EVENING on one date were two rows and both succeeded.
		// Serialise every claim on (hall, date) - an advisory lock, because
		// there may be no row yet to lock - then refuse a FULL_DAY against any
		// booked half, and a half against a booked FULL_DAY. Dates are claimed
		// in ascending order, so two multi-day bookings cannot deadlock.
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1::text || '/' || $2::date::text))`,
			in.FacilityID, d); err != nil {
			return nil, err
		}
		var clash bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM hall_availability
			  WHERE facility_id = $1 AND date = $2 AND status = 'BOOKED'
			    AND (slot_type = 'FULL_DAY' OR $3 = 'FULL_DAY'))`,
			in.FacilityID, d, in.SlotType).Scan(&clash); err != nil {
			return nil, err
		}
		if clash {
			return nil, ErrSlotTaken
		}
		tag, err := tx.Exec(ctx,
			`INSERT INTO hall_availability (facility_id, date, slot_type, status, booking_id)
			 VALUES ($1, $2, $3, 'BOOKED', $4)
			 ON CONFLICT (facility_id, date, slot_type) DO UPDATE
			    SET status = 'BOOKED', booking_id = $4, updated_at = CURRENT_TIMESTAMP
			 WHERE hall_availability.status = 'AVAILABLE'`,
			in.FacilityID, d, in.SlotType, bookingID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, ErrSlotTaken
		}
	}

	for _, pkgID := range in.PackageIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO booking_packages (booking_id, package_id, price_at_booking)
			 SELECT $1, id, price FROM hall_packages WHERE id = $2 AND is_deleted = FALSE`,
			bookingID, pkgID); err != nil {
			return nil, err
		}
	}
	for _, a := range in.Addons {
		if _, err := tx.Exec(ctx,
			`INSERT INTO booking_addons (booking_id, addon_id, quantity, price_at_booking)
			 SELECT $1, id, $3, price FROM add_on_services WHERE id = $2 AND is_deleted = FALSE`,
			bookingID, a.AddonID, a.Quantity); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO booking_status_history (booking_id, to_status, reason)
		 VALUES ($1, 'PENDING', 'Booking created')`, bookingID); err != nil {
		return nil, err
	}
	// Redeemed in this transaction, so a slot clash above - or anything else
	// that rolls it back - never spends a use.
	if in.CouponID != "" {
		ok, err := coupon.Redeem(ctx, tx, in.CouponID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrCouponUnavailable
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.Get(ctx, bookingID)
}

type HotelBookingInput struct {
	UserID        int64
	FacilityID    string
	CheckIn       time.Time
	CheckOut      time.Time
	TotalAmount   float64
	IdempotentKey string
	GuestName     *string
	GuestEmail    *string
	GuestPhone    *string
	HoldFor       time.Duration
	Rooms         []RoomLine
	// DiscountAmount, CouponID and CouponCode as for a hall booking.
	DiscountAmount float64
	CouponID       string
	CouponCode     string
}

type RoomLine struct {
	RoomTypeID string
	Quantity   int
	Price      float64
}

// CreateHotelBooking decrements per-night room inventory for every night in the
// stay. The UPDATE's WHERE clause is the guarantee: it only matches while enough
// rooms remain, so concurrent bookings cannot oversell. The CHECK constraint on
// the table is the second line of defence if this query is ever changed.
func (r *Repo) CreateHotelBooking(ctx context.Context, in HotelBookingInput) (*Booking, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var bookingID string
	var expiresAt *time.Time
	if in.HoldFor > 0 {
		t := time.Now().Add(in.HoldFor)
		expiresAt = &t
	}

	err = tx.QueryRow(ctx,
		`INSERT INTO bookings (user_id, target_type, target_id, check_in, check_out,
		    total_amount, idempotent_key, expires_at, guest_name, guest_email, guest_phone,
		    discount_amount, coupon_id, coupon_code)
		 VALUES ($1,'HOTEL',$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,$10,
		         $11, NULLIF($12,'')::uuid, NULLIF($13,'')) RETURNING id`,
		in.UserID, in.FacilityID, in.CheckIn, in.CheckOut, in.TotalAmount,
		in.IdempotentKey, expiresAt, in.GuestName, in.GuestEmail, in.GuestPhone,
		in.DiscountAmount, in.CouponID, in.CouponCode).Scan(&bookingID)
	if err != nil {
		if isUnique(err, "idx_bookings_idempotent") {
			return nil, ErrDuplicateKey
		}
		return nil, err
	}

	// Lock room types in one global order. In request order, [A,B] racing
	// [B,A] deadlocked and one of them got a 500.
	rooms := append([]RoomLine(nil), in.Rooms...)
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].RoomTypeID < rooms[j].RoomTypeID })

	// Nights are [check_in, check_out) - the checkout day itself is not occupied.
	for _, room := range rooms {
		for d := in.CheckIn; d.Before(in.CheckOut); d = d.AddDate(0, 0, 1) {
			// Materialise the night from the room type's default capacity if it
			// has never been touched, then claim against it.
			if _, err := tx.Exec(ctx,
				`INSERT INTO room_availability (room_type_id, date, total_rooms, booked_rooms)
				 SELECT id, $2, total_rooms, 0 FROM room_types WHERE id = $1
				 ON CONFLICT (room_type_id, date) DO NOTHING`,
				room.RoomTypeID, d); err != nil {
				return nil, err
			}
			tag, err := tx.Exec(ctx,
				`UPDATE room_availability SET booked_rooms = booked_rooms + $3,
				    updated_at = CURRENT_TIMESTAMP
				 WHERE room_type_id = $1 AND date = $2
				   AND total_rooms - booked_rooms >= $3`,
				room.RoomTypeID, d, room.Quantity)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() == 0 {
				return nil, ErrNoInventory
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO booking_rooms (booking_id, room_type_id, quantity, price_at_booking)
			 VALUES ($1, $2, $3, $4)`,
			bookingID, room.RoomTypeID, room.Quantity, room.Price); err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO booking_status_history (booking_id, to_status, reason)
		 VALUES ($1, 'PENDING', 'Booking created')`, bookingID); err != nil {
		return nil, err
	}
	if in.CouponID != "" {
		ok, err := coupon.Redeem(ctx, tx, in.CouponID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrCouponUnavailable
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.Get(ctx, bookingID)
}

// SetStatus moves a booking between states and records the transition. Returns
// ErrNotFound if the booking is not currently in one of the expected states, so
// callers cannot, for example, confirm an already-cancelled booking.
func (r *Repo) SetStatus(ctx context.Context, id, to, reason string, from ...string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var current string
	err = tx.QueryRow(ctx,
		`SELECT status FROM bookings WHERE id = $1 AND status = ANY($2) FOR UPDATE`,
		id, from).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if to == "REJECTED" {
		if _, err := tx.Exec(ctx,
			`UPDATE bookings SET status = $2, rejection_reason = $3, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
			id, to, reason); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx,
			`UPDATE bookings SET status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
			id, to); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO booking_status_history (booking_id, from_status, to_status, reason)
		 VALUES ($1, $2, $3, $4)`, id, current, to, reason); err != nil {
		return err
	}
	// A booking that ends without being used gives back its dates or rooms and
	// its coupon use. The FOR UPDATE and the from-status guard above make this
	// run exactly once per booking, however often a cancel is retried - which
	// is why the release lives here and nowhere else. Released separately, a
	// repeated cancel decremented a hotel's booked_rooms on every call, and
	// the sweeper could free the slot of a booking paid a moment later.
	if (to == "CANCELLED" || to == "EXPIRED" || to == "REJECTED") &&
		(current == "PENDING" || current == "CONFIRMED") {
		if err := releaseInventory(ctx, tx, id); err != nil {
			return err
		}
		if err := coupon.Release(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// releaseInventory frees whatever a booking was holding. Only SetStatus calls
// it, inside the transaction that ends the booking.
func releaseInventory(ctx context.Context, tx pgx.Tx, id string) error {
	b, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM bookings WHERE id = $1`, id))
	if err != nil {
		return err
	}

	if b.TargetType == "HALL" {
		if _, err := tx.Exec(ctx,
			`UPDATE hall_availability SET status = 'AVAILABLE', booking_id = NULL,
			    updated_at = CURRENT_TIMESTAMP
			 WHERE booking_id = $1`, id); err != nil {
			return err
		}
	} else {
		rows, err := tx.Query(ctx,
			`SELECT room_type_id, quantity FROM booking_rooms WHERE booking_id = $1`, id)
		if err != nil {
			return err
		}
		type line struct {
			id  string
			qty int
		}
		var lines []line
		for rows.Next() {
			var l line
			if err := rows.Scan(&l.id, &l.qty); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, l)
		}
		rows.Close()
		// A truncated read here would commit a partial release.
		if err := rows.Err(); err != nil {
			return err
		}

		for _, l := range lines {
			for d := b.CheckIn; d.Before(b.CheckOut); d = d.AddDate(0, 0, 1) {
				// GREATEST guards against ever driving the counter negative.
				if _, err := tx.Exec(ctx,
					`UPDATE room_availability
					 SET booked_rooms = GREATEST(booked_rooms - $3, 0),
					     updated_at = CURRENT_TIMESTAMP
					 WHERE room_type_id = $1 AND date = $2`,
					l.id, d, l.qty); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *Repo) ListForUser(ctx context.Context, userID int64, page, size int) ([]Booking, int64, error) {
	var total int64
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM bookings WHERE user_id = $1 AND is_deleted = FALSE`,
		userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.Query(ctx,
		`SELECT `+cols+` FROM bookings WHERE user_id = $1 AND is_deleted = FALSE
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, size, httpx.Offset(page, size))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Booking{}
	for rows.Next() {
		b, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *b)
	}
	return out, total, rows.Err()
}

// FacilityOwner is who owns the venue a booking is for. Used to decide whether
// the caller may accept or reject it.
func (r *Repo) FacilityOwner(ctx context.Context, facilityID string) (int64, error) {
	var ownerID int64
	err := r.db.QueryRow(ctx,
		`SELECT owner_id FROM facilities WHERE id = $1 AND is_deleted = FALSE`,
		facilityID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return ownerID, err
}

// ListForOwner is the bookings made at an owner's venues - their side of
// ListForUser. An empty status means every status.
func (r *Repo) ListForOwner(ctx context.Context, ownerID int64, status string, page, size int) ([]Booking, int64, error) {
	var total int64
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM bookings b JOIN facilities f ON f.id = b.target_id
		  WHERE f.owner_id = $1 AND b.is_deleted = FALSE
		    AND ($2 = '' OR b.status = $2)`, ownerID, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.Query(ctx,
		`SELECT `+ownerCols+` FROM bookings b JOIN facilities f ON f.id = b.target_id
		  WHERE f.owner_id = $1 AND b.is_deleted = FALSE
		    AND ($2 = '' OR b.status = $2)
		  ORDER BY b.created_at DESC LIMIT $3 OFFSET $4`,
		ownerID, status, size, httpx.Offset(page, size))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Booking{}
	for rows.Next() {
		b, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	ptrs := make([]*Booking, len(out))
	for i := range out {
		ptrs[i] = &out[i]
	}
	_ = r.EnrichUsers(ctx, ptrs)
	return out, total, nil
}

// ExpireHolds releases bookings whose payment window elapsed. Returns the ids
// it expired - also on error, so the ones already expired are still announced.
func (r *Repo) ExpireHolds(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id FROM bookings
		 WHERE status = 'PENDING' AND expires_at IS NOT NULL AND expires_at < CURRENT_TIMESTAMP`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var expired []string
	for _, id := range ids {
		// SetStatus releases the inventory itself, under its status guard.
		if err := r.SetStatus(ctx, id, "EXPIRED", "Payment window elapsed", "PENDING"); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue // someone paid for it in the meantime
			}
			return expired, err
		}
		expired = append(expired, id)
	}
	return expired, nil
}

func (r *Repo) MarkPaid(ctx context.Context, bookingID string, amount float64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE bookings SET paid_amount = paid_amount + $2, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1`, bookingID, amount)
	return err
}

func isUnique(err error, index string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		(index == "" || pgErr.ConstraintName == index)
}

// Enrich fills in the venue summary and review state for a page of bookings.
//
// One query for the whole page, not one per booking: a 20-row bookings screen
// would otherwise issue 40 round trips, and the list endpoint is the most
// frequently hit screen in the app.
//
// Missing facilities are left nil rather than failing the request - a booking
// whose venue was deleted must still appear in the customer's history.
func (r *Repo) Enrich(ctx context.Context, userID int64, bookings []*Booking) error {
	if len(bookings) == 0 {
		return nil
	}
	ids := make([]string, 0, len(bookings))
	seen := map[string]bool{}
	for _, b := range bookings {
		if !seen[b.TargetID] {
			seen[b.TargetID] = true
			ids = append(ids, b.TargetID)
		}
	}

	rows, err := r.db.Query(ctx, `
		SELECT f.id::text, f.name, f.type, f.city, f.full_address,
		       f.contact_phone, COALESCE(f.avg_rating,0),
		       f.lat::double precision, f.lng::double precision,
		       (SELECT i.url FROM facility_images i
		         WHERE i.facility_id = f.id
		         ORDER BY i.is_cover DESC, i.sort_order LIMIT 1),
		       rv.id::text, rv.rating
		  FROM facilities f
		  LEFT JOIN reviews rv
		         ON rv.facility_id = f.id AND rv.user_id = $2 AND rv.is_deleted = FALSE
		 WHERE f.id = ANY($1::uuid[])`, ids, userID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type info struct {
		f        BookingFacility
		reviewID *string
		rating   *int
	}
	byID := map[string]info{}
	for rows.Next() {
		var x info
		if err := rows.Scan(&x.f.ID, &x.f.Name, &x.f.Type, &x.f.City, &x.f.Address,
			&x.f.Phone, &x.f.AvgRating, &x.f.Lat, &x.f.Lng, &x.f.CoverImage,
			&x.reviewID, &x.rating); err != nil {
			return err
		}
		// The column says MARRIAGE_HALL, the booking's own targetType says
		// HALL. Translate here so one response never shows both words for the
		// same venue.
		x.f.Type = venuetype.API(x.f.Type)
		byID[x.f.ID] = x
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, b := range bookings {
		x, ok := byID[b.TargetID]
		if !ok {
			continue
		}
		f := x.f
		b.Facility = &f
		b.HasReviewed = x.reviewID != nil
		b.MyReviewID, b.MyRating = x.reviewID, x.rating
		// Matches what POST /api/v1/reviews actually enforces: a confirmed or
		// completed stay, and one review per venue per user.
		b.CanReview = !b.HasReviewed &&
			(b.Status == "CONFIRMED" || b.Status == "COMPLETED")
	}
	return nil
}

// EnrichUsers attaches booker details (name, email, phone, address) to each
// booking. Used on the owner's booking feed so venue owners see who booked
// their venue without requiring additional API calls.
func (r *Repo) EnrichUsers(ctx context.Context, bookings []*Booking) error {
	if len(bookings) == 0 {
		return nil
	}
	userIDs := make([]int64, 0, len(bookings))
	seen := map[int64]bool{}
	for _, b := range bookings {
		if b != nil && b.UserID > 0 && !seen[b.UserID] {
			seen[b.UserID] = true
			userIDs = append(userIDs, b.UserID)
		}
	}
	if len(userIDs) == 0 {
		return nil
	}

	rows, err := r.db.Query(ctx, `
		SELECT u.id,
		       COALESCE(NULLIF(TRIM(u.full_name), ''), NULLIF(TRIM(CONCAT_WS(' ', p.first_name, p.last_name)), ''), 'User') AS name,
		       u.email,
		       u.phone_number,
		       (SELECT NULLIF(CONCAT_WS(', ',
		           NULLIF(TRIM(a.street), ''),
		           NULLIF(TRIM(a.city), ''),
		           NULLIF(TRIM(a.state), ''),
		           NULLIF(TRIM(a.zip_code), ''),
		           NULLIF(TRIM(a.country), '')
		       ), '')
		        FROM addresses a
		        WHERE a.user_profile_id = u.id AND a.is_deleted = FALSE
		        ORDER BY a.id LIMIT 1) AS address
		  FROM users u
		  LEFT JOIN user_profiles p ON p.id = u.id
		 WHERE u.id = ANY($1)`, userIDs)
	if err != nil {
		return err
	}
	defer rows.Close()

	byID := map[int64]BookingUser{}
	for rows.Next() {
		var u BookingUser
		if err := rows.Scan(&u.UserID, &u.Name, &u.Email, &u.PhoneNumber, &u.Address); err != nil {
			return err
		}
		byID[u.UserID] = u
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, b := range bookings {
		if b == nil {
			continue
		}
		if u, ok := byID[b.UserID]; ok {
			userCopy := u
			if (userCopy.Name == "" || userCopy.Name == "User") && b.GuestName != nil && *b.GuestName != "" {
				userCopy.Name = *b.GuestName
			}
			if userCopy.Email == nil && b.GuestEmail != nil {
				userCopy.Email = b.GuestEmail
			}
			if userCopy.PhoneNumber == nil && b.GuestPhone != nil {
				userCopy.PhoneNumber = b.GuestPhone
			}
			b.User = &userCopy
		} else {
			name := "User"
			if b.GuestName != nil && *b.GuestName != "" {
				name = *b.GuestName
			}
			b.User = &BookingUser{
				UserID:      b.UserID,
				Name:        name,
				Email:       b.GuestEmail,
				PhoneNumber: b.GuestPhone,
				Address:     nil,
			}
		}
	}
	return nil
}
