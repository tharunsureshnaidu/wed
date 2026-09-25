// Package handler serves venue search.
//
// The Java service ran this on Elasticsearch. There is no Elasticsearch here,
// so search runs on PostgreSQL with trigram indexes - which covers every
// endpoint's contract for this data size. Recent/recently-viewed/trending live
// in Redis, exactly as they did before.
package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

type Handler struct {
	db     *pgxpool.Pool
	rdb    *redis.Client
	signer *jwt.Signer
}

func New(db *pgxpool.Pool, rdb *redis.Client, signer *jwt.Signer) *Handler {
	return &Handler{db: db, rdb: rdb, signer: signer}
}

func (h *Handler) Register(mux *http.ServeMux) {
	// Search is public; the caller's identity is used only to personalise
	// recent/recently-viewed, and is optional everywhere.
	mux.HandleFunc("GET /api/v1/search/venues", h.searchVenues)
	mux.HandleFunc("GET /api/v1/search/autocomplete", h.autocomplete)
	mux.HandleFunc("GET /api/v1/search/suggestions", h.suggestions)
	mux.HandleFunc("GET /api/v1/search/venues/{id}/similar", h.similar)
	mux.HandleFunc("POST /api/v1/search/venues/{id}/view", h.recordView)
	mux.HandleFunc("GET /api/v1/search/recent", h.recentSearches)
	mux.HandleFunc("GET /api/v1/search/recently-viewed", h.recentlyViewed)
	mux.HandleFunc("GET /api/v1/search/trending", h.trending)
	mux.HandleFunc("GET /api/v1/search/popular-cities", h.popularCities)
	// Clearing history is the other half of having it: a user who searched
	// something they would rather not see again needs a way to remove it.
	mux.HandleFunc("DELETE /api/v1/search/recent", h.clearRecentSearches)
}

// clearRecentSearches wipes the caller's own search history. Scoped to their
// viewer key, so one caller can never clear another's.
func (h *Handler) clearRecentSearches(w http.ResponseWriter, r *http.Request) {
	if h.rdb == nil {
		response.OK(w, "Recent searches cleared", nil)
		return
	}
	if err := h.rdb.Del(r.Context(), "search:recent:"+h.viewer(r)).Err(); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Recent searches cleared", nil)
}

// viewer identifies the caller for personalised lists: recent searches and
// recently viewed venues.
//
// A signed-in caller is keyed by user id. Everyone else used to share one
// "anonymous" bucket, which meant one logged-out visitor's search history was
// served to the next - a stranger could see that someone had searched for
// "divorce party venue". Anonymous callers are now keyed by a hash of their
// address and user agent, so the feature still works before sign-in without
// pooling unrelated people together.
//
// That key is a best-effort device fingerprint, not an identity: visitors
// behind one NAT with the same browser still share a bucket. It is good enough
// for a convenience list and deliberately not used for anything else.
func (h *Handler) viewer(r *http.Request) string {
	if tok := r.Header.Get("Authorization"); len(tok) > 7 {
		if claims, err := h.signer.Parse(tok[7:]); err == nil {
			return claims.UserID
		}
	}
	if id, ok := middleware.UserID(r.Context()); ok {
		return strconv.FormatInt(id, 10)
	}
	sum := sha256.Sum256([]byte(httpx.IP(r) + "|" + r.UserAgent()))
	return "anon-" + hex.EncodeToString(sum[:8])
}

type venue struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	City        *string  `json:"city"`
	Description *string  `json:"description"`
	AvgRating   float64  `json:"avgRating"`
	ReviewCount int      `json:"reviewCount"`
	BasePrice   *float64 `json:"basePricePerDay"`
	CapacityPax *int     `json:"capacityPax"`
	IsFeatured  bool     `json:"isFeatured"`
}

type searchResult struct {
	SearchID   string  `json:"searchId"`
	Content    []venue `json:"content"`
	Page       int     `json:"page"`
	Size       int     `json:"size"`
	Total      int64   `json:"totalElements"`
	TotalPages int     `json:"totalPages"`
	HasNext    bool    `json:"hasNext"`
	TookMs     int64   `json:"tookMs"`
}

