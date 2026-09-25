package handler

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
)

// Admin review management.
//
// The public POST /api/v1/reviews requires the reviewer to have booked the
// venue, which is what stops ratings being invented. Admin needs to seed and
// correct reviews - listings imported from elsewhere arrive with ratings
// already attached, and a review that breaks the rules has to be editable
// rather than only deletable - so these routes skip the booking check and are
// restricted to ROLE_ADMIN.
func (h *Handler) RegisterAdmin(mux *http.ServeMux) {
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequireAuth(h.signer),
			middleware.RequireRole(domain.RoleAdmin))
	}
	mux.Handle("POST /api/v1/admin/reviews", admin(h.adminCreate))
	mux.Handle("PUT /api/v1/admin/reviews/{id}", admin(h.adminUpdate))
	mux.Handle("DELETE /api/v1/admin/reviews/{id}", admin(h.adminDelete))
}

type adminReviewReq struct {
	FacilityID string  `json:"facilityId"`
	UserID     *int64  `json:"userId"` // whose review it is; defaults to the admin
	Rating     int     `json:"rating"`
	Title      *string `json:"title"`
	Comment    *string `json:"comment"`
}

func (h *Handler) adminCreate(w http.ResponseWriter, r *http.Request) {
	var req adminReviewReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	if !httpx.ValidUUID(req.FacilityID) {
		e = append(e, "A valid facilityId is required")
	}
	if req.Rating < 1 || req.Rating > 5 {
		e = append(e, "rating must be between 1 and 5")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	author, _ := middleware.UserID(r.Context())
	if req.UserID != nil {
		author = *req.UserID
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	// One live review per (facility, user), and an admin correcting a rating
	// should not have to delete the old row first.
	//
	// The WHERE on the conflict target is required, not decorative: the index
	// is partial (migration 047, so a deleted review stops blocking a new one),
	// and an inference clause without the same predicate matches no index and
	// fails the whole statement.
	var id string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO reviews (facility_id, user_id, rating, title, comment)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (user_id, facility_id) WHERE is_deleted = FALSE DO UPDATE
		    SET rating = excluded.rating, title = excluded.title,
		        comment = excluded.comment, updated_at = CURRENT_TIMESTAMP
		 RETURNING id`,
		req.FacilityID, author, req.Rating, req.Title, req.Comment).Scan(&id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.recalcRating(r.Context(), tx, req.FacilityID); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Review saved successfully", map[string]any{
		"id": id, "facilityId": req.FacilityID, "userId": author,
		"rating": req.Rating, "title": req.Title, "comment": req.Comment,
	})
}

func (h *Handler) adminUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid review id", "VALIDATION_ERROR")
		return
	}
	var req adminReviewReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Rating < 1 || req.Rating > 5 {
		response.Error(w, http.StatusBadRequest,
			"rating must be between 1 and 5", "VALIDATION_ERROR")
		return
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	// facility_id comes back from the row: the rating average has to be
	// recomputed for the facility the review actually belongs to, not one the
	// caller names.
	var facilityID string
	err = tx.QueryRow(r.Context(),
		`UPDATE reviews SET rating = $2, title = $3, comment = $4,
		    updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE
		 RETURNING facility_id`, id, req.Rating, req.Title, req.Comment).Scan(&facilityID)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Review not found", "REVIEW_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.recalcRating(r.Context(), tx, facilityID); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Review updated successfully", map[string]any{
		"id": id, "facilityId": facilityID, "rating": req.Rating,
		"title": req.Title, "comment": req.Comment,
	})
}

func (h *Handler) adminDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid review id", "VALIDATION_ERROR")
		return
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	var facilityID string
	err = tx.QueryRow(r.Context(),
		`UPDATE reviews SET is_deleted = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE RETURNING facility_id`, id).Scan(&facilityID)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Review not found", "REVIEW_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.recalcRating(r.Context(), tx, facilityID); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Review deleted successfully", nil)
}
