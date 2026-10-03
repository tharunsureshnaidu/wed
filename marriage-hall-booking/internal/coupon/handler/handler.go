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

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/coupon"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

type Handler struct {
	db     *pgxpool.Pool
	signer *jwt.Signer

	// OnCouponCreated announces a new coupon: to customers near the venue it
	// is scoped to, or - for an all-halls coupon - to every customer. Nil
	// disables the announcement entirely.
	OnCouponCreated func(ctx context.Context, c Created)
}

// Created is what the announcement needs to say what the offer is.
type Created struct {
	ID            string
	Code          string
	FacilityID    string // empty for an all-halls coupon
	DiscountType  string
	DiscountValue float64
	MaxDiscount   *float64
	CreatedBy     int64
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
	mux.Handle("PUT /api/v1/coupons/{id}", owner(func(w http.ResponseWriter, r *http.Request) { h.save(w, r, false) }))
	mux.Handle("DELETE /api/v1/coupons/{id}", owner(func(w http.ResponseWriter, r *http.Request) { h.remove(w, r, false) }))

	// Coupon cards for signed-in users on app screen (filtered by location <= 50 km or no location)
	mux.Handle("GET /api/v1/coupons/available", auth(http.HandlerFunc(h.listAvailable)))

	// Any signed-in customer can check a code before booking.
	mux.Handle("POST /api/v1/coupons/validate", auth(http.HandlerFunc(h.validateCode)))
	// Public: the offers a venue's page can show, for a visitor who is not
	// signed in. What it lists is exactly what checkout accepts - both use
	// coupon.AppliesSQL.
	//
	// Its own path rather than /available: that one is the signed-in card list
	// and filters by the caller's saved location, which a logged-out visitor
	// does not have. Registering both on one pattern panics ServeMux at start.
	mux.HandleFunc("GET /api/v1/coupons/offers", h.available)

	// Admin coupons for every marriage hall. Their own routes, with no
	// facilityId in the request at all, rather than "leave facilityId out on
	// POST /coupons" - which used to create a coupon valid on hotels too.
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, auth, middleware.RequireRole(domain.RoleAdmin))
	}
	mux.Handle("POST /api/v1/admin/coupons", admin(h.adminCreate))
	mux.Handle("GET /api/v1/admin/coupons", admin(h.adminList))
	mux.Handle("PUT /api/v1/admin/coupons/{id}", admin(func(w http.ResponseWriter, r *http.Request) { h.save(w, r, true) }))
	mux.Handle("DELETE /api/v1/admin/coupons/{id}", admin(func(w http.ResponseWriter, r *http.Request) { h.remove(w, r, true) }))
}

// hallType is the stored facility type an admin coupon applies to. Halls only
// for now; the column takes HOTEL too, so a hotel-wide coupon needs no
// migration - only a route.
const hallType = "MARRIAGE_HALL"

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
	// An admin's coupon here must name a venue. One without used to mean
	// "platform-wide" with no venue type, so it discounted hotels as well.
	if vendorID == nil && (req.FacilityID == nil || *req.FacilityID == "") {
		response.Error(w, http.StatusBadRequest,
			"facilityId is required. For a coupon on every marriage hall use POST /api/v1/admin/coupons",
			"VALIDATION_ERROR")
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
		h.OnCouponCreated(r.Context(), Created{
			ID: id, Code: strings.ToUpper(strings.TrimSpace(req.Code)), FacilityID: *req.FacilityID,
			DiscountType: strings.ToUpper(req.DiscountType), DiscountValue: req.DiscountValue,
			MaxDiscount: req.MaxDiscount, CreatedBy: userID,
		})
	}
	response.Created(w, "Coupon created successfully", "/api/v1/coupons/"+id,
		map[string]any{"id": id, "code": req.Code})
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
	h.listWhere(w, r, `($1::uuid IS NULL OR vendor_id = $1::uuid)`, vendorID)
}

func (h *Handler) adminList(w http.ResponseWriter, r *http.Request) {
	h.listWhere(w, r, `facility_type IS NOT NULL`)
}