// paged fills totalPages/hasNext, which Java's SearchResultResponse carries and
// clients page on. Derived rather than stored so the two construction sites
// below cannot disagree.
func (s searchResult) paged() searchResult {
	if s.Size > 0 {
		s.TotalPages = int((s.Total + int64(s.Size) - 1) / int64(s.Size))
	}
	s.HasNext = int64((s.Page+1)*s.Size) < s.Total
	return s
}

func (h *Handler) searchVenues(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	q := r.URL.Query()
	page, size := httpx.Page(r)

	minCap, _ := strconv.Atoi(q.Get("minCapacity"))
	maxCap, _ := strconv.Atoi(q.Get("maxCapacity"))
	minBudget, _ := strconv.ParseFloat(q.Get("minBudget"), 64)
	maxBudget, _ := strconv.ParseFloat(q.Get("maxBudget"), 64)

	// An inverted range can only ever match nothing. Answering "no venues"
	// reads as "none available" and sends the user off changing the wrong
	// filter; Java rejects it, and so does this.
	if minCap > 0 && maxCap > 0 && minCap > maxCap {
		response.Error(w, http.StatusBadRequest,
			"minCapacity cannot exceed maxCapacity", "INVALID_CAPACITY_RANGE")
		return
	}
	if minBudget > 0 && maxBudget > 0 && minBudget > maxBudget {
		response.Error(w, http.StatusBadRequest,
			"minBudget cannot exceed maxBudget", "INVALID_BUDGET_RANGE")
		return
	}

	// Amenity filter: a venue must have ALL of the requested amenities, matched
	// by code or name so either spelling from the client works.
	// A nil slice binds as SQL NULL, and cardinality(NULL) is NULL, not 0 - so
	// "no amenity filter" must be an empty array, not nil, or the whole WHERE
	// clause evaluates to NULL and every row is filtered out.
	amenities := []string{}
	for _, a := range q["amenities"] {
		if a != "" {
			amenities = append(amenities, a)
		}
	}

	// ORDER BY is chosen from a fixed set - never interpolated from user input.
	order := "f.is_featured DESC, f.avg_rating DESC NULLS LAST"
	switch q.Get("sort") {
	case "PRICE_LOW_TO_HIGH":
		order = "f.base_price_per_day ASC NULLS LAST"
	case "PRICE_HIGH_TO_LOW":
		order = "f.base_price_per_day DESC NULLS LAST"
	case "RATING":
		order = "f.avg_rating DESC NULLS LAST"
	case "NEWEST":
		order = "f.created_at DESC"
	case "CAPACITY":
		order = "f.capacity_pax DESC NULLS LAST"
	}

	where := `
		WHERE f.is_deleted = FALSE AND f.status <> 'BLOCKED'
		  AND ($1 = '' OR f.name ILIKE '%'||$1||'%' OR f.description ILIKE '%'||$1||'%')
		  AND ($2 = '' OR LOWER(f.city) = LOWER($2))
		  AND ($3 = '' OR f.type = $3)
		  AND ($4 = 0 OR f.capacity_pax >= $4)
		  AND ($5 = 0 OR f.capacity_pax <= $5)
		  AND ($6 = 0 OR f.base_price_per_day >= $6)
		  AND ($7 = 0 OR f.base_price_per_day <= $7)
		  AND (COALESCE(cardinality($8::text[]), 0) = 0 OR (
		        SELECT count(DISTINCT a.id) FROM facility_amenities fa
		        JOIN amenities a ON a.id = fa.amenity_id
		        WHERE fa.facility_id = f.id
		          AND (a.code = ANY($8::text[]) OR a.name = ANY($8::text[]))
		      ) = cardinality($8::text[]))`

	args := []any{
		q.Get("q") + q.Get("search"), q.Get("city"), q.Get("venueType"),
		minCap, maxCap, minBudget, maxBudget, amenities,
	}

	var total int64
	if err := h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM facilities f`+where, args...).Scan(&total); err != nil {
		httpx.Fail(w, err)
		return
	}

	args = append(args, size, page*size)
	rows, err := h.db.Query(r.Context(),
		`SELECT f.id, f.name, f.type, f.city, f.description, COALESCE(f.avg_rating,0),
		        COALESCE(f.review_count,0), f.base_price_per_day, f.capacity_pax,
		        COALESCE(f.is_featured,FALSE)
		 FROM facilities f`+where+` ORDER BY `+order+` LIMIT $9 OFFSET $10`, args...)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	out := []venue{}
	for rows.Next() {
		var v venue
		if err := rows.Scan(&v.ID, &v.Name, &v.Type, &v.City, &v.Description, &v.AvgRating,
			&v.ReviewCount, &v.BasePrice, &v.CapacityPax, &v.IsFeatured); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, v)
	}

	took := time.Since(started).Milliseconds()
	searchID := h.recordSearch(r, q.Get("q")+q.Get("search"), q.Get("city"), len(out), took)

	response.OK(w, "Venues fetched", searchResult{
		SearchID: searchID, Content: out, Page: page, Size: size,
		Total: total, TookMs: took,
	}.paged())
}

// recordSearch logs the query for trending/popular-cities and pushes it onto the
// caller's recent list. Best-effort: analytics must never fail a search.
func (h *Handler) recordSearch(r *http.Request, query, city string, results int, took int64) string {
	filters, _ := json.Marshal(r.URL.Query())
	var id string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO search_events (user_id, query_text, filters_json, result_count,
		    time_taken_ms, city)
		 VALUES ($1, NULLIF($2,''), $3, $4, $5, NULLIF($6,'')) RETURNING id`,
		h.viewer(r), query, string(filters), results, took, city).Scan(&id)
	if err != nil {
		return ""
	}
	if query != "" && h.rdb != nil {
		key := "search:recent:" + h.viewer(r)
		ctx := r.Context()
		h.rdb.LRem(ctx, key, 0, query)
		h.rdb.LPush(ctx, key, query)
		h.rdb.LTrim(ctx, key, 0, 9)
		h.rdb.Expire(ctx, key, 30*24*time.Hour)
	}
	return id
}

