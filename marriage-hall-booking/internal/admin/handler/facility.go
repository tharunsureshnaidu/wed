package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
	"github.com/tripfcatory/marriage-hall-booking/pkg/validate"
)

// Admin facility editing.
//
// The vendor-side routes under /api/v1/facilities already accept an admin -
// requireOwner lets ROLE_ADMIN through - so pricing, packages, add-ons,
// amenities, images and room types are editable by an admin today and are not
// duplicated here. What was missing is on this file:
//
//   - PATCH: change one field without resending the whole record. The existing
//     PUT is a full replace, so editing just a phone number meant resending
//     every column and silently nulling anything omitted.
//   - rating override, for venues whose rating came from somewhere else.
//   - a single GET that returns the facility with its children, so an admin
//     screen does not need eight calls to render one venue.
func (h *Handler) RegisterFacilityAdmin(mux *http.ServeMux) {
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequireAuth(h.signer),
			middleware.RequireRole(domain.RoleAdmin))
	}

	mux.Handle("GET /api/v1/admin/facilities/{id}", admin(h.facilityDetail))
	mux.Handle("PATCH /api/v1/admin/facilities/{id}", admin(h.patchFacility))
	mux.Handle("PUT /api/v1/admin/facilities/{id}/rating", admin(h.setRating))
	mux.Handle("DELETE /api/v1/admin/facilities/{id}/rating", admin(h.clearRating))
}

// patchReq is every admin-editable column. Pointers throughout: a nil field is
// "leave it alone", which is what makes this a patch rather than a replace.
// Without that distinction there is no way to tell "clear the description"
// from "I did not mention the description".
type patchReq struct {
	Name         *string  `json:"name"`
	Description  *string  `json:"description"`
	City         *string  `json:"city"`
	FullAddress  *string  `json:"fullAddress"`
	State        *string  `json:"state"`
	Zipcode      *string  `json:"zipcode"`
	Country      *string  `json:"country"`
	Lat          *float64 `json:"lat"`
	Lng          *float64 `json:"lng"`
	ContactPhone *string  `json:"contactPhone"`
	ContactEmail *string  `json:"contactEmail"`
	Website      *string  `json:"website"`

	// The advertised discount shown on the listing card. clearDiscount:true
	// removes it - an explicit flag rather than a null, because a nil pointer
	// already means "not mentioned" for every other field here and one field
	// behaving differently is how mistakes happen.
	DiscountPercent    *float64 `json:"discountPercent"`
	DiscountLabel      *string  `json:"discountLabel"`
	DiscountValidUntil *string  `json:"discountValidUntil"`
	ClearDiscount      bool     `json:"clearDiscount"`

	StarRating   *int    `json:"starRating"`
	CheckInTime  *string `json:"checkInTime"`
	CheckOutTime *string `json:"checkOutTime"`

	CapacityPax      *int     `json:"capacityPax"`
	AreaSqft         *int     `json:"areaSqft"`
	BasePricePerDay  *float64 `json:"basePricePerDay"`
	SeatingCapacity  *int     `json:"seatingCapacity"`
	FloatingCapacity *int     `json:"floatingCapacity"`
	MinBookingSize   *int     `json:"minBookingSize"`

	Status     *string `json:"status"`
	IsVerified *bool   `json:"isVerified"`
	IsFeatured *bool   `json:"isFeatured"`
	OwnerID    *int64  `json:"ownerId"`
}

