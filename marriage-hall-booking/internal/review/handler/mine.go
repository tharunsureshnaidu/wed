package handler

import (
	"net/http"
	"time"

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
		       r.rating, r.title, r.comment, r.created_at
		  FROM reviews r
		  JOIN facilities f ON f.id = r.facility_id
		 WHERE r.user_id = $1 AND r.is_deleted = FALSE
		 ORDER BY r.created_at DESC
		 LIMIT $2 OFFSET $3`, userID, size, page*size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	out := []myReview{}
	for rows.Next() {
		var m myReview
		if err := rows.Scan(&m.ID, &m.FacilityID, &m.FacilityName, &m.FacilityCity,
			&m.CoverImage, &m.Rating, &m.Title, &m.Comment, &m.CreatedAt); err != nil {
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
