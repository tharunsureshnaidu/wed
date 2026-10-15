// Package handler serves venue search.
//
// The Java service ran this on Elasticsearch. There is no Elasticsearch here,
// so search runs on PostgreSQL with pg_trgm - typo-tolerant and ranked by
// match quality, see pkg/venuesearch - which covers every endpoint's contract
// for this data size. Recent/recently-viewed/trending live
// in Redis, exactly as they did before.
package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	facilityrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/geo"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuesearch"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
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
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Type        string               `json:"type"`
	City        *string              `json:"city"`
	Description *string              `json:"description"`
	AvgRating   float64              `json:"avgRating"`
	ReviewCount int                  `json:"reviewCount"`
	BasePrice   *float64             `json:"basePricePerDay"`
	CapacityPax *int                 `json:"capacityPax"`
	IsFeatured  bool                 `json:"isFeatured"`
	CoverImage  *string              `json:"coverImage"`
	Images      []facilityrepo.Image `json:"images"`
	// DistanceKm is from the caller's ?lat=&lng=; absent without a location,
	// and for a venue with no coordinates - unknown, never 0.
	DistanceKm *float64 `json:"distanceKm,omitempty"`
	lat, lng   *float64
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
	// Relaxed is true when no venue matched every word and these are the
	// closest partial matches instead - the client's cue for "no exact match".
	Relaxed bool `json:"relaxed"`
	// DidYouMean is a corrected spelling that does find venues, offered when
	// nothing matched word for word; null otherwise.
	DidYouMean *string `json:"didYouMean"`
	// Facets are the filter sheet's counts and price range for this search.
	Facets facets `json:"facets"`
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

	// An unparseable filter used to be dropped silently, so "minBudget=50k"
	// searched with no budget at all and looked like a working filter.
	var bad []string
	num := func(name string, whole bool) float64 {
		v := q.Get(name)
		if v == "" {
			return 0
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) || (whole && f != math.Trunc(f)) {
			bad = append(bad, name)
			return 0
		}
		return f
	}
	minCap := int(num("minCapacity", true))
	maxCap := int(num("maxCapacity", true))
	minBudget := num("minBudget", false)
	maxBudget := num("maxBudget", false)
	if len(bad) > 0 {
		response.Error(w, http.StatusBadRequest,
			strings.Join(bad, ", ")+" must be a non-negative number", "VALIDATION_ERROR")
		return
	}

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

	raw := strings.TrimSpace(q.Get("q") + " " + q.Get("search"))
	text := venuesearch.Clean(raw)

	st, msg := parseStay(q, time.Now())
	if msg != "" {
		response.Error(w, http.StatusBadRequest, msg, "INVALID_DATES")
		return
	}
	rooms := int(num("rooms", true))
	if rooms == 0 {
		rooms = 1
	}

	// Distance follows the GPS rule everywhere else: an absent or malformed
	// location is no location, never a 400. radiusKm is the caller's own
	// choice, so out of range is loud - as on /coupons/available.
	here := geo.Parse(r)
	radius := num("radiusKm", false)
	if len(bad) == 0 && q.Get("radiusKm") != "" && (radius <= 0 || radius > maxRadiusKm) {
		response.Error(w, http.StatusBadRequest,
			"radiusKm must be greater than 0 and at most 500", "INVALID_RADIUS")
		return
	}
	if len(bad) > 0 {
		response.Error(w, http.StatusBadRequest,
			strings.Join(bad, ", ")+" must be a non-negative number", "VALIDATION_ERROR")
		return
	}
	var lat, lng *float64
	if here.OK {
		lat, lng = &here.Lat, &here.Lng
	}

	// ORDER BY is chosen from a fixed set - never interpolated from user input.
	// With search text and no explicit sort, the best match leads: a typo
	// like "palce" must not rank Grand Palace below every featured venue.
	// An explicit sort leads instead, and relevance breaks its ties.
	// Then featured, then rating_score - the review-weighted rating (migration
	// 062), so 5.0 from one review does not outrank 4.6 from 200 - lifted by
	// recent bookings, so venues people actually book rise. f.id last keeps
	// paging stable across ties.
	rank := "f.is_featured DESC, " + popularSQL + " DESC"
	if text != "" {
		rank = venuesearch.ScoreSQL("$1") + " DESC, " + rank
	}
	order := rank
	switch q.Get("sort") {
	case "PRICE_LOW_TO_HIGH":
		order = "f.base_price_per_day ASC NULLS LAST, " + rank
	case "PRICE_HIGH_TO_LOW":
		order = "f.base_price_per_day DESC NULLS LAST, " + rank
	case "RATING":
		order = "f.rating_score DESC, " + rank
	case "POPULARITY":
		order = "COALESCE(pop.n, 0) DESC, " + rank
	case "NEWEST":
		order = "f.created_at DESC, " + rank
	case "CAPACITY":
		order = "f.capacity_pax DESC NULLS LAST, " + rank
	case "DISTANCE":
		// Venues without coordinates last: unknown is not near.
		if here.OK {
			order = distanceSQL + " ASC NULLS LAST, " + rank
		}
	}
	order += ", f.id"

	where := func(match string) string {
		return `
		WHERE ` + venuetype.LiveSQL("f") + `
		  AND ` + match + `
		  AND ($2 = '' OR LOWER(f.city) = $2)
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
		      ) = cardinality($8::text[]))
		  AND ($9 = '' OR EXISTS (
		        SELECT 1 FROM facility_events fe
		         WHERE fe.facility_id = f.id AND fe.event_code = $9)
		      OR NOT EXISTS (
		        SELECT 1 FROM facility_events fe WHERE fe.facility_id = f.id))
		  AND ` + radiusSQL + `
		  AND ` + availableSQL
	}

	// eventType filters to venues that host it. A venue which has declared
	// NOTHING still matches: 43 halls predate the feature, and hiding every one
	// of them the moment a filter is used would look like the search is broken.
	// Silence is "not stated", not "no".
	vType := q.Get("type")
	if vType == "" {
		vType = q.Get("venueType")
	}
	evType := q.Get("eventType")
	if evType == "" {
		evType = q.Get("event_type")
	}
	args := []any{
		text, venuesearch.City(q.Get("city")), venuetype.Stored(vType),
		minCap, maxCap, minBudget, maxBudget, amenities,
		strings.ToUpper(strings.TrimSpace(evType)),
		lat, lng, radius, st.Start, st.End, st.Slot, rooms,
	}
	pageArgs := append(append([]any{}, args...), size, httpx.Offset(page, size))

	// Every word must match first; only when that finds nothing does the
	// looser whole-phrase match get a turn, so the screen is never empty for
	// a query that matches partly.
	ctx := r.Context()
	match := where(venuesearch.MatchSQL("$1", text))
	out, total, err := h.searchPage(ctx, match, order, pageArgs)
	relaxed := false
	var didYouMean *string
	if err == nil && total == 0 && text != "" {
		relaxed = true
		didYouMean, err = h.didYouMean(ctx, text, where, args)
		if err == nil {
			match = where(venuesearch.LooseSQL("$1"))
			out, total, err = h.searchPage(ctx, match, order, pageArgs)
		}
	}
	var fc facets
	if err == nil {
		fc, err = h.facets(ctx, match, args)
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	for i := range out {
		out[i].DistanceKm = here.DistanceFrom(out[i].lat, out[i].lng)
	}
	h.attachImages(ctx, out)

	took := time.Since(started).Milliseconds()
	searchID := h.recordSearch(r, raw, q.Get("city"), len(out), took)

	response.OK(w, "Venues fetched", searchResult{
		SearchID: searchID, Content: out, Page: page, Size: size,
		Total: total, TookMs: took, Relaxed: relaxed, DidYouMean: didYouMean,
		Facets: fc,
	}.paged())
}