func (h *Handler) patchFacility(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	var req patchReq
	if !httpx.Decode(w, r, &req) {
		return
	}

	var e validate.Errors
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		e = append(e, "Name cannot be empty")
	}
	if req.StarRating != nil && (*req.StarRating < 1 || *req.StarRating > 5) {
		e = append(e, "Star rating must be between 1 and 5")
	}
	if req.BasePricePerDay != nil && *req.BasePricePerDay < 0 {
		e = append(e, "Base price cannot be negative")
	}
	if req.ContactEmail != nil && *req.ContactEmail != "" {
		e.Email("Contact email", req.ContactEmail)
	}
	if req.ContactPhone != nil && *req.ContactPhone != "" {
		e.Phone("Contact phone", req.ContactPhone)
	}
	if req.Status != nil {
		switch *req.Status {
		case "APPROVED", "REJECTED", "BLOCKED", "PENDING":
		default:
			e = append(e, "Status must be APPROVED, REJECTED, BLOCKED or PENDING")
		}
	}
	if req.DiscountPercent != nil && (*req.DiscountPercent <= 0 || *req.DiscountPercent > 100) {
		e = append(e, "discountPercent must be between 0 and 100")
	}
	var discountUntil *time.Time
	if req.DiscountValidUntil != nil && *req.DiscountValidUntil != "" {
		t, perr := time.Parse(time.RFC3339, *req.DiscountValidUntil)
		if perr != nil {
			t, perr = time.Parse("2006-01-02", *req.DiscountValidUntil)
		}
		if perr != nil {
			e = append(e, "discountValidUntil must be YYYY-MM-DD or RFC3339")
		} else {
			discountUntil = &t
		}
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	// Built column by column so an omitted field is never written. The
	// alternative - COALESCE($n, column) - cannot express "set this to NULL",
	// which an admin clearing a stale phone number needs.
	set := []string{}
	args := []any{id}
	add := func(col string, val any) {
		args = append(args, val)
		set = append(set, col+" = $"+itoa(len(args)))
	}
	if req.Name != nil {
		add("name", strings.TrimSpace(*req.Name))
	}
	if req.Description != nil {
		add("description", *req.Description)
	}
	if req.City != nil {
		add("city", *req.City)
	}
	if req.FullAddress != nil {
		add("full_address", *req.FullAddress)
	}
	if req.State != nil {
		add("state", *req.State)
	}
	if req.Zipcode != nil {
		add("zipcode", *req.Zipcode)
	}
	if req.Country != nil {
		add("country", *req.Country)
	}
	if req.Lat != nil {
		add("lat", *req.Lat)
	}
	if req.Lng != nil {
		add("lng", *req.Lng)
	}
	if req.ContactPhone != nil {
		add("contact_phone", nullIfEmpty(*req.ContactPhone))
	}
	if req.ContactEmail != nil {
		add("contact_email", nullIfEmpty(*req.ContactEmail))
	}
	if req.Website != nil {
		add("website", nullIfEmpty(*req.Website))
	}
	if req.ClearDiscount {
		// Clears the whole offer, not just the number: a label and an expiry
		// with no percentage would render as an offer with no discount.
		add("discount_percent", nil)
		add("discount_label", nil)
		add("discount_valid_until", nil)
	} else {
		if req.DiscountPercent != nil {
			add("discount_percent", *req.DiscountPercent)
		}
		if req.DiscountLabel != nil {
			add("discount_label", nullIfEmpty(*req.DiscountLabel))
		}
		if req.DiscountValidUntil != nil {
			add("discount_valid_until", discountUntil)
		}
	}
	if req.StarRating != nil {
		add("star_rating", *req.StarRating)
	}
	if req.CheckInTime != nil {
		add("check_in_time", *req.CheckInTime)
	}
	if req.CheckOutTime != nil {
		add("check_out_time", *req.CheckOutTime)
	}
	if req.CapacityPax != nil {
		add("capacity_pax", *req.CapacityPax)
	}
	if req.AreaSqft != nil {
		add("area_sqft", *req.AreaSqft)
	}
	if req.BasePricePerDay != nil {
		add("base_price_per_day", *req.BasePricePerDay)
	}
	if req.SeatingCapacity != nil {
		add("seating_capacity", *req.SeatingCapacity)
	}
	if req.FloatingCapacity != nil {
		add("floating_capacity", *req.FloatingCapacity)
	}
	if req.MinBookingSize != nil {
		add("min_booking_size", *req.MinBookingSize)
	}
	if req.Status != nil {
		add("status", *req.Status)
	}
	if req.IsVerified != nil {
		add("is_verified", *req.IsVerified)
	}
	if req.IsFeatured != nil {
		add("is_featured", *req.IsFeatured)
	}
	if req.OwnerID != nil {
		add("owner_id", *req.OwnerID)
	}

	if len(set) == 0 {
		response.Error(w, http.StatusBadRequest, "No fields to update", "VALIDATION_ERROR")
		return
	}
	set = append(set, "updated_at = CURRENT_TIMESTAMP")

	var ownerID int64
	var name, status string
	err := h.db.QueryRow(r.Context(),
		`UPDATE facilities SET `+strings.Join(set, ", ")+
			` WHERE id = $1 AND is_deleted = FALSE
		  RETURNING owner_id, name, status`, args...).Scan(&ownerID, &name, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// Only a status change is worth telling the owner about; renaming a field
	// on their behalf is not news.
	if req.Status != nil {
		h.notifyStatus(r.Context(), StatusChange{
			Entity: "facility", EntityID: id, UserID: ownerID,
			Status: status, Name: name,
		})
	}
	response.OK(w, "Facility updated successfully", map[string]any{
		"id": id, "name": name, "status": status, "fieldsUpdated": len(set) - 1,
	})
}

type ratingReq struct {
	AvgRating   *float64 `json:"avgRating"`
	ReviewCount *int     `json:"reviewCount"`
}

// setRating pins a facility's rating.
//
// Normally avg_rating/review_count are derived from the reviews table. An
// imported venue has a rating earned on another platform and no review rows
// here, so the derived value would be zero. Pinning sets rating_is_manual,
// which makes recalcRating skip this facility - otherwise the first real
// review would replace "4.3 from 218" with "5.0 from 1".
func (h *Handler) setRating(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	var req ratingReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	if req.AvgRating == nil {
		e = append(e, "avgRating is required")
	} else if *req.AvgRating < 0 || *req.AvgRating > 5 {
		e = append(e, "avgRating must be between 0 and 5")
	}
	if req.ReviewCount != nil && *req.ReviewCount < 0 {
		e = append(e, "reviewCount cannot be negative")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	count := 0
	if req.ReviewCount != nil {
		count = *req.ReviewCount
	}
	tag, err := h.db.Exec(r.Context(),
		`UPDATE facilities SET avg_rating = $2, review_count = $3,
		    rating_is_manual = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE`, id, *req.AvgRating, count)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
		return
	}
	response.OK(w, "Rating set manually - it will no longer be recalculated from reviews",
		map[string]any{"avgRating": *req.AvgRating, "reviewCount": count})
}

// clearRating hands the rating back to the reviews table and recomputes it now,
// so the response reflects the real value rather than the stale pinned one.
func (h *Handler) clearRating(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	var avg float64
	var count int
	err := h.db.QueryRow(r.Context(),
		`UPDATE facilities f SET
		    rating_is_manual = FALSE,
		    avg_rating = COALESCE((SELECT round(avg(rating)::numeric, 2) FROM reviews
		        WHERE facility_id = f.id AND is_deleted = FALSE), 0),
		    review_count = (SELECT count(*) FROM reviews
		        WHERE facility_id = f.id AND is_deleted = FALSE),
		    updated_at = CURRENT_TIMESTAMP
		 WHERE f.id = $1 AND f.is_deleted = FALSE
		 RETURNING avg_rating, review_count`, id).Scan(&avg, &count)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Rating is now calculated from reviews",
		map[string]any{"avgRating": avg, "reviewCount": count})
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

// facilityDetail returns one facility with its child rows in a single call.
//
// An admin editing a venue needs the pricing rules, packages, add-ons,
// amenities, images and reviews together; assembling that from the public
// endpoints is eight round trips and none of them show a PENDING listing.
func (h *Handler) facilityDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}

	var f struct {
		ID           string   `json:"id"`
		OwnerID      int64    `json:"ownerId"`
		OwnerName    string   `json:"ownerName"`
		OwnerEmail   *string  `json:"ownerEmail"`
		Name         string   `json:"name"`
		Description  *string  `json:"description"`
		Type         string   `json:"type"`
		Status       string   `json:"status"`
		City         *string  `json:"city"`
		FullAddress  *string  `json:"fullAddress"`
		State        *string  `json:"state"`
		Zipcode      *string  `json:"zipcode"`
		Country      *string  `json:"country"`
		ContactPhone *string  `json:"contactPhone"`
		ContactEmail *string  `json:"contactEmail"`
		Website      *string  `json:"website"`
		StarRating   *int     `json:"starRating"`
		BasePrice    *float64 `json:"basePricePerDay"`
		CapacityPax  *int     `json:"capacityPax"`
		SeatingCap   *int     `json:"seatingCapacity"`
		AvgRating    float64  `json:"avgRating"`
		ReviewCount  int      `json:"reviewCount"`
		RatingManual bool     `json:"ratingIsManual"`
		IsVerified   bool     `json:"isVerified"`
		IsFeatured   bool     `json:"isFeatured"`
	}
	err := h.db.QueryRow(r.Context(),
		`SELECT f.id, f.owner_id, u.full_name, u.email, f.name, f.description, f.type, f.status,
		        f.city, f.full_address, f.state, f.zipcode, f.country,
		        f.contact_phone, f.contact_email, f.website,
		        f.star_rating, f.base_price_per_day, f.capacity_pax, f.seating_capacity,
		        COALESCE(f.avg_rating,0), COALESCE(f.review_count,0), f.rating_is_manual,
		        COALESCE(f.is_verified,false), COALESCE(f.is_featured,false)
		   FROM facilities f JOIN users u ON u.id = f.owner_id
		  WHERE f.id = $1 AND f.is_deleted = FALSE`, id).Scan(
		&f.ID, &f.OwnerID, &f.OwnerName, &f.OwnerEmail, &f.Name, &f.Description, &f.Type, &f.Status,
		&f.City, &f.FullAddress, &f.State, &f.Zipcode, &f.Country,
		&f.ContactPhone, &f.ContactEmail, &f.Website,
		&f.StarRating, &f.BasePrice, &f.CapacityPax, &f.SeatingCap,
		&f.AvgRating, &f.ReviewCount, &f.RatingManual, &f.IsVerified, &f.IsFeatured)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	out := map[string]any{"facility": f}
	// Each child list is best-effort: a malformed pricing row must not stop an
	// admin seeing the venue it belongs to, which is usually why they opened
	// the screen.
	out["pricing"] = h.children(r, `SELECT id, price, event_type, day_type, season,
	            min_guests, max_guests, valid_from, valid_to
	     FROM facility_pricing_rules WHERE facility_id = $1
	     ORDER BY valid_from NULLS FIRST`, id, "pricing")
	out["packages"] = h.children(r, `SELECT id, name, price, description, guest_capacity,
	            includes_catering, included_services, excluded_services
	     FROM hall_packages WHERE facility_id = $1 AND is_deleted = FALSE ORDER BY price`, id, "packages")
	out["amenities"] = h.children(r, `SELECT a.id, a.name, a.code
	     FROM facility_amenities fa JOIN amenities a ON a.id = fa.amenity_id
	     WHERE fa.facility_id = $1 ORDER BY a.name`, id, "amenities")
	out["images"] = h.children(r, `SELECT id, url, is_cover, status
	     FROM facility_images WHERE facility_id = $1
	     ORDER BY sort_order`, id, "images")
	out["reviews"] = h.children(r, `SELECT r.id, r.rating, r.title, r.comment, u.full_name
	     FROM reviews r JOIN users u ON u.id = r.user_id
	     WHERE r.facility_id = $1 AND r.is_deleted = FALSE
	     ORDER BY r.created_at DESC LIMIT 50`, id, "reviews")

	response.OK(w, "Facility retrieved successfully", out)
}

