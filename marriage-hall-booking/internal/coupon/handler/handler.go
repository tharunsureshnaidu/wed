// Package handler serves coupon CRUD for vendors and admins.
//
// No service layer: a coupon is a flat table with validation, which is the
// shape internal/vendors and internal/quote already use.
package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/jwt"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
	"github.com/tripfcatory/marriage-hall-booking/pkg/validate"
)

type Handler struct {
	db     *pgxpool.Pool
	signer *jwt.Signer

	// OnCouponCreated announces a new coupon to customers near the venue it
	// applies to. Nil disables the announcement entirely.
	OnCouponCreated func(ctx context.Context, couponID, code, facilityID string, createdBy int64)
}

func New(db *pgxpool.Pool, signer *jwt.Signer) *Handler {
	return &Handler{db: db, signer: signer}
}

func (h *Handler) Register(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	// "Vendor" is ROLE_HALL_OWNER - there is no ROLE_VENDOR in this codebase.
	owner := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, auth,
			middleware.RequireRole(domain.RoleHallOwner, domain.RoleAdmin))
	}

	mux.Handle("POST /api/v1/coupons", owner(h.create))
	mux.Handle("GET /api/v1/coupons", owner(h.list))
	mux.Handle("PUT /api/v1/coupons/{id}", owner(h.update))
	mux.Handle("DELETE /api/v1/coupons/{id}", owner(h.delete))

	// Any signed-in customer can check a code before booking.
	mux.Handle("POST /api/v1/coupons/validate", auth(http.HandlerFunc(h.validateCode)))
}

type couponReq struct {
	Code             string   `json:"code"`
	Description      *string  `json:"description"`
	FacilityID       *string  `json:"facilityId"`
	DiscountType     string   `json:"discountType"`
	DiscountValue    float64  `json:"discountValue"`
	MaxDiscount      *float64 `json:"maxDiscount"`
	MinBookingAmount *float64 `json:"minBookingAmount"`
	ValidFrom        *string  `json:"validFrom"`
	ValidUntil       *string  `json:"validUntil"`
	UsageLimit       *int     `json:"usageLimit"`
	IsActive         *bool    `json:"isActive"`
}

func (r couponReq) validateInto(e *validate.Errors) (from, until *time.Time) {
	e.Required("Code", r.Code)
	switch strings.ToUpper(r.DiscountType) {
	case "PERCENT":
		if r.DiscountValue <= 0 || r.DiscountValue > 100 {
			*e = append(*e, "A percent discount must be between 0 and 100")
		}
	case "FLAT":
		if r.DiscountValue <= 0 {
			*e = append(*e, "A flat discount must be greater than 0")
		}
	default:
		*e = append(*e, "discountType must be PERCENT or FLAT")
	}
	if r.MaxDiscount != nil && *r.MaxDiscount <= 0 {
		*e = append(*e, "maxDiscount must be greater than 0")
	}
	if r.FacilityID != nil && *r.FacilityID != "" && !httpx.ValidUUID(*r.FacilityID) {
		*e = append(*e, "facilityId must be a valid id")
	}
	for _, f := range []struct {
		raw  *string
		name string
		out  **time.Time
	}{{r.ValidFrom, "validFrom", &from}, {r.ValidUntil, "validUntil", &until}} {
		if f.raw == nil || *f.raw == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, *f.raw)
		if err != nil {
			// Accept a bare date too: an admin setting a campaign window
			// should not have to write a timezone offset.
			t, err = time.Parse("2006-01-02", *f.raw)
		}
		if err != nil {
			*e = append(*e, f.name+" must be YYYY-MM-DD or RFC3339")
			continue
		}
		*f.out = &t
	}
	if from != nil && until != nil && !until.After(*from) {
		*e = append(*e, "validUntil must be after validFrom")
	}
	return from, until
}

