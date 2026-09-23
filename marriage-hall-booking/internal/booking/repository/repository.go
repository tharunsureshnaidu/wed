package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrSlotTaken    = errors.New("slot already booked")
	ErrNoInventory  = errors.New("insufficient inventory")
	ErrDuplicateKey = errors.New("idempotency key already used")
)

type Repo struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Repo { return &Repo{db: db} }

type Booking struct {
	ID         string    `json:"id"`
	UserID     int64     `json:"userId"`
	TargetType string    `json:"targetType"`
	TargetID   string    `json:"targetId"`
	CheckIn    time.Time `json:"checkIn"`
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
	CanReview      bool       `json:"canReview"`
	HasReviewed    bool       `json:"hasReviewed"`
	MyRating       *int       `json:"myRating"`
	MyReviewID     *string    `json:"myReviewId"`
	EventType      *string    `json:"eventType"`
	SlotType       *string    `json:"slotType,omitempty"`
	TotalAmount    float64    `json:"totalAmount"`
	DiscountAmount float64    `json:"discountAmount"`
	PaidAmount     float64    `json:"paidAmount"`
	Status         string     `json:"status"`
	IdempotentKey  *string    `json:"idempotentKey,omitempty"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	GuestName      *string    `json:"guestName,omitempty"`
	GuestEmail     *string    `json:"guestEmail,omitempty"`
	GuestPhone     *string    `json:"guestPhone,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

const cols = `id, user_id, target_type, target_id, check_in, check_out, slot_type,
	total_amount, COALESCE(discount_amount,0), COALESCE(paid_amount,0), status,
	idempotent_key, expires_at, guest_name, guest_email, guest_phone, created_at,
	to_char(start_time,'HH24:MI'), to_char(end_time,'HH24:MI'), guest_count, event_type,
	room_count`

func scan(row pgx.Row) (*Booking, error) {
	var b Booking
	err := row.Scan(&b.ID, &b.UserID, &b.TargetType, &b.TargetID, &b.CheckIn, &b.CheckOut,
		&b.SlotType, &b.TotalAmount, &b.DiscountAmount, &b.PaidAmount, &b.Status,
		&b.IdempotentKey, &b.ExpiresAt, &b.GuestName, &b.GuestEmail, &b.GuestPhone, &b.CreatedAt,
		&b.StartTime, &b.EndTime, &b.GuestCount, &b.EventType, &b.RoomCount)
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
		    guest_phone, start_time, end_time, guest_count, event_type, room_count)
		 VALUES ($1,'HALL',$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,
		         NULLIF($12,'')::time, NULLIF($13,'')::time, $14, $15, $16) RETURNING id`,
		in.UserID, in.FacilityID, in.EventDate, in.EndDate, in.SlotType, in.TotalAmount,
		in.IdempotentKey, expiresAt, in.GuestName, in.GuestEmail, in.GuestPhone,
		in.StartTime, in.EndTime, in.GuestCount, in.EventType, in.RoomCount).Scan(&bookingID)
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
		    total_amount, idempotent_key, expires_at, guest_name, guest_email, guest_phone)
		 VALUES ($1,'HOTEL',$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,$10) RETURNING id`,
		in.UserID, in.FacilityID, in.CheckIn, in.CheckOut, in.TotalAmount,
		in.IdempotentKey, expiresAt, in.GuestName, in.GuestEmail, in.GuestPhone).Scan(&bookingID)
	if err != nil {
		if isUnique(err, "idx_bookings_idempotent") {
			return nil, ErrDuplicateKey
		}
		return nil, err
	}

	// Nights are [check_in, check_out) - the checkout day itself is not occupied.
	for _, room := range in.Rooms {
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

	if _, err := tx.Exec(ctx,
		`UPDATE bookings SET status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		id, to); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO booking_status_history (booking_id, from_status, to_status, reason)
		 VALUES ($1, $2, $3, $4)`, id, current, to, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReleaseInventory frees whatever a booking was holding. Used when a booking is
// cancelled or expires unpaid.
func (r *Repo) ReleaseInventory(ctx context.Context, id string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

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
	return tx.Commit(ctx)
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
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, size, page*size)
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

// ExpireHolds releases bookings whose payment window elapsed. Returns how many
// were expired, so the sweeper can log real work.
func (r *Repo) ExpireHolds(ctx context.Context) (int, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id FROM bookings
		 WHERE status = 'PENDING' AND expires_at IS NOT NULL AND expires_at < CURRENT_TIMESTAMP`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, id := range ids {
		if err := r.ReleaseInventory(ctx, id); err != nil {
			return n, err
		}
		if err := r.SetStatus(ctx, id, "EXPIRED", "Payment window elapsed", "PENDING"); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue // someone paid for it in the meantime
			}
			return n, err
		}
		n++
	}
	return n, nil
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
		 WHERE f.id::text = ANY($1)`, ids, userID)
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
