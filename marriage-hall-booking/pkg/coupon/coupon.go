// Package coupon is the one definition of whether a code applies to a venue and
// what it takes off.
//
// Its own package because two modules need it: the coupon handler previews a
// code, the booking service redeems one. The same split as pkg/eventtypes - a
// handler importing a handler is the wrong direction - and one rule means the
// preview can never promise a discount the booking then refuses.
package coupon

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is a pool or a transaction: Load runs before a booking's
// transaction, Redeem and Release inside one.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// AppliesSQL is true when coupon c may be used at facility f. The most
// specific scope wins: a coupon pinned to a venue applies there only, a
// vendor's coupon to that vendor's venues, and an admin coupon to every venue
// of its type - or to every venue at all, when that type is ALL. Shared by Load and the public list, so what the offers screen
// shows is exactly what checkout accepts.
const AppliesSQL = `CASE
	WHEN c.facility_id IS NOT NULL THEN c.facility_id = f.id
	WHEN c.vendor_id IS NOT NULL THEN EXISTS (
	     SELECT 1 FROM vendors v WHERE v.id = c.vendor_id AND v.user_id = f.owner_id)
	WHEN c.facility_type = 'ALL' THEN TRUE
	ELSE c.facility_type = f.type
END`

// LiveSQL is the part of "usable now" that does not depend on the venue.
const LiveSQL = `c.is_deleted = FALSE AND c.is_active
	AND (c.valid_from IS NULL OR c.valid_from <= CURRENT_TIMESTAMP)
	AND (c.valid_until IS NULL OR c.valid_until >= CURRENT_TIMESTAMP)`

type Coupon struct {
	ID            string
	Code          string
	DiscountType  string // PERCENT or FLAT
	DiscountValue float64
	MaxDiscount   *float64
	MinBooking    *float64
	UsageLimit    *int
	UsedCount     int
	applies       bool
}

// Error is a reason a code cannot be used, with the status and code the API
// reports it under.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

var (
	ErrInvalid    = &Error{404, "COUPON_INVALID", "That coupon is not valid or has expired"}
	ErrExhausted  = &Error{409, "COUPON_EXHAUSTED", "This coupon has been fully redeemed"}
	ErrMinAmount  = &Error{400, "COUPON_MIN_AMOUNT", "This coupon needs a minimum booking amount"}
	ErrWrongVenue = &Error{400, "COUPON_WRONG_FACILITY", "This coupon does not apply to that venue"}
)

// Load finds a live coupon by code and whether it applies at facilityID.
func Load(ctx context.Context, q Querier, code, facilityID string) (*Coupon, error) {
	var c Coupon
	var applies *bool
	err := q.QueryRow(ctx, `
		SELECT c.id::text, c.code, c.discount_type, c.discount_value, c.max_discount,
		       c.min_booking_amount, c.usage_limit, c.used_count, `+AppliesSQL+`
		  FROM coupons c
		  LEFT JOIN facilities f ON f.id = $2 AND f.is_deleted = FALSE
		 WHERE upper(c.code) = upper($1) AND `+LiveSQL,
		strings.TrimSpace(code), facilityID).Scan(&c.ID, &c.Code, &c.DiscountType,
		&c.DiscountValue, &c.MaxDiscount, &c.MinBooking, &c.UsageLimit, &c.UsedCount, &applies)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, err
	}
	// NULL when the facility does not exist: the coupon applies to nothing.
	c.applies = applies != nil && *applies
	return &c, nil
}

// Check reports why the coupon cannot be used on amount, or nil.
func (c *Coupon) Check(amount float64) error {
	switch {
	case !c.applies:
		return ErrWrongVenue
	case c.UsageLimit != nil && c.UsedCount >= *c.UsageLimit:
		return ErrExhausted
	case c.MinBooking != nil && amount < *c.MinBooking:
		return ErrMinAmount
	}
	return nil
}

// Discount is what the coupon takes off amount: capped by MaxDiscount for a
// percent coupon, never more than the amount itself - a flat coupon larger than
// the booking would otherwise produce a negative total. Rounded to whole units,
// like the listing's discountedPrice.
func (c *Coupon) Discount(amount float64) float64 {
	d := c.DiscountValue
	if c.DiscountType == "PERCENT" {
		d = amount * c.DiscountValue / 100
		if c.MaxDiscount != nil && d > *c.MaxDiscount {
			d = *c.MaxDiscount
		}
	}
	return math.Min(math.Round(d), amount)
}

// Redeem counts one use, inside the booking's transaction. False means the
// coupon ran out, or stopped being live, since Load: the conditional UPDATE is
// what keeps a limit of 100 from being exceeded by concurrent bookings.
func Redeem(ctx context.Context, tx Querier, id string) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE coupons c SET used_count = used_count + 1, updated_at = CURRENT_TIMESTAMP
		 WHERE c.id = $1 AND `+LiveSQL+`
		   AND (c.usage_limit IS NULL OR c.used_count < c.usage_limit)`, id)
	return tag.RowsAffected() == 1, err
}

// Release gives back the use a booking redeemed, when it is cancelled or its
// hold expires - otherwise abandoned checkouts use up a limited coupon without
// anyone paying. Called inside the status change's transaction, which is
// guarded by the booking's current status, so a use is never returned twice.
func Release(ctx context.Context, tx Querier, bookingID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE coupons SET used_count = used_count - 1, updated_at = CURRENT_TIMESTAMP
		 WHERE id = (SELECT coupon_id FROM bookings WHERE id = $1) AND used_count > 0`,
		bookingID)
	return err
}
