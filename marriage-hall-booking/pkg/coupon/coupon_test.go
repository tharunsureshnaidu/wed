package coupon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func TestCouponScope(t *testing.T) {
	adminType := "MARRIAGE_HALL"
	adminCoupon := Coupon{FacilityType: &adminType}
	if !adminCoupon.IsAdmin() || adminCoupon.Scope() != "ADMIN" {
		t.Errorf("expected admin coupon with facility_type to be ADMIN, got %s", adminCoupon.Scope())
	}

	facilityID := "fac-1"
	vendorCoupon := Coupon{FacilityID: &facilityID}
	if vendorCoupon.IsAdmin() || vendorCoupon.Scope() != "VENDOR" {
		t.Errorf("expected vendor coupon with facility_id to be VENDOR, got %s", vendorCoupon.Scope())
	}

	vendorID := "ven-1"
	vendorCoupon2 := Coupon{VendorID: &vendorID}
	if vendorCoupon2.IsAdmin() || vendorCoupon2.Scope() != "VENDOR" {
		t.Errorf("expected vendor coupon with vendor_id to be VENDOR, got %s", vendorCoupon2.Scope())
	}
}

type mockRow struct {
	coupon  Coupon
	applies *bool
	err     error
}

func (m mockRow) Scan(dest ...any) error {
	if m.err != nil {
		return m.err
	}
	*dest[0].(*string) = m.coupon.ID
	*dest[1].(*string) = m.coupon.Code
	*dest[2].(*string) = m.coupon.DiscountType
	*dest[3].(*float64) = m.coupon.DiscountValue
	*dest[4].(**float64) = m.coupon.MaxDiscount
	*dest[5].(**float64) = m.coupon.MinBooking
	*dest[6].(**int) = m.coupon.UsageLimit
	*dest[7].(*int) = m.coupon.UsedCount
	applies := true
	if m.applies != nil {
		applies = *m.applies
	}
	*dest[8].(**bool) = &applies
	*dest[9].(**string) = m.coupon.FacilityID
	*dest[10].(**string) = m.coupon.VendorID
	*dest[11].(**string) = m.coupon.FacilityType
	return nil
}

type mockQuerier struct {
	coupons map[string]Coupon
}

func (m *mockQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	code := args[0].(string)
	if c, ok := m.coupons[strings.ToUpper(code)]; ok {
		return mockRow{coupon: c}
	}
	return mockRow{err: pgx.ErrNoRows}
}

