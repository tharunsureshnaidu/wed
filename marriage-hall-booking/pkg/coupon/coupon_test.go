package coupon

import (
	"errors"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestDiscount(t *testing.T) {
	for _, c := range []struct {
		name   string
		c      Coupon
		amount float64
		want   float64
	}{
		{"percent", Coupon{DiscountType: "PERCENT", DiscountValue: 10}, 85000, 8500},
		{"percent capped", Coupon{DiscountType: "PERCENT", DiscountValue: 50, MaxDiscount: ptr(5000.0)}, 85000, 5000},
		{"flat", Coupon{DiscountType: "FLAT", DiscountValue: 2000}, 85000, 2000},
		// A flat coupon larger than the booking must not make the total negative.
		{"flat above amount", Coupon{DiscountType: "FLAT", DiscountValue: 2000}, 1500, 1500},
		// Whole units, as the listing prints prices.
		{"rounded", Coupon{DiscountType: "PERCENT", DiscountValue: 7.5}, 1234, 93},
	} {
		if got := c.c.Discount(c.amount); got != c.want {
			t.Errorf("%s: Discount(%v) = %v, want %v", c.name, c.amount, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	for _, c := range []struct {
		name   string
		c      Coupon
		amount float64
		want   error
	}{
		{"usable", Coupon{applies: true}, 1000, nil},
		{"other venue", Coupon{applies: false}, 1000, ErrWrongVenue},
		{"fully redeemed", Coupon{applies: true, UsageLimit: ptr(100), UsedCount: 100}, 1000, ErrExhausted},
		{"last use left", Coupon{applies: true, UsageLimit: ptr(100), UsedCount: 99}, 1000, nil},
		{"below minimum", Coupon{applies: true, MinBooking: ptr(50000.0)}, 49999, ErrMinAmount},
		// The venue check comes first: a code for another venue is "wrong
		// venue", not "exhausted", even when both are true.
		{"wrong venue wins", Coupon{applies: false, UsageLimit: ptr(1), UsedCount: 1}, 1000, ErrWrongVenue},
	} {
		if got := c.c.Check(c.amount); !errors.Is(got, c.want) {
			t.Errorf("%s: Check = %v, want %v", c.name, got, c.want)
		}
	}
}

// An ALL coupon must reach hotels as well as halls. The scope exists because
// an admin coupon could previously only ever be written as MARRIAGE_HALL, so
// "applies to everything" was not expressible.
func TestAppliesSQLHandlesTheAllScope(t *testing.T) {
	if !strings.Contains(AppliesSQL, "'ALL'") {
		t.Error("AppliesSQL does not mention the ALL scope, so an all-venue " +
			"coupon would be created and then match nothing")
	}
	// The ALL branch must come before the facility_type equality, or
	// c.facility_type = f.type would be reached first and never be true for
	// the literal 'ALL'.
	all := strings.Index(AppliesSQL, "'ALL'")
	eq := strings.Index(AppliesSQL, "c.facility_type = f.type")
	if all > eq {
		t.Error("the ALL branch must be tested before the type equality")
	}
}
