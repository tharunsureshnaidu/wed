// Package handler serves facility reviews.
package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
)

type Handler struct {
	db     *pgxpool.Pool
	signer *jwt.Signer

	// OnReviewCreated tells the venue owner someone rated them. The reviewer
	// is not notified - they just wrote it.
	OnReviewCreated func(ctx context.Context, facilityID string, rating int)
}

func New(db *pgxpool.Pool, signer *jwt.Signer) *Handler {
	return &Handler{db: db, signer: signer}
}

func (h *Handler) Register(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	// Reading reviews is public; writing one requires an account.
	mux.HandleFunc("GET /api/v1/reviews/facility/{facilityId}", h.listForFacility)
	mux.HandleFunc("GET /api/v1/reviews/facility/{facilityId}/summary", h.summary)
	// Registered before the facility routes so the literal path wins; a user
	// reading their own reviews needs no facility id.
	mux.Handle("GET /api/v1/reviews/my-reviews", auth(http.HandlerFunc(h.listMine)))
	mux.Handle("POST /api/v1/reviews", auth(http.HandlerFunc(h.create)))
	mux.Handle("DELETE /api/v1/reviews/{id}", auth(http.HandlerFunc(h.delete)))
}

type createReq struct {
	FacilityID string  `json:"facilityId"`
	BookingID  *string `json:"bookingId"`
	Rating     int     `json:"rating"`
	Title      *string `json:"title"`
	Comment    *string `json:"comment"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createReq
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

	userID, _ := middleware.UserID(r.Context())

	// A review must be earned: the reviewer needs a completed or confirmed
	// booking at this facility. Without that check anyone could rate any venue,
	// repeatedly, and the ratings would be worthless.
	var stayed bool
	if err := h.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM bookings
		   WHERE user_id = $1 AND target_id = $2
		     AND status IN ('CONFIRMED','COMPLETED') AND is_deleted = FALSE)`,
		userID, req.FacilityID).Scan(&stayed); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !stayed {
		response.Error(w, http.StatusForbidden,
			"You can only review a venue you have booked", "NO_ELIGIBLE_BOOKING")
		return
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	var id string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO reviews (facility_id, user_id, booking_id, rating, title, comment)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		req.FacilityID, userID, req.BookingID, req.Rating, req.Title, req.Comment).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			response.Error(w, http.StatusConflict,
				"You have already reviewed this venue", "REVIEW_EXISTS")
			return
		}
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

	if h.OnReviewCreated != nil {
		h.OnReviewCreated(r.Context(), req.FacilityID, req.Rating)
	}
	response.Created(w, "Review submitted successfully", "/api/v1/reviews/"+id, map[string]any{
		"id": id, "facilityId": req.FacilityID, "rating": req.Rating,
		"title": req.Title, "comment": req.Comment,
	})
}

// recalcRating keeps facilities.avg_rating/review_count consistent with the
// reviews table, in the same transaction as the change that caused it.
//
// Skips a facility whose rating an admin has pinned (rating_is_manual): an
// imported venue carries a rating earned on the source platform, with no
// review rows here to derive it from, and the first review posted in this
// system would otherwise replace "4.3 from 218 reviews" with "5.0 from 1".
func (h *Handler) recalcRating(ctx context.Context, tx pgx.Tx, facilityID string) error {
	_, err := tx.Exec(ctx,
		`UPDATE facilities f SET
		    avg_rating = COALESCE((SELECT round(avg(rating)::numeric, 2) FROM reviews
		        WHERE facility_id = f.id AND is_deleted = FALSE), 0),
		    review_count = (SELECT count(*) FROM reviews
		        WHERE facility_id = f.id AND is_deleted = FALSE),
		    updated_at = CURRENT_TIMESTAMP
		 WHERE f.id = $1 AND f.rating_is_manual = FALSE`, facilityID)
	return err
}

func (h *Handler) listForFacility(w http.ResponseWriter, r *http.Request) {
	facilityID := r.PathValue("facilityId")
	if !httpx.ValidUUID(facilityID) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	page, size := httpx.Page(r)

	var total int64
	if err := h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM reviews WHERE facility_id = $1 AND is_deleted = FALSE`,
		facilityID).Scan(&total); err != nil {
		httpx.Fail(w, err)
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT r.id, r.user_id, u.full_name, r.rating, r.title, r.comment, r.created_at
		 FROM reviews r JOIN users u ON u.id = r.user_id
		 WHERE r.facility_id = $1 AND r.is_deleted = FALSE
		 ORDER BY r.created_at DESC LIMIT $2 OFFSET $3`, facilityID, size, page*size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID        string    `json:"id"`
		UserID    int64     `json:"userId"`
		UserName  string    `json:"userName"`
		Rating    int       `json:"rating"`
		Title     *string   `json:"title"`
		Comment   *string   `json:"comment"`
		CreatedAt time.Time `json:"createdAt"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.UserID, &x.UserName, &x.Rating, &x.Title, &x.Comment,
			&x.CreatedAt); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	response.OK(w, "Reviews retrieved successfully", httpx.NewPaged(out, page, size, total))
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	facilityID := r.PathValue("facilityId")
	if !httpx.ValidUUID(facilityID) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	// Java's FacilityRatingSummaryDTO: averageRating plus a rating->count map.
	// The flat five/four/... fields shipped already, so both are returned.
	var s struct {
		FacilityID    string           `json:"facilityId"`
		AverageRating float64          `json:"averageRating"`
		AvgRating     float64          `json:"avgRating"`
		Total         int64            `json:"totalReviews"`
		StarCounts    map[string]int64 `json:"starCounts"`
		Five          int64            `json:"five"`
		Four          int64            `json:"four"`
		Three         int64            `json:"three"`
		Two           int64            `json:"two"`
		One           int64            `json:"one"`
	}
	s.FacilityID = facilityID
	err := h.db.QueryRow(r.Context(),
		`SELECT COALESCE(round(avg(rating)::numeric, 2), 0), count(*),
		        count(*) FILTER (WHERE rating = 5), count(*) FILTER (WHERE rating = 4),
		        count(*) FILTER (WHERE rating = 3), count(*) FILTER (WHERE rating = 2),
		        count(*) FILTER (WHERE rating = 1)
		 FROM reviews WHERE facility_id = $1 AND is_deleted = FALSE`, facilityID).
		Scan(&s.AvgRating, &s.Total, &s.Five, &s.Four, &s.Three, &s.Two, &s.One)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.AverageRating = s.AvgRating
	s.StarCounts = map[string]int64{
		"5": s.Five, "4": s.Four, "3": s.Three, "2": s.Two, "1": s.One,
	}
	response.OK(w, "Rating summary retrieved successfully", s)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid review id", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	isAdmin := middleware.HasRole(r.Context(), domain.RoleAdmin)

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	// Scope the delete to the author unless an admin is moderating, so one user
	// cannot remove another's review.
	var facilityID string
	err = tx.QueryRow(r.Context(),
		`UPDATE reviews SET is_deleted = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE AND ($3 OR user_id = $2)
		 RETURNING facility_id`, id, userID, isAdmin).Scan(&facilityID)
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
