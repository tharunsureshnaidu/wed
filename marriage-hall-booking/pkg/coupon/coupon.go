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
// vendor's coupon applies only to the specific venue for which it was created,
// and an admin coupon applies to every venue of its type - or to every venue at all,
// when that type is ALL. Shared by Load and the public list, so what the offers screen
// shows is exactly what checkout accepts.
const AppliesSQL = `CASE
	WHEN f.id IS NULL THEN FALSE
	WHEN c.vendor_id IS NOT NULL THEN (c.facility_id IS NOT NULL AND c.facility_id = f.id)
	WHEN c.facility_id IS NOT NULL THEN c.facility_id = f.id
	WHEN c.facility_type = 'ALL' THEN TRUE
	ELSE (c.facility_type = f.type OR (c.facility_type = 'HALL' AND f.type = 'MARRIAGE_HALL') OR (c.facility_type = 'MARRIAGE_HALL' AND f.type = 'HALL'))
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
	FacilityID    *string
	VendorID      *string
	FacilityType  *string
	applies       bool
}

const (
	ScopeAdmin  = "ADMIN"
	ScopeVendor = "VENDOR"
)

// Scope returns "ADMIN" (platform coupon) or "VENDOR" (hall owner/venue coupon).
func (c *Coupon) Scope() string {
	if c.FacilityType != nil || (c.VendorID == nil && c.FacilityID == nil) {
		return ScopeAdmin
	}
	return ScopeVendor
}

// IsAdmin reports whether this coupon was created platform-wide by an admin.
func (c *Coupon) IsAdmin() bool {
	return c.Scope() == "ADMIN"
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
	ErrInvalid        = &Error{404, "COUPON_INVALID", "That coupon is not valid or has expired"}
	ErrExhausted      = &Error{409, "COUPON_EXHAUSTED", "This coupon has been fully redeemed"}
	ErrMinAmount      = &Error{400, "COUPON_MIN_AMOUNT", "This coupon needs a minimum booking amount"}
	ErrWrongVenue     = &Error{400, "COUPON_WRONG_FACILITY", "This coupon does not apply to that venue"}
	ErrDuplicateScope = &Error{400, "COUPON_DUPLICATE_SCOPE", "Cannot apply multiple coupons of the same type (only 1 Admin and 1 Venue coupon allowed)"}
	ErrDuplicateCode  = &Error{400, "COUPON_DUPLICATE_CODE", "Same coupon code cannot be applied more than once"}
	ErrMaxCoupons     = &Error{400, "COUPON_LIMIT_EXCEEDED", "Maximum 2 coupons can be applied per booking"}
)

// Load finds a live coupon by code and whether it applies at facilityID.
func Load(ctx context.Context, q Querier, code, facilityID string) (*Coupon, error) {
	var c Coupon
	var applies *bool
	err := q.QueryRow(ctx, `
		SELECT c.id::text, c.code, c.discount_type, c.discount_value, c.max_discount,
		       c.min_booking_amount, c.usage_limit, c.used_count, `+AppliesSQL+`,
		       c.facility_id::text, c.vendor_id::text, c.facility_type
		  FROM coupons c
		  LEFT JOIN facilities f ON f.id = $2 AND f.is_deleted = FALSE
		 WHERE upper(c.code) = upper($1) AND `+LiveSQL,
		strings.TrimSpace(code), facilityID).Scan(&c.ID, &c.Code, &c.DiscountType,
		&c.DiscountValue, &c.MaxDiscount, &c.MinBooking, &c.UsageLimit, &c.UsedCount, &applies,
		&c.FacilityID, &c.VendorID, &c.FacilityType)
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

// AppliedCoupon contains the details of a successfully applied coupon and its discount amount.
type AppliedCoupon struct {
	Coupon         *Coupon `json:"coupon"`
	DiscountAmount float64 `json:"discountAmount"`
	Scope          string  `json:"scope"` // "ADMIN" or "VENDOR"
}

// PriceCoupons checks up to 2 coupon codes (max 1 Admin coupon and 1 Hall Owner coupon)
// against a venue and an amount, and computes their discounts in order: Hall Owner coupon first,
// then Admin coupon on the remaining balance.
func PriceCoupons(ctx context.Context, q Querier, codes []string, facilityID string, amount float64) ([]AppliedCoupon, float64, error) {
	if len(codes) == 0 {
		return nil, 0, nil
	}

	seenCodes := make(map[string]bool)
	var cleanCodes []string
	for _, raw := range codes {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if code == "" {
			continue
		}
		if seenCodes[code] {
			return nil, 0, ErrDuplicateCode
		}
		seenCodes[code] = true
		cleanCodes = append(cleanCodes, code)
	}

	if len(cleanCodes) == 0 {
		return nil, 0, nil
	}
	if len(cleanCodes) > 2 {
		return nil, 0, ErrMaxCoupons
	}

	loaded := make([]*Coupon, 0, len(cleanCodes))
	hasAdmin := false
	hasVendor := false

	for _, code := range cleanCodes {
		c, err := Load(ctx, q, code, facilityID)
		if err != nil {
			return nil, 0, err
		}
		if err := c.Check(amount); err != nil {
			return nil, 0, err
		}
		if c.IsAdmin() {
			if hasAdmin {
				return nil, 0, ErrDuplicateScope
			}
			hasAdmin = true
		} else {
			if hasVendor {
				return nil, 0, ErrDuplicateScope
			}
			hasVendor = true
		}
		loaded = append(loaded, c)
	}

	var vendorC, adminC *Coupon
	for _, c := range loaded {
		if c.IsAdmin() {
			adminC = c
		} else {
			vendorC = c
		}
	}

	var results []AppliedCoupon
	totalDiscount := 0.0
	remaining := amount

	// Hall Owner coupon applies first
	if vendorC != nil {
		d := vendorC.Discount(remaining)
		results = append(results, AppliedCoupon{
			Coupon:         vendorC,
			DiscountAmount: d,
			Scope:          "VENDOR",
		})
		totalDiscount += d
		remaining = math.Max(0, remaining-d)
	}

	// Admin coupon applies next on remaining
	if adminC != nil {
		d := adminC.Discount(remaining)
		results = append(results, AppliedCoupon{
			Coupon:         adminC,
			DiscountAmount: d,
			Scope:          "ADMIN",
		})
		totalDiscount += d
		remaining = math.Max(0, remaining-d)
	}

	return results, math.Min(totalDiscount, amount), nil
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
		 WHERE id IN (
		     SELECT coupon_id FROM booking_coupons WHERE booking_id = $1
		     UNION
		     SELECT coupon_id FROM bookings WHERE id = $1 AND coupon_id IS NOT NULL
		     UNION
		     SELECT admin_coupon_id FROM bookings WHERE id = $1 AND admin_coupon_id IS NOT NULL
		     UNION
		     SELECT vendor_coupon_id FROM bookings WHERE id = $1 AND vendor_coupon_id IS NOT NULL
		 ) AND used_count > 0`,
		bookingID)
	return err
}
