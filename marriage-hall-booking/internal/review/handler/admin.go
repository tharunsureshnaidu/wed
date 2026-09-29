package handler

import (
	"errors"
	"fmt"
	"net/http"
	"time"

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
	mux.Handle("GET /api/v1/admin/reviews", admin(h.adminList))
	mux.Handle("POST /api/v1/admin/reviews", admin(h.adminCreate))
	mux.Handle("PUT /api/v1/admin/reviews/{id}", admin(h.adminUpdate))
	mux.Handle("PATCH /api/v1/admin/reviews/{id}/approve", admin(h.adminApprove))
	mux.Handle("PATCH /api/v1/admin/reviews/{id}/reject", admin(h.adminReject))
	mux.Handle("DELETE /api/v1/admin/reviews/{id}", admin(h.adminDelete))
}

type adminReviewReq struct {
	FacilityID string  `json:"facilityId"`
	UserID     *int64  `json:"userId"` // whose review it is; defaults to the admin
	Rating     int     `json:"rating"`
	Title      *string `json:"title"`
	Comment    *string `json:"comment"`
}

type adminReviewRow struct {
	ID           string    `json:"id"`
	UserID       int64     `json:"userId"`
	UserName     string    `json:"userName"`
	FacilityID   string    `json:"facilityId"`
	FacilityName string    `json:"facilityName"`
	Rating       int       `json:"rating"`
	Title        *string   `json:"title"`
	Comment      *string   `json:"comment"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
}

func (h *Handler) adminList(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)
	statusFilter := r.URL.Query().Get("status")

	qCount := `SELECT COUNT(*) FROM reviews r WHERE r.is_deleted = FALSE`
	qRows := `SELECT r.id::text, r.user_id, u.full_name, r.facility_id::text, f.name,
	                 r.rating, r.title, r.comment, r.status, r.created_at
	            FROM reviews r
	            JOIN users u ON u.id = r.user_id
	            JOIN facilities f ON f.id = r.facility_id
	           WHERE r.is_deleted = FALSE`
	argsCount := []any{}
	argsRows := []any{}

	if statusFilter != "" {
		qCount += ` AND r.status = $1`
		qRows += ` AND r.status = $1`
		argsCount = append(argsCount, statusFilter)
		argsRows = append(argsRows, statusFilter)
	}

	var total int64
	if err := h.db.QueryRow(r.Context(), qCount, argsCount...).Scan(&total); err != nil {
		httpx.Fail(w, err)
		return
	}

	limitIdx := len(argsRows) + 1
	offsetIdx := len(argsRows) + 2
	qRows += fmt.Sprintf(` ORDER BY r.created_at DESC LIMIT $%d OFFSET $%d`, limitIdx, offsetIdx)
	argsRows = append(argsRows, size, page*size)

	rows, err := h.db.Query(r.Context(), qRows, argsRows...)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	out := []adminReviewRow{}
	for rows.Next() {
		var x adminReviewRow
		if err := rows.Scan(&x.ID, &x.UserID, &x.UserName, &x.FacilityID, &x.FacilityName,
			&x.Rating, &x.Title, &x.Comment, &x.Status, &x.CreatedAt); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Admin reviews retrieved successfully", httpx.NewPaged(out, page, size, total))
}

func (h *Handler) adminApprove(w http.ResponseWriter, r *http.Request) {
	h.adminUpdateStatus(w, r, "APPROVED")
}

func (h *Handler) adminReject(w http.ResponseWriter, r *http.Request) {
	h.adminUpdateStatus(w, r, "REJECTED")
}

func (h *Handler) adminUpdateStatus(w http.ResponseWriter, r *http.Request, targetStatus string) {
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
		`UPDATE reviews SET status = $2, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE
		 RETURNING facility_id`, id, targetStatus).Scan(&facilityID)
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

	msg := "Review approved successfully"
	if targetStatus == "REJECTED" {
		msg = "Review rejected successfully"
	}
	response.OK(w, msg, map[string]any{
		"id": id, "facilityId": facilityID, "status": targetStatus,
	})
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
		`INSERT INTO reviews (facility_id, user_id, rating, title, comment, status)
		 VALUES ($1,$2,$3,$4,$5,'APPROVED')
		 ON CONFLICT (user_id, facility_id) WHERE is_deleted = FALSE DO UPDATE
		    SET rating = excluded.rating, title = excluded.title,
		        comment = excluded.comment, status = 'APPROVED', updated_at = CURRENT_TIMESTAMP
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
		"rating": req.Rating, "title": req.Title, "comment": req.Comment, "status": "APPROVED",
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