func (h *Handler) listWhere(w http.ResponseWriter, r *http.Request, where string, args ...any) {
	rows, err := h.db.Query(r.Context(), `
		SELECT id::text, code, description, facility_id::text, facility_type, discount_type,
		       discount_value, max_discount, min_booking_amount,
		       valid_from, valid_until, usage_limit, used_count, is_active
		  FROM coupons
		 WHERE is_deleted = FALSE AND `+where+`
		 ORDER BY created_at DESC`, args...)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	type row struct {
		ID           string  `json:"id"`
		Code         string  `json:"code"`
		Description  *string `json:"description"`
		FacilityID   *string `json:"facilityId"`
		facilityType *string
		// AppliesTo is set on an all-venues-of-a-type coupon: "HALL".
		AppliesTo     *string    `json:"appliesTo"`
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
		if err := rows.Scan(&x.ID, &x.Code, &x.Description, &x.FacilityID, &x.facilityType,
			&x.DiscountType, &x.DiscountValue, &x.MaxDiscount, &x.MinBooking, &x.ValidFrom,
			&x.ValidUntil, &x.UsageLimit, &x.UsedCount, &x.IsActive); err != nil {
			httpx.Fail(w, err)
			return
		}
		if x.facilityType != nil {
			t := venuetype.API(*x.facilityType)
			x.AppliesTo = &t
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Coupons retrieved successfully", out)
}

// adminCreate creates a coupon valid at every marriage hall. There is no
// facilityId field: sending one is rejected rather than ignored, so a client
// that meant one venue finds out instead of discounting all of them.
func (h *Handler) adminCreate(w http.ResponseWriter, r *http.Request) {
	var req couponReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	from, until := req.validateInto(&e)
	if req.FacilityID != nil {
		e = append(e, "facilityId is not accepted here - this coupon applies to every marriage hall")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	userID, _ := middleware.UserID(r.Context())
	active := req.IsActive == nil || *req.IsActive
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	var id string
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO coupons (code, description, facility_type,
		    discount_type, discount_value, max_discount, min_booking_amount,
		    valid_from, valid_until, usage_limit, is_active, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id::text`,
		code, req.Description, hallType, strings.ToUpper(req.DiscountType), req.DiscountValue,
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

	// Told to every customer, but only when it can be used today: announcing a
	// coupon scheduled for next month sends people to a code that fails.
	now := time.Now()
	usableNow := active && (from == nil || !from.After(now)) && (until == nil || until.After(now))
	if h.OnCouponCreated != nil && usableNow {
		h.OnCouponCreated(r.Context(), Created{
			ID: id, Code: code, DiscountType: strings.ToUpper(req.DiscountType),
			DiscountValue: req.DiscountValue, MaxDiscount: req.MaxDiscount, CreatedBy: userID,
		})
	}
	response.Created(w, "Coupon created successfully", "/api/v1/admin/coupons/"+id,
		map[string]any{"id": id, "code": code, "appliesTo": venuetype.API(hallType), "announced": h.OnCouponCreated != nil && usableNow})
}

// available lists the coupons a customer can use at a venue - or, without a
// facilityId, the ones valid at every marriage hall. Exhausted and expired
// coupons are filtered in SQL, so the list never offers a code checkout rejects.
func (h *Handler) available(w http.ResponseWriter, r *http.Request) {
	facilityID := r.URL.Query().Get("facilityId")
	// Without a venue: coupons for every hall. With one: whatever applies
	// there, by the same rule checkout uses.
	join, where, args := ``, `c.facility_type = $1`, []any{hallType}
	if facilityID != "" {
		if !httpx.ValidUUID(facilityID) {
			response.Error(w, http.StatusBadRequest, "facilityId must be a valid id", "VALIDATION_ERROR")
			return
		}
		var exists bool
		if err := h.db.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM facilities WHERE id = $1 AND is_deleted = FALSE)`,
			facilityID).Scan(&exists); err != nil {
			httpx.Fail(w, err)
			return
		}
		if !exists {
			response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
			return
		}
		join = `JOIN facilities f ON f.id = $1 AND f.is_deleted = FALSE`
		where, args = coupon.AppliesSQL, []any{facilityID}
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT c.id::text, c.code, c.description, c.discount_type, c.discount_value,
		       c.max_discount, c.min_booking_amount, c.valid_until, c.facility_type
		  FROM coupons c `+join+`
		 WHERE `+coupon.LiveSQL+`
		   AND (c.usage_limit IS NULL OR c.used_count < c.usage_limit)
		   AND `+where+`
		 ORDER BY c.facility_type IS NULL, c.created_at DESC`, args...)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	type offer struct {
		ID            string     `json:"id"`
		Code          string     `json:"code"`
		Description   *string    `json:"description"`
		DiscountType  string     `json:"discountType"`
		DiscountValue float64    `json:"discountValue"`
		MaxDiscount   *float64   `json:"maxDiscount"`
		MinBooking    *float64   `json:"minBookingAmount"`
		ValidUntil    *time.Time `json:"validUntil"`
		// AppliesTo is "HALL" for a coupon valid at every marriage hall, null
		// for one scoped to this venue or its vendor.
		AppliesTo    *string `json:"appliesTo"`
		facilityType *string
	}
	out := []offer{}
	for rows.Next() {
		var o offer
		if err := rows.Scan(&o.ID, &o.Code, &o.Description, &o.DiscountType, &o.DiscountValue,
			&o.MaxDiscount, &o.MinBooking, &o.ValidUntil, &o.facilityType); err != nil {
			httpx.Fail(w, err)
			return
		}
		if o.facilityType != nil {
			t := venuetype.API(*o.facilityType)
			o.AppliesTo = &t
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Coupons retrieved successfully", out)
}

// save updates a coupon. platform is the admin route: only all-halls coupons,
// and never a facilityId. Otherwise an admin may edit any coupon and a vendor
// only their own.
func (h *Handler) save(w http.ResponseWriter, r *http.Request, platform bool) {
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
	if platform && req.FacilityID != nil {
		e = append(e, "facilityId is not accepted here - this coupon applies to every marriage hall")
	}
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
	active := req.IsActive == nil || *req.IsActive
	// Ownership is in the WHERE clause, so another vendor's coupon simply
	// matches nothing and is reported as not found.
	tag, err := h.db.Exec(r.Context(), `
		UPDATE coupons SET code = $2, description = $3, discount_type = $4,
		    discount_value = $5, max_discount = $6, min_booking_amount = $7,
		    valid_from = $8, valid_until = $9, usage_limit = $10, is_active = $11,
		    updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE
		   AND ($12::uuid IS NULL OR vendor_id = $12::uuid)
		   AND (NOT $13 OR facility_type IS NOT NULL)`,
		id, strings.ToUpper(strings.TrimSpace(req.Code)), req.Description,
		strings.ToUpper(req.DiscountType), req.DiscountValue, req.MaxDiscount,
		req.MinBookingAmount, from, until, req.UsageLimit, active, vendorID, platform)
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
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Coupon not found", "COUPON_NOT_FOUND")
		return
	}
	response.OK(w, "Coupon updated successfully", nil)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request, platform bool) {
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
		   AND ($2::uuid IS NULL OR vendor_id = $2::uuid)
		   AND (NOT $3 OR facility_type IS NOT NULL)`, id, vendorID, platform)
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

// validateCode prices a coupon against an amount at one venue.
//
// amount is what the coupon comes off: the price after the venue's own
// advertised discount, as the booking applies it. The discount is always
// computed here from the stored row, never taken from the client.
//
// facilityId is required. Without it, a coupon scoped to one venue - or to one
// vendor's venues - validated for any venue on the platform, because the scope
// check was skipped whenever the client left the id out.
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
	if req.FacilityID == nil || !httpx.ValidUUID(*req.FacilityID) {
		e = append(e, "facilityId is required")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	c, err := coupon.Load(r.Context(), h.db, req.Code, *req.FacilityID)
	if err == nil {
		err = c.Check(req.Amount)
	}
	var ce *coupon.Error
	if errors.As(err, &ce) {
		response.Error(w, ce.Status, ce.Message, ce.Code)
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	discount := c.Discount(req.Amount)
	response.OK(w, "Coupon applied", map[string]any{
		"couponId": c.ID, "code": c.Code,
		"discountAmount": discount, "finalAmount": req.Amount - discount,
	})
}

type availableCouponItem struct {
	ID               string     `json:"id"`
	Code             string     `json:"code"`
	Description      *string    `json:"description"`
	FacilityID       *string    `json:"facilityId,omitempty"`
	FacilityName     *string    `json:"facilityName,omitempty"`
	DiscountType     string     `json:"discountType"`
	DiscountValue    float64    `json:"discountValue"`
	MaxDiscount      *float64   `json:"maxDiscount,omitempty"`
	MinBookingAmount *float64   `json:"minBookingAmount,omitempty"`
	ValidFrom        *time.Time `json:"validFrom,omitempty"`
	ValidUntil       *time.Time `json:"validUntil,omitempty"`
	UsageLimit       *int       `json:"usageLimit,omitempty"`
	UsedCount        int        `json:"usedCount"`
	IsActive         bool       `json:"isActive"`
	DistanceKm       *float64   `json:"distanceKm,omitempty"`
}

const defaultGeoRadiusMetres = 50000.0 // 50 km

// listAvailable returns coupon cards for a user's app screen.
//
// Selection rule:
// 1. User has location AND Facility has location: included ONLY if distance <= 50 km.
// 2. User has NO location (lat/lng is null) OR Facility has NO location: ALWAYS INCLUDED.
// 3. Platform-wide coupons (facilityId is null): ALWAYS INCLUDED.
func (h *Handler) listAvailable(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT c.id::text, c.code, c.description, c.facility_id::text, COALESCE(f.name, ''),
		       c.discount_type, c.discount_value, c.max_discount, c.min_booking_amount,
		       c.valid_from, c.valid_until, c.usage_limit, c.used_count, c.is_active,
		       CASE
		         WHEN up.lat IS NOT NULL AND up.lng IS NOT NULL AND f.lat IS NOT NULL AND f.lng IS NOT NULL THEN
		           earth_distance(ll_to_earth(f.lat::double precision, f.lng::double precision), ll_to_earth(up.lat, up.lng)) / 1000.0
		         ELSE NULL
		       END AS distance_km
		  FROM coupons c
		  LEFT JOIN user_profiles up ON up.id = $1
		  LEFT JOIN facilities f ON f.id = c.facility_id AND f.is_deleted = FALSE
		 WHERE c.is_active = TRUE
		   AND c.is_deleted = FALSE
		   AND (c.valid_from IS NULL OR c.valid_from <= CURRENT_TIMESTAMP)
		   AND (c.valid_until IS NULL OR c.valid_until >= CURRENT_TIMESTAMP)
		   AND (c.usage_limit IS NULL OR c.used_count < c.usage_limit)
		   AND (
		     c.facility_id IS NULL
		     OR up.lat IS NULL OR up.lng IS NULL
		     OR f.lat IS NULL OR f.lng IS NULL
		     OR earth_distance(ll_to_earth(f.lat::double precision, f.lng::double precision), ll_to_earth(up.lat, up.lng)) <= $2
		   )
		 ORDER BY c.created_at DESC`, userID, defaultGeoRadiusMetres)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	out := []availableCouponItem{}
	for rows.Next() {
		var x availableCouponItem
		if err := rows.Scan(&x.ID, &x.Code, &x.Description, &x.FacilityID, &x.FacilityName,
			&x.DiscountType, &x.DiscountValue, &x.MaxDiscount, &x.MinBookingAmount,
			&x.ValidFrom, &x.ValidUntil, &x.UsageLimit, &x.UsedCount, &x.IsActive,
			&x.DistanceKm); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Available coupons retrieved successfully", out)
}

func nullUUID(s *string) any {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	return *s
}