// maxRadiusKm caps radiusKm, as on /coupons/available.
const maxRadiusKm = 500

// popularSQL is the default ranking score: rating_score plus a bonus for
// bookings in the last 90 days that were not cancelled, on a log scale so the
// tenth booking adds less than the first - 20 recent bookings lift a 3.5
// venue to about 4.26, past a 4.2 venue nobody books.
//
// ponytail: aggregated from bookings on every page query, unindexed by date.
// Past ~100k bookings, keep the count on facilities and refresh it hourly.
const popularSQL = "(f.rating_score + 0.25 * ln(1 + COALESCE(pop.n, 0)))"

const popularJoin = `
	LEFT JOIN (SELECT target_id, count(*) AS n FROM bookings
	            WHERE status IN ('PENDING', 'CONFIRMED', 'COMPLETED') AND is_deleted IS NOT TRUE
	              AND created_at > now() - interval '90 days'
	            GROUP BY target_id) pop ON pop.target_id = f.id`

// distanceSQL is metres from the caller ($10, $11) to venue f; NULL when
// either has no coordinates. ll_to_earth(f.lat::float8, f.lng::float8) is
// the expression idx_facilities_earth indexes.
const distanceSQL = "earth_distance(ll_to_earth($10, $11), ll_to_earth(f.lat::float8, f.lng::float8))"