// vendorOf returns the caller's vendor id, or nil for an admin (whose coupons
// are platform-wide). A hall owner with no vendor record cannot create one.
func (h *Handler) vendorOf(r *http.Request) (*string, bool, error) {
	if middleware.HasRole(r.Context(), domain.RoleAdmin) {
		return nil, true, nil
	}
	userID, _ := middleware.UserID(r.Context())
	var id string
	err := h.db.QueryRow(r.Context(),
		`SELECT id::text FROM vendors WHERE user_id = $1 AND is_deleted = FALSE`,
		userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &id, true, nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req couponReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	from, until := req.validateInto(&e)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	vendorID, ok, err := h.vendorOf(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !ok {
		response.Error(w, http.StatusForbidden,
			"Create your vendor business first (PUT /api/v1/vendors/me)", "VENDOR_REQUIRED")
		return
	}
	// A vendor may only scope a coupon to a venue they own; an admin may scope
	// it anywhere. Checked before the insert so the coupon is never created.
	if req.FacilityID != nil && *req.FacilityID != "" && vendorID != nil {
		var owns bool
		if err := h.db.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM facilities f JOIN vendors v ON v.id = $2
			   WHERE f.id = $1 AND f.owner_id = v.user_id AND f.is_deleted = FALSE)`,
			*req.FacilityID, *vendorID).Scan(&owns); err != nil {
			httpx.Fail(w, err)
			return
		}
		if !owns {
			response.Error(w, http.StatusForbidden,
				"You do not own that facility", "NOT_FACILITY_OWNER")
			return
		}
	}

	userID, _ := middleware.UserID(r.Context())
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	var id string
	err = h.db.QueryRow(r.Context(), `
		INSERT INTO coupons (code, description, vendor_id, facility_id,
		    discount_type, discount_value, max_discount, min_booking_amount,
		    valid_from, valid_until, usage_limit, is_active, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id::text`,
		strings.ToUpper(strings.TrimSpace(req.Code)), req.Description, vendorID,
		nullUUID(req.FacilityID), strings.ToUpper(req.DiscountType), req.DiscountValue,
		req.MaxDiscount, req.MinBookingAmount, from, until, req.UsageLimit,
		active, userID).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			response.Error(w, http.StatusConflict,
				"That coupon code already exists", "COUPON_EXISTS")
			return
		}
		httpx.Fail(w, err)
		return
	}

	// Announced only when scoped to a venue: a radius is measured from a point,
	// and a platform-wide coupon has none. Announcing it to everyone near some
	// arbitrary venue would be worse than not announcing it.
	if h.OnCouponCreated != nil && active && req.FacilityID != nil && *req.FacilityID != "" {
		h.OnCouponCreated(r.Context(), id, req.Code, *req.FacilityID, userID)
	}
	response.OK(w, "Coupon created successfully", map[string]any{"id": id, "code": req.Code})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	vendorID, ok, err := h.vendorOf(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !ok {
		response.OK(w, "Coupons retrieved successfully", []any{})
		return
	}
	// An admin sees every coupon; a vendor sees only their own.
	rows, err := h.db.Query(r.Context(), `
		SELECT id::text, code, description, facility_id::text, discount_type,
		       discount_value, max_discount, min_booking_amount,
		       valid_from, valid_until, usage_limit, used_count, is_active
		  FROM coupons
		 WHERE is_deleted = FALSE AND ($1::uuid IS NULL OR vendor_id = $1::uuid)
		 ORDER BY created_at DESC`, vendorID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	type row struct {
		ID            string     `json:"id"`
		Code          string     `json:"code"`
		Description   *string    `json:"description"`
		FacilityID    *string    `json:"facilityId"`
		DiscountType  string     `json:"discountType"`
		DiscountValue float64    `json:"discountValue"`
		MaxDiscount   *float64   `json:"maxDiscount"`
		MinBooking    *float64   `json:"minBookingAmount"`
		ValidFrom     *time.Time `json:"validFrom"`
		ValidUntil    *time.Time `json:"validUntil"`
		UsageLimit    *int       `json:"usageLimit"`
		UsedCount     int        `json:"usedCount"`
		IsActive      bool       `json:"isActive"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.Code, &x.Description, &x.FacilityID, &x.DiscountType,
			&x.DiscountValue, &x.MaxDiscount, &x.MinBooking, &x.ValidFrom, &x.ValidUntil,
			&x.UsageLimit, &x.UsedCount, &x.IsActive); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	response.OK(w, "Coupons retrieved successfully", out)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid coupon id", "VALIDATION_ERROR")
		return
	}
	var req couponReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	from, until := req.validateInto(&e)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	vendorID, ok, err := h.vendorOf(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !ok {
		response.Error(w, http.StatusForbidden, "Not your coupon", "NOT_COUPON_OWNER")
		return
	}
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	// Ownership is in the WHERE clause, so another vendor's coupon simply
	// matches nothing and is reported as not found.
	tag, err := h.db.Exec(r.Context(), `
		UPDATE coupons SET code = $2, description = $3, discount_type = $4,
		    discount_value = $5, max_discount = $6, min_booking_amount = $7,
		    valid_from = $8, valid_until = $9, usage_limit = $10, is_active = $11,
		    updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE
		   AND ($12::uuid IS NULL OR vendor_id = $12::uuid)`,
		id, strings.ToUpper(strings.TrimSpace(req.Code)), req.Description,
		strings.ToUpper(req.DiscountType), req.DiscountValue, req.MaxDiscount,
		req.MinBookingAmount, from, until, req.UsageLimit, active, vendorID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Coupon not found", "COUPON_NOT_FOUND")
		return
	}
	response.OK(w, "Coupon updated successfully", nil)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid coupon id", "VALIDATION_ERROR")
		return
	}
	vendorID, ok, err := h.vendorOf(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !ok {
		response.Error(w, http.StatusForbidden, "Not your coupon", "NOT_COUPON_OWNER")
		return
	}
	// Soft delete: a coupon already applied to bookings is part of their
	// pricing history and must stay resolvable.
	tag, err := h.db.Exec(r.Context(), `
		UPDATE coupons SET is_deleted = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE
		   AND ($2::uuid IS NULL OR vendor_id = $2::uuid)`, id, vendorID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Coupon not found", "COUPON_NOT_FOUND")
		return
	}
	response.OK(w, "Coupon deleted successfully", nil)
}

type validateReq struct {
	Code       string  `json:"code"`
	Amount     float64 `json:"amount"`
	FacilityID *string `json:"facilityId"`
}

// validateCode prices a coupon against a booking amount.
//
// The discount is always computed here from the stored row, never taken from
// the client - the same rule the booking service states about prices.
func (h *Handler) validateCode(w http.ResponseWriter, r *http.Request) {
	var req validateReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Code", req.Code)
	if req.Amount <= 0 {
		e = append(e, "amount must be greater than 0")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	var (
		id, dtype                string
		dvalue                   float64
		maxDisc, minBooking      *float64
		facilityID               *string
		usageLimit               *int
		usedCount                int
	)
	err := h.db.QueryRow(r.Context(), `
		SELECT id::text, discount_type, discount_value, max_discount,
		       min_booking_amount, facility_id::text, usage_limit, used_count
		  FROM coupons
		 WHERE upper(code) = upper($1) AND is_deleted = FALSE AND is_active
		   AND (valid_from IS NULL OR valid_from <= CURRENT_TIMESTAMP)
		   AND (valid_until IS NULL OR valid_until >= CURRENT_TIMESTAMP)`,
		strings.TrimSpace(req.Code)).Scan(&id, &dtype, &dvalue, &maxDisc,
		&minBooking, &facilityID, &usageLimit, &usedCount)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound,
			"That coupon is not valid or has expired", "COUPON_INVALID")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if usageLimit != nil && usedCount >= *usageLimit {
		response.Error(w, http.StatusConflict,
			"This coupon has been fully redeemed", "COUPON_EXHAUSTED")
		return
	}
	if minBooking != nil && req.Amount < *minBooking {
		response.Error(w, http.StatusBadRequest,
			"This coupon needs a minimum booking amount", "COUPON_MIN_AMOUNT")
		return
	}
	if facilityID != nil && req.FacilityID != nil && *facilityID != *req.FacilityID {
		response.Error(w, http.StatusBadRequest,
			"This coupon does not apply to that venue", "COUPON_WRONG_FACILITY")
		return
	}

	discount := dvalue
	if dtype == "PERCENT" {
		discount = req.Amount * dvalue / 100
		if maxDisc != nil && discount > *maxDisc {
			discount = *maxDisc
		}
	}
	// Never discount below zero: a flat coupon larger than the booking would
	// otherwise produce a negative total.
	if discount > req.Amount {
		discount = req.Amount
	}
	response.OK(w, "Coupon applied", map[string]any{
		"couponId": id, "code": strings.ToUpper(req.Code),
		"discountAmount": discount, "finalAmount": req.Amount - discount,
	})
}

func nullUUID(s *string) any {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	return *s
}
