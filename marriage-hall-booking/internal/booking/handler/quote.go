package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
)

// Price preview for the review screen.
//
// The screen that shows "hall price / service fee / taxes / total" before the
// user confirms needs those numbers without creating anything. Computing them
// on the client would mean the price rules living in two places and drifting;
// this returns exactly what the booking will charge.
//
// Nothing is written and no slot is held - availability is reported as it
// stands at this moment and can change before the booking is made.
const (
	serviceFeeRate = 0.02 // 2% platform fee
	gstRate        = 0.18 // 18% GST on the hall price and fee
)

type quoteReq struct {
	HallID     string   `json:"hallId"`
	FacilityID string   `json:"facilityId"`
	StartDate  string   `json:"startDate"`
	EndDate    string   `json:"endDate"`
	StartTime  string   `json:"startTime"`
	EndTime    string   `json:"endTime"`
	EventDate  string   `json:"eventDate"` // legacy single-date form
	SlotType   string   `json:"slotType"`
	GuestCount *int     `json:"guestCount"`
	PackageIDs []string `json:"packageIds"`
	// CouponCode is optional, priced exactly as the booking will price it.
	CouponCode string `json:"couponCode"`
}

func (h *Handler) quote(w http.ResponseWriter, r *http.Request) {
	var req quoteReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	facilityID := req.HallID
	if facilityID == "" {
		facilityID = req.FacilityID
	}

	var e validate.Errors
	if !httpx.ValidUUID(facilityID) {
		e = append(e, "A valid hallId is required")
	}
	rawStart, rawEnd := req.StartDate, req.EndDate
	if rawStart == "" {
		rawStart = req.EventDate
	}
	if rawEnd == "" {
		rawEnd = rawStart
	}
	start, err := time.Parse("2006-01-02", rawStart)
	if err != nil {
		e = append(e, "startDate must be YYYY-MM-DD")
	}
	end, err2 := time.Parse("2006-01-02", rawEnd)
	if err2 != nil {
		e = append(e, "endDate must be YYYY-MM-DD")
	}
	if err == nil && err2 == nil && end.Before(start) {
		e = append(e, "endDate cannot be before startDate")
	}
	startTime, ok1 := hhmm(req.StartTime)
	endTime, ok2 := hhmm(req.EndTime)
	if !ok1 || !ok2 {
		e = append(e, "startTime and endTime must be HH:MM")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	slot := req.SlotType
	if startTime != "" {
		slot = slotFor(startTime, endTime)
	} else if slot == "" {
		slot = "FULL_DAY"
	}

	var (
		name        string
		city        *string
		basePrice   *float64
		discountPct *float64
		capacityPax *int
		facType     string
	)
	if err := h.svc.Pool().QueryRow(r.Context(),
		`SELECT name, city, base_price_per_day, capacity_pax, type, `+service.ActiveDiscountSQL+`
		   FROM facilities WHERE id = $1 AND is_deleted = FALSE`,
		facilityID).Scan(&name, &city, &basePrice, &capacityPax, &facType, &discountPct); err != nil {
		response.Error(w, http.StatusNotFound, "Hall not found", "HALL_NOT_FOUND")
		return
	}
	if facType != "MARRIAGE_HALL" {
		response.Error(w, http.StatusBadRequest, "Facility is not a marriage hall", "INVALID_HALL")
		return
	}

	days := int(end.Sub(start).Hours()/24) + 1
	if days < 1 {
		days = 1
	}
	// The same day rate the booking charges: after the venue's advertised
	// discount.
	hallPrice, venueDiscount := 0.0, 0.0
	if basePrice != nil {
		rate := service.DayRate(*basePrice, discountPct)
		hallPrice = rate * float64(days)
		venueDiscount = (*basePrice - rate) * float64(days)
	}

	// Packages must belong to this hall, the same rule the booking applies -
	// a preview that quoted someone else's cheaper package would be a lie.
	packages := []map[string]any{}
	for _, id := range req.PackageIDs {
		var pname string
		var price float64
		if err := h.svc.Pool().QueryRow(r.Context(),
			`SELECT name, price FROM hall_packages
			  WHERE id = $1 AND facility_id = $2 AND is_deleted = FALSE`,
			id, facilityID).Scan(&pname, &price); err != nil {
			response.Error(w, http.StatusBadRequest,
				"A selected package does not belong to this hall", "INVALID_PACKAGE")
			return
		}
		packages = append(packages, map[string]any{"id": id, "name": pname, "price": price})
		hallPrice += price
	}

	// The coupon comes off after the venue discount and before fee and tax, in
	// the order the booking applies them. An unusable code fails the preview
	// with the same error the booking would give, rather than quietly pricing
	// without it.
	var couponCode *string
	couponDiscount := 0.0
	if code := strings.TrimSpace(req.CouponCode); code != "" {
		c, d, err := h.svc.PriceCoupon(r.Context(), code, facilityID, hallPrice)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		couponCode, couponDiscount = &c.Code, d
		hallPrice -= d
	}

	serviceFee := round2(hallPrice * serviceFeeRate)
	taxes := round2((hallPrice + serviceFee) * gstRate)
	total := round2(hallPrice + serviceFee + taxes)

	// Availability as it stands now. Every date in the range must be free.
	available := true
	if err := h.svc.Pool().QueryRow(r.Context(),
		`SELECT NOT EXISTS (
		   SELECT 1 FROM hall_availability
		    WHERE facility_id = $1 AND slot_type = $2
		      AND date BETWEEN $3 AND $4 AND status <> 'AVAILABLE')`,
		facilityID, slot, start, end).Scan(&available); err != nil {
		httpx.Fail(w, err)
		return
	}

	overCapacity := req.GuestCount != nil && capacityPax != nil && *req.GuestCount > *capacityPax

	policies := []map[string]any{}
	rows, err := h.svc.Pool().Query(r.Context(),
		`SELECT days_before_checkin, refund_percentage, policy_type FROM cancellation_policies
		  WHERE facility_id = $1 ORDER BY days_before_checkin DESC`, facilityID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d int
			var pct float64
			var ptype string
			if rows.Scan(&d, &pct, &ptype) == nil {
				policies = append(policies, map[string]any{
					"daysBeforeEvent": d, "refundPercentage": pct, "policyType": ptype})
			}
		}
	}

	response.OK(w, "Quote generated", map[string]any{
		"hallId": facilityID, "hallName": name, "city": city,
		"startDate": rawStart, "endDate": rawEnd,
		"startTime": startTime, "endTime": endTime,
		"slotType": slot, "days": days,
		"guestCount": req.GuestCount, "capacityPax": capacityPax,
		"exceedsCapacity": overCapacity,
		"packages":        packages,
		"priceBreakdown": map[string]any{
			"hallPrice":      round2(hallPrice),
			"venueDiscount":  round2(venueDiscount),
			"couponDiscount": round2(couponDiscount),
			"couponCode":     couponCode,
			"serviceFee":     serviceFee,
			"taxes":          taxes,
			"total":          total,
			"currency":       "INR",
		},
		"available":            available,
		"cancellationPolicies": policies,
	})
}

// round2 keeps money to paise; float arithmetic otherwise shows 4999.999999.
func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

func (h *Handler) registerQuote(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	mux.Handle("POST /api/v1/bookings/quote", auth(http.HandlerFunc(h.quote)))
}