// radiusSQL keeps venues within $12 km of the caller. A venue with no
// coordinates is kept: it is hidden only when we can prove it is far away,
// as with nearby coupons. No location, or no radius, filters nothing.
const radiusSQL = `($12::float8 = 0 OR $10::float8 IS NULL OR f.lat IS NULL OR f.lng IS NULL
		OR (earth_box(ll_to_earth($10, $11), $12 * 1000) @> ll_to_earth(f.lat::float8, f.lng::float8)
		    AND ` + distanceSQL + ` <= $12 * 1000))`

// didYouMean is the corrected spelling of a search that matched nothing word
// for word - offered, never applied - and only when the correction finds
// venues with the same filters: suggesting another dead end is worse than
// suggesting nothing.
func (h *Handler) didYouMean(ctx context.Context, text string, where func(string) string, args []any) (*string, error) {
	fixed, err := h.correction(ctx, text)
	if err != nil || fixed == "" {
		return nil, err
	}
	clean := venuesearch.Clean(fixed)
	a := append([]any{clean}, args[1:]...)
	var n int64
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM facilities f`+
		where(venuesearch.MatchSQL("$1", clean)), a...).Scan(&n); err != nil || n == 0 {
		return nil, err
	}
	return &fixed, nil
}

// searchPage runs one search: the total, then the page. args ends with LIMIT
// and OFFSET as $17/$18; the count ignores them.
//
// Two queries rather than count(*) OVER(): the window would evaluate every
// match, where the page query alone can walk the price or rating index and
// stop at LIMIT.
func (h *Handler) searchPage(ctx context.Context, where, order string, args []any) ([]venue, int64, error) {
	var total int64
	if err := h.db.QueryRow(ctx,
		`SELECT count(*) FROM facilities f`+where, args[:len(args)-2]...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := h.db.Query(ctx,
		`SELECT f.id, f.name, f.type, f.city, f.description, COALESCE(f.avg_rating,0),
		        COALESCE(f.review_count,0), f.base_price_per_day, f.capacity_pax,
		        COALESCE(f.is_featured,FALSE), f.lat::float8, f.lng::float8
		 FROM facilities f`+popularJoin+where+` ORDER BY `+order+` LIMIT $17 OFFSET $18`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []venue{}
	for rows.Next() {
		var v venue
		if err := rows.Scan(&v.ID, &v.Name, &v.Type, &v.City, &v.Description, &v.AvgRating,
			&v.ReviewCount, &v.BasePrice, &v.CapacityPax, &v.IsFeatured, &v.lat, &v.lng); err != nil {
			return nil, 0, err
		}
		v.Type = venuetype.API(v.Type)
		out = append(out, v)
	}
	return out, total, rows.Err()
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
	h.suggest(w, r, 10, "Suggestions fetched")
}

func (h *Handler) suggestions(w http.ResponseWriter, r *http.Request) {
	h.suggest(w, r, 8, "Suggestions fetched")
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
		 WHERE f.id <> $1 AND `+venuetype.LiveSQL("f")+`
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
		v.Type = venuetype.API(v.Type)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, err)
		return
	}
	h.attachImages(r.Context(), out)
	response.OK(w, "Similar venues fetched", searchResult{
		Content: out, Page: 0, Size: limit, Total: int64(len(out)),
	}.paged())
}

func (h *Handler) attachImages(ctx context.Context, venues []venue) {
	if len(venues) == 0 {
		return
	}
	ids := make([]string, len(venues))
	for i, v := range venues {
		ids[i] = v.ID
	}
	images, err := facilityrepo.New(h.db).AllImagesOfMany(ctx, ids)
	if err != nil {
		for i := range venues {
			if venues[i].Images == nil {
				venues[i].Images = []facilityrepo.Image{}
			}
		}
		return
	}
	for i := range venues {
		if im, ok := images[venues[i].ID]; ok && im != nil {
			venues[i].Images = im
			venues[i].CoverImage = facilityrepo.DeriveCoverImage(im)
		} else {
			venues[i].Images = []facilityrepo.Image{}
		}
	}
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
	if err := rows.Err(); err != nil {
		httpx.Fail(w, err)
		return
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
		      WHERE `+venuetype.LiveSQL("f")+` AND f.city IS NOT NULL AND f.city <> ''
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
	if err := rows.Err(); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Popular cities fetched", out)
}