// children runs one child query and returns its rows as generic maps. Generic
// because these six tables share no shape and an admin screen only displays
// them - typing each one would be six structs for no gain.
func (h *Handler) children(r *http.Request, sql, id, label string) []map[string]any {
	rows, err := h.db.Query(r.Context(), sql, id)
	if err != nil {
		logger.Warn("admin facility detail: child query failed",
			"facilityId", id, "child", label, logger.Err(err))
		return []map[string]any{}
	}
	defer rows.Close()

	cols := rows.FieldDescriptions()
	out := []map[string]any{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			logger.Warn("admin facility detail: scan failed",
				"facilityId", id, "child", label, logger.Err(err))
			return out
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[camel(string(c.Name))] = jsonValue(vals[i])
		}
		out = append(out, m)
	}
	return out
}

// jsonValue makes a pgx value safe to marshal.
//
// pgx decodes a uuid column into [16]byte, which encoding/json renders as an
// array of 16 numbers rather than the usual dashed string - an id a client
// cannot use. Same for numeric, which arrives as a struct.
func jsonValue(v any) any {
	switch t := v.(type) {
	case [16]byte:
		u, err := uuid.FromBytes(t[:])
		if err != nil {
			return v
		}
		return u.String()
	case pgtype.Numeric:
		f, err := t.Float64Value()
		if err != nil || !f.Valid {
			return nil
		}
		return f.Float64
	default:
		return v
	}
}

// camel turns snake_case column names into the camelCase the rest of the API
// returns, so an admin client does not have to special-case these lists.
func camel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func itoa(i int) string { return strconv.Itoa(i) }