func (h *Handler) autocomplete(w http.ResponseWriter, r *http.Request) {
	h.names(w, r, 10, "Suggestions fetched")
}

func (h *Handler) suggestions(w http.ResponseWriter, r *http.Request) {
	h.names(w, r, 8, "Suggestions fetched")
}

// names returns matching venue names and cities - what a type-ahead needs.
func (h *Handler) names(w http.ResponseWriter, r *http.Request, limit int, msg string) {
	q := r.URL.Query().Get("q")
	if q == "" {
		response.OK(w, msg, []string{})
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT name FROM (
		    SELECT DISTINCT f.name, 1 AS kind FROM facilities f
		      WHERE f.is_deleted = FALSE AND f.name ILIKE '%'||$1||'%'
		    UNION
		    SELECT DISTINCT f.city, 2 AS kind FROM facilities f
		      WHERE f.is_deleted = FALSE AND f.city ILIKE '%'||$1||'%' AND f.city IS NOT NULL
		 ) s ORDER BY kind, name LIMIT $2`, q, limit)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, s)
	}
	response.OK(w, msg, out)
}

// similar finds venues of the same type, preferring the same city and a
// comparable capacity.
func (h *Handler) similar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid venue id", "VALIDATION_ERROR")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	rows, err := h.db.Query(r.Context(),
		`WITH src AS (SELECT type, city, capacity_pax FROM facilities WHERE id = $1)
		 SELECT f.id, f.name, f.type, f.city, f.description, COALESCE(f.avg_rating,0),
		        COALESCE(f.review_count,0), f.base_price_per_day, f.capacity_pax,
		        COALESCE(f.is_featured,FALSE)
		 FROM facilities f, src
		 WHERE f.id <> $1 AND f.is_deleted = FALSE AND f.status <> 'BLOCKED'
		   AND f.type = src.type
		 ORDER BY (LOWER(f.city) = LOWER(src.city)) DESC,
		          abs(COALESCE(f.capacity_pax,0) - COALESCE(src.capacity_pax,0)),
		          f.avg_rating DESC NULLS LAST
		 LIMIT $2`, id, limit)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	out := []venue{}
	for rows.Next() {
		var v venue
		if err := rows.Scan(&v.ID, &v.Name, &v.Type, &v.City, &v.Description, &v.AvgRating,
			&v.ReviewCount, &v.BasePrice, &v.CapacityPax, &v.IsFeatured); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, v)
	}
	response.OK(w, "Similar venues fetched", searchResult{
		Content: out, Page: 0, Size: limit, Total: int64(len(out)),
	}.paged())
}

func (h *Handler) recordView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid venue id", "VALIDATION_ERROR")
		return
	}
	viewer := h.viewer(r)
	if h.rdb != nil {
		key := "search:viewed:" + viewer
		ctx := r.Context()
		h.rdb.LRem(ctx, key, 0, id)
		h.rdb.LPush(ctx, key, id)
		h.rdb.LTrim(ctx, key, 0, 19)
		h.rdb.Expire(ctx, key, 30*24*time.Hour)
	}
	// Attribute the click to the search that produced it, when the caller says so.
	if searchID := r.URL.Query().Get("searchId"); httpx.ValidUUID(searchID) {
		h.db.Exec(r.Context(),
			`UPDATE search_events SET clicked_venue_id = $2 WHERE id = $1`, searchID, id)
	}
	response.OK(w, "View recorded", nil)
}

func (h *Handler) recentSearches(w http.ResponseWriter, r *http.Request) {
	h.redisList(w, r, "search:recent:"+h.viewer(r), "Recent searches fetched")
}

func (h *Handler) recentlyViewed(w http.ResponseWriter, r *http.Request) {
	h.redisList(w, r, "search:viewed:"+h.viewer(r), "Recently viewed venues fetched")
}

func (h *Handler) redisList(w http.ResponseWriter, r *http.Request, key, msg string) {
	if h.rdb == nil {
		response.OK(w, msg, []string{})
		return
	}
	vals, err := h.rdb.LRange(r.Context(), key, 0, -1).Result()
	if err != nil || vals == nil {
		// Redis being down degrades personalisation to empty, never an error.
		vals = []string{}
	}
	response.OK(w, msg, vals)
}

// trending reads the most frequent recent queries straight from search_events,
// so it reflects real traffic rather than a hand-maintained list.
func (h *Handler) trending(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(),
		`SELECT query_text FROM search_events
		 WHERE query_text IS NOT NULL AND query_text <> ''
		   AND created_at > CURRENT_TIMESTAMP - interval '7 days'
		 GROUP BY query_text ORDER BY count(*) DESC, max(created_at) DESC LIMIT 10`)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, s)
	}
	response.OK(w, "Trending searches fetched", out)
}

// popularCities ranks by where venues actually are, falling back from search
// traffic so the list is useful before any searches have been run.
func (h *Handler) popularCities(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT city FROM (
		    SELECT f.city, count(*) * 10 AS score FROM facilities f
		      WHERE f.is_deleted = FALSE AND f.city IS NOT NULL AND f.city <> ''
		      GROUP BY f.city
		    UNION ALL
		    SELECT e.city, count(*) AS score FROM search_events e
		      WHERE e.city IS NOT NULL AND e.city <> ''
		        AND e.created_at > CURRENT_TIMESTAMP - interval '30 days'
		      GROUP BY e.city
		 ) t GROUP BY city ORDER BY sum(score) DESC LIMIT $1`, limit)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, s)
	}
	response.OK(w, "Popular cities fetched", out)
}