func (m *mockQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func TestPriceCoupons(t *testing.T) {
	adminType := "MARRIAGE_HALL"
	facID := "fac-1"

	mq := &mockQuerier{
		coupons: map[string]Coupon{
			"ADMIN10": {
				ID: "c-admin", Code: "ADMIN10", DiscountType: "PERCENT", DiscountValue: 10,
				FacilityType: &adminType,
			},
			"ADMIN_FLAT": {
				ID: "c-admin-flat", Code: "ADMIN_FLAT", DiscountType: "FLAT", DiscountValue: 5000,
				FacilityType: &adminType,
			},
			"HALL20": {
				ID: "c-hall", Code: "HALL20", DiscountType: "PERCENT", DiscountValue: 20,
				FacilityID: &facID,
			},
			"HALL_FLAT": {
				ID: "c-hall-flat", Code: "HALL_FLAT", DiscountType: "FLAT", DiscountValue: 10000,
				FacilityID: &facID,
			},
		},
	}

	ctx := context.Background()

	// 1. Valid: 1 Vendor + 1 Admin
	// Amount: 100,000
	// Hall 20% off: 20,000 -> Remaining: 80,000
	// Admin flat 5,000 off: 5,000 -> Remaining: 75,000
	// Total discount: 25,000
	applied, totalDiscount, err := PriceCoupons(ctx, mq, []string{"HALL20", "ADMIN_FLAT"}, facID, 100000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("expected 2 applied coupons, got %d", len(applied))
	}
	if totalDiscount != 25000 {
		t.Errorf("expected total discount 25000, got %v", totalDiscount)
	}
	if applied[0].Scope != "VENDOR" || applied[0].DiscountAmount != 20000 {
		t.Errorf("expected applied[0] to be VENDOR with 20000, got %s: %v", applied[0].Scope, applied[0].DiscountAmount)
	}
	if applied[1].Scope != "ADMIN" || applied[1].DiscountAmount != 5000 {
		t.Errorf("expected applied[1] to be ADMIN with 5000, got %s: %v", applied[1].Scope, applied[1].DiscountAmount)
	}

	// 2. Reject 2 Admin coupons
	_, _, err = PriceCoupons(ctx, mq, []string{"ADMIN10", "ADMIN_FLAT"}, facID, 100000)
	if !errors.Is(err, ErrDuplicateScope) {
		t.Errorf("expected ErrDuplicateScope for 2 admin coupons, got %v", err)
	}

	// 3. Reject 2 Vendor coupons
	_, _, err = PriceCoupons(ctx, mq, []string{"HALL20", "HALL_FLAT"}, facID, 100000)
	if !errors.Is(err, ErrDuplicateScope) {
		t.Errorf("expected ErrDuplicateScope for 2 vendor coupons, got %v", err)
	}

	// 4. Reject more than 2 coupons
	_, _, err = PriceCoupons(ctx, mq, []string{"HALL20", "ADMIN10", "HALL_FLAT"}, facID, 100000)
	if !errors.Is(err, ErrMaxCoupons) {
		t.Errorf("expected ErrMaxCoupons for 3 coupons, got %v", err)
	}
	if err != nil && err.Error() != "Maximum 2 coupons can be applied per booking" {
		t.Errorf("unexpected error message: %v", err.Error())
	}

	// 5. Reject duplicate codes
	_, _, err = PriceCoupons(ctx, mq, []string{"HALL20", "hall20"}, facID, 100000)
	if !errors.Is(err, ErrDuplicateCode) {
		t.Errorf("expected ErrDuplicateCode for duplicate code, got %v", err)
	}
}

// evalApplies mirrors the SQL AppliesSQL evaluation logic for tests
func evalApplies(c Coupon, facilityID, facilityType string, facilityExists bool) bool {
	if !facilityExists {
		return false
	}
	if c.VendorID != nil {
		return c.FacilityID != nil && *c.FacilityID == facilityID
	}
	if c.FacilityID != nil {
		return *c.FacilityID == facilityID
	}
	if c.FacilityType != nil {
		if *c.FacilityType == "ALL" {
			return true
		}
		if *c.FacilityType == facilityType ||
			(*c.FacilityType == "HALL" && facilityType == "MARRIAGE_HALL") ||
			(*c.FacilityType == "MARRIAGE_HALL" && facilityType == "HALL") {
			return true
		}
	}
	return false
}

type venueAwareMockQuerier struct {
	coupons    map[string]Coupon
	facilities map[string]string // id -> type
}

func (m *venueAwareMockQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	code := args[0].(string)
	facilityID := args[1].(string)
	c, ok := m.coupons[strings.ToUpper(code)]
	if !ok {
		return mockRow{err: pgx.ErrNoRows}
	}
	facType, facExists := m.facilities[facilityID]
	applies := evalApplies(c, facilityID, facType, facExists)
	c.applies = applies

	return mockRow{
		coupon:  c,
		applies: &applies,
	}
}

func (m *venueAwareMockQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func TestVenueSpecificAndGlobalCoupons(t *testing.T) {
	hotelA := "hotel-a-id"
	hotelB := "hotel-b-id"
	hallA := "hall-a-id"
	hallB := "hall-b-id"

	vendor1 := "vendor-1"
	vendor2 := "vendor-2"

	scopeHotel := "HOTEL"
	scopeHall := "MARRIAGE_HALL"
	scopeAll := "ALL"

	mq := &venueAwareMockQuerier{
		facilities: map[string]string{
			hotelA: "HOTEL",
			hotelB: "HOTEL",
			hallA:  "MARRIAGE_HALL",
			hallB:  "MARRIAGE_HALL",
		},
		coupons: map[string]Coupon{
			"HOTEL_A_COUPON": {
				ID: "c1", Code: "HOTEL_A_COUPON", DiscountType: "PERCENT", DiscountValue: 15,
				VendorID: &vendor1, FacilityID: &hotelA,
			},
			"HALL_A_COUPON": {
				ID: "c2", Code: "HALL_A_COUPON", DiscountType: "PERCENT", DiscountValue: 20,
				VendorID: &vendor2, FacilityID: &hallA,
			},
			"ADMIN_ALL": {
				ID: "c3", Code: "ADMIN_ALL", DiscountType: "FLAT", DiscountValue: 2000,
				FacilityType: &scopeAll,
			},
			"ADMIN_HOTEL": {
				ID: "c4", Code: "ADMIN_HOTEL", DiscountType: "PERCENT", DiscountValue: 10,
				FacilityType: &scopeHotel,
			},
			"ADMIN_HALL": {
				ID: "c5", Code: "ADMIN_HALL", DiscountType: "PERCENT", DiscountValue: 12,
				FacilityType: &scopeHall,
			},
		},
	}

	ctx := context.Background()

	// 1. Hotel A vendor coupon works ONLY on Hotel A
	applied, discount, err := PriceCoupons(ctx, mq, []string{"HOTEL_A_COUPON"}, hotelA, 50000)
	if err != nil || len(applied) != 1 || discount != 7500 {
		t.Errorf("Hotel A coupon should apply on Hotel A: err=%v, applied=%d, disc=%v", err, len(applied), discount)
	}

	// Hotel A vendor coupon fails on Hotel B
	_, _, err = PriceCoupons(ctx, mq, []string{"HOTEL_A_COUPON"}, hotelB, 50000)
	if !errors.Is(err, ErrWrongVenue) {
		t.Errorf("Hotel A coupon must fail on Hotel B, got: %v", err)
	}

	// Hotel A vendor coupon fails on Hall A
	_, _, err = PriceCoupons(ctx, mq, []string{"HOTEL_A_COUPON"}, hallA, 50000)
	if !errors.Is(err, ErrWrongVenue) {
		t.Errorf("Hotel A coupon must fail on Hall A, got: %v", err)
	}

	// 2. Hall A vendor coupon works ONLY on Hall A
	applied, discount, err = PriceCoupons(ctx, mq, []string{"HALL_A_COUPON"}, hallA, 50000)
	if err != nil || len(applied) != 1 || discount != 10000 {
		t.Errorf("Hall A coupon should apply on Hall A: err=%v, applied=%d, disc=%v", err, len(applied), discount)
	}

	// Hall A vendor coupon fails on Hall B
	_, _, err = PriceCoupons(ctx, mq, []string{"HALL_A_COUPON"}, hallB, 50000)
	if !errors.Is(err, ErrWrongVenue) {
		t.Errorf("Hall A coupon must fail on Hall B, got: %v", err)
	}

	// Hall A vendor coupon fails on Hotel A
	_, _, err = PriceCoupons(ctx, mq, []string{"HALL_A_COUPON"}, hotelA, 50000)
	if !errors.Is(err, ErrWrongVenue) {
		t.Errorf("Hall A coupon must fail on Hotel A, got: %v", err)
	}

	// 3. Admin ALL coupon works across both Hotel and Hall
	applied, discount, err = PriceCoupons(ctx, mq, []string{"ADMIN_ALL"}, hotelA, 50000)
	if err != nil || discount != 2000 {
		t.Errorf("Admin ALL should work on Hotel A, got: err=%v, disc=%v", err, discount)
	}
	applied, discount, err = PriceCoupons(ctx, mq, []string{"ADMIN_ALL"}, hallA, 50000)
	if err != nil || discount != 2000 {
		t.Errorf("Admin ALL should work on Hall A, got: err=%v, disc=%v", err, discount)
	}

	// 4. Admin HOTEL coupon works on Hotel, fails on Hall
	applied, discount, err = PriceCoupons(ctx, mq, []string{"ADMIN_HOTEL"}, hotelA, 50000)
	if err != nil || discount != 5000 {
		t.Errorf("Admin HOTEL should work on Hotel A, got: err=%v, disc=%v", err, discount)
	}
	_, _, err = PriceCoupons(ctx, mq, []string{"ADMIN_HOTEL"}, hallA, 50000)
	if !errors.Is(err, ErrWrongVenue) {
		t.Errorf("Admin HOTEL must fail on Hall A, got: %v", err)
	}

	// 5. Admin HALL coupon works on Hall, fails on Hotel
	applied, discount, err = PriceCoupons(ctx, mq, []string{"ADMIN_HALL"}, hallA, 50000)
	if err != nil || discount != 6000 {
		t.Errorf("Admin HALL should work on Hall A, got: err=%v, disc=%v", err, discount)
	}
	_, _, err = PriceCoupons(ctx, mq, []string{"ADMIN_HALL"}, hotelA, 50000)
	if !errors.Is(err, ErrWrongVenue) {
		t.Errorf("Admin HALL must fail on Hotel A, got: %v", err)
	}

	// 6. Maximum 2 coupons allowed per booking (1 Vendor + 1 Admin)
	// Amount: 50,000
	// Hotel A vendor 15% off: 7,500 -> Remaining: 42,500
	// Admin flat 2,000 off: 2,000 -> Remaining: 40,500
	// Total discount: 9,500
	applied, discount, err = PriceCoupons(ctx, mq, []string{"HOTEL_A_COUPON", "ADMIN_ALL"}, hotelA, 50000)
	if err != nil {
		t.Fatalf("unexpected error applying 2 coupons: %v", err)
	}
	if len(applied) != 2 || discount != 9500 {
		t.Errorf("expected 2 applied coupons and 9500 discount, got %d coupons, %v discount", len(applied), discount)
	}

	// 7. Applying 3 coupons is rejected
	_, _, err = PriceCoupons(ctx, mq, []string{"HOTEL_A_COUPON", "ADMIN_ALL", "ADMIN_HOTEL"}, hotelA, 50000)
	if !errors.Is(err, ErrMaxCoupons) {
		t.Errorf("applying 3 coupons must return ErrMaxCoupons, got: %v", err)
	}
}


