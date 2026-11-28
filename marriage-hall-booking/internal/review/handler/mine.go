package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// myReview is one card on the My Reviews screen. It carries the venue's name
// and cover image because the screen shows them: a bare facilityId would force
// the client into one extra request per row.
type myReview struct {
	ID           string    `json:"id"`
	FacilityID   string    `json:"facilityId"`
	FacilityName string    `json:"facilityName"`
	FacilityCity *string   `json:"facilityCity,omitempty"`
	CoverImage   *string   `json:"coverImage,omitempty"`
	Rating       int       `json:"rating"`
	Title        *string   `json:"title"`
	Comment      *string   `json:"comment"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
}

// listMine is GET /api/v1/reviews/my-reviews.
//
// The stats come back with the page rather than from a second endpoint: the
// screen draws them in its header and would otherwise render empty while a
// second request was in flight. They are computed over the whole set, not the
// page - "2 Total Reviews" must not become "20" because the user scrolled.
func (h *Handler) listMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}
	page, size := httpx.Page(r)

	var total int64
	var avg *float64
	if err := h.db.QueryRow(r.Context(), `
		SELECT COUNT(*), ROUND(AVG(rating)::numeric, 1)::double precision
		  FROM reviews WHERE user_id = $1 AND is_deleted = FALSE`,
		userID).Scan(&total, &avg); err != nil {
		httpx.Fail(w, err)
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT r.id::text, f.id::text, f.name, f.city,
		       (SELECT i.url FROM facility_images i
		         WHERE i.facility_id = f.id
		         ORDER BY i.is_cover DESC, i.sort_order LIMIT 1),
		       r.rating, r.title, r.comment, r.status, r.created_at
		  FROM reviews r
		  JOIN facilities f ON f.id = r.facility_id
		 WHERE r.user_id = $1 AND r.is_deleted = FALSE
		 ORDER BY r.created_at DESC
		 LIMIT $2 OFFSET $3`, userID, size, httpx.Offset(page, size))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	out := []myReview{}
	for rows.Next() {
		var m myReview
		if err := rows.Scan(&m.ID, &m.FacilityID, &m.FacilityName, &m.FacilityCity,
			&m.CoverImage, &m.Rating, &m.Title, &m.Comment, &m.Status, &m.CreatedAt); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, err)
		return
	}

	// httpx.NewPaged is the shape every other list in this service returns.
	// An ad-hoc map here would break the client's one pagination helper and
	// silently omit totalPages.
	paged := httpx.NewPaged(out, page, size, total)

	// The header stats ride alongside the page. avgRatingGiven is what this
	// user awarded, not what the venues scored, and it is null rather than 0
	// with no reviews - 0 would draw as a zero-star average.
	response.OK(w, "Reviews retrieved successfully", map[string]any{
		"content":        paged.Content,
		"page":           paged.Page,
		"size":           paged.Size,
		"totalElements":  paged.TotalElements,
		"totalPages":     paged.TotalPages,
		"totalReviews":   total,
		"avgRatingGiven": avg,
	})
}

// myEntityReview is the response shape for GET /api/v1/reviews/me?type=hotel|hall&id={id}.
type myEntityReview struct {
	ID           string     `json:"id"`
	EntityID     string     `json:"entityId"`
	FacilityID   string     `json:"facilityId"`
	EntityType   string     `json:"entityType"`
	FacilityName string     `json:"facilityName"`
	FacilityCity *string    `json:"facilityCity,omitempty"`
	Rating       int        `json:"rating"`
	Title        *string    `json:"title"`
	Comment      *string    `json:"comment"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

// getMineForEntity handles GET /api/v1/reviews/me?type=hotel&id={id}
// and GET /api/v1/reviews/me?type=hall&id={id}.
func (h *Handler) getMineForEntity(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	entityType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	if entityType != "hotel" && entityType != "hall" {
		response.Error(w, http.StatusBadRequest, "type parameter is required and must be either 'hotel' or 'hall'", "VALIDATION_ERROR")
		return
	}

	entityID := strings.TrimSpace(r.URL.Query().Get("id"))
	if entityID == "" || !httpx.ValidUUID(entityID) {
		response.Error(w, http.StatusBadRequest, "A valid id is required", "VALIDATION_ERROR")
		return
	}

	dbType := "HOTEL"
	entityLabel := "Hotel"
	if entityType == "hall" {
		dbType = "MARRIAGE_HALL"
		entityLabel = "Hall"
	}

	// 1. Verify that the specified hotel or hall exists.
	var facilityName string
	err := h.db.QueryRow(r.Context(), `
		SELECT name
		  FROM facilities
		 WHERE id = $1 AND type = $2 AND is_deleted = FALSE`,
		entityID, dbType).Scan(&facilityName)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, entityLabel+" not found", "NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	// 2. Fetch the user's review for this entity.
	var rev myEntityReview
	rev.EntityID = entityID
	rev.FacilityID = entityID
	rev.EntityType = entityType
	rev.FacilityName = facilityName

	err = h.db.QueryRow(r.Context(), `
		SELECT r.id::text, f.city, r.rating, r.title, r.comment, r.status, r.created_at, r.updated_at
		  FROM reviews r
		  JOIN facilities f ON f.id = r.facility_id
		 WHERE r.facility_id = $1
		   AND r.user_id = $2
		   AND r.is_deleted = FALSE`,
		entityID, userID).Scan(
		&rev.ID,
		&rev.FacilityCity,
		&rev.Rating,
		&rev.Title,
		&rev.Comment,
		&rev.Status,
		&rev.CreatedAt,
		&rev.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Review not found for this "+entityType, "REVIEW_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Review retrieved successfully", rev)
}
