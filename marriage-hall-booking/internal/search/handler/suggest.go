package handler

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/eventtypes"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuesearch"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

// suggestion is one type-ahead row. Typed, so the app can draw a pin for a
// city, a venue's photo, and route a tap to the right screen - a bare string
// could only ever be typed back into the search box.
//
//	VENUE     a venue; id opens it
//	CITY      a city; count venues in it
//	CATEGORY  "Marriage halls in Bengaluru", "Sangeet venues": a search with
//	          venueType / city / eventCode preset
//	RECENT    the caller's own recent search, when nothing is typed yet
type suggestion struct {
	Type      string  `json:"type"`
	Label     string  `json:"label"`
	Sublabel  *string `json:"sublabel,omitempty"`
	ID        string  `json:"id,omitempty"`
	VenueType string  `json:"venueType,omitempty"`
	City      string  `json:"city,omitempty"`
	EventCode string  `json:"eventCode,omitempty"`
	Count     int64   `json:"count,omitempty"`
}

var typePlural = map[string]string{"MARRIAGE_HALL": "Marriage halls", "HOTEL": "Hotels"}

// suggest serves /autocomplete and /suggestions. Cities first, then
// categories, then venues - the order Booking.com and Airbnb use, since a
// place narrows more than a name. Venues match exactly as /search/venues
// does, so a name offered here is one the search finds; and only live venues
// are counted or offered.
func (h *Handler) suggest(w http.ResponseWriter, r *http.Request, limit int, msg string) {
	text := venuesearch.Clean(r.URL.Query().Get("q"))
	out := []suggestion{}
	if text == "" {
		if h.rdb != nil {
			recent, _ := h.rdb.LRange(r.Context(), "search:recent:"+h.viewer(r), 0, int64(limit-1)).Result()
			for _, s := range recent {
				out = append(out, suggestion{Type: "RECENT", Label: s})
			}
		}
		response.OK(w, msg, out)
		return
	}
	ctx := r.Context()
	typed := venuesearch.Typed(text)

	// Cities: a word begins the stored name or an old one ("banga" ->
	// Bengaluru), or nearly matches it.
	var aliased []string
	for _, w := range typed {
		aliased = append(aliased, venuesearch.CitiesStartingWith(w)...)
	}
	rows, err := h.db.Query(ctx, `SELECT f.city, count(*) FROM facilities f
		 WHERE `+venuetype.LiveSQL("f")+` AND f.city <> ''
		   AND (lower(f.city) = ANY($2::text[]) OR EXISTS (
		        SELECT 1 FROM unnest($1::text[]) w
		         WHERE f.city ILIKE w||'%' OR word_similarity(w, f.city) >= 0.4))
		 GROUP BY f.city ORDER BY 2 DESC, 1 LIMIT 2`, typed, aliased)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	type cityCount struct {
		Name  string
		Count int64
	}
	cities, err := pgx.CollectRows(rows, pgx.RowToStructByPos[cityCount])
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	for _, c := range cities {
		out = append(out, suggestion{Type: "CITY", Label: c.Name, City: c.Name, Count: c.Count})
	}
	city := ""
	if len(cities) > 0 {
		city = cities[0].Name
	}

	cats, err := h.categories(ctx, typed, city)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	out = append(out, cats...)

	// Venues fill what is left, best match first.
	if n := limit - len(out); n > 0 {
		rows, err := h.db.Query(ctx, `SELECT f.id::text, f.name, f.city, f.type FROM facilities f
			 WHERE `+venuetype.LiveSQL("f")+` AND `+venuesearch.MatchSQL("$1", text)+`
			 ORDER BY `+venuesearch.ScoreSQL("$1")+` DESC, f.rating_score DESC, f.id LIMIT $2`, text, n)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		venues, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (s suggestion, err error) {
			s.Type = "VENUE"
			err = row.Scan(&s.ID, &s.Label, &s.Sublabel, &s.VenueType)
			s.VenueType = venuetype.API(s.VenueType)
			return s, err
		})
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, venues...)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	response.OK(w, msg, out)
}

// categories turns intent in the typed words into preset searches: a venue
// type ("hot" -> Hotels, "mandapam" -> Marriage halls), an occasion ("sang"
// -> Sangeet venues), each within the top city when one matched. When the
// words are only a city, both types in it - "Hotels in Bengaluru" is what
// someone typing a city usually wants. Only categories with a venue in them.
func (h *Handler) categories(ctx context.Context, typed []string, city string) ([]suggestion, error) {
	types := map[string]bool{}
	var events []eventtypes.EventType
	for _, w := range typed {
		if len(w) < 3 {
			continue
		}
		// Either way round: "hot" is being typed, "hotels" is a plural.
		like := func(name string) bool { return strings.HasPrefix(name, w) || strings.HasPrefix(w, name) }
		if like("hotel") {
			types["HOTEL"] = true
		}
		if like("hall") || like("marriage") || venuesearch.Synonym(w, "hall") {
			types["MARRIAGE_HALL"] = true
		}
		for _, e := range eventtypes.Catalogue {
			if strings.HasPrefix(strings.ToLower(e.Name), w) || strings.HasPrefix(strings.ToLower(e.Code), w) {
				events = append(events, e)
			}
		}
	}
	if len(types) == 0 && len(events) == 0 && city != "" && onlyCity(typed, city) {
		types = map[string]bool{"MARRIAGE_HALL": true, "HOTEL": true}
	}
	in := ""
	if city != "" {
		in = " in " + city
	}

	var out []suggestion
	for _, t := range []string{"MARRIAGE_HALL", "HOTEL"} {
		if !types[t] {
			continue
		}
		var n int64
		if err := h.db.QueryRow(ctx, `SELECT count(*) FROM facilities f
			 WHERE `+venuetype.LiveSQL("f")+` AND f.type = $1 AND ($2 = '' OR f.city = $2)`,
			t, city).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, suggestion{Type: "CATEGORY", Label: typePlural[t] + in,
				VenueType: venuetype.API(t), City: city, Count: n})
		}
	}
	// A venue that declares no events hosts any, as the eventType filter says.
	for _, e := range events {
		var n int64
		if err := h.db.QueryRow(ctx, `SELECT count(*) FROM facilities f
			 WHERE `+venuetype.LiveSQL("f")+` AND ($2 = '' OR f.city = $2)
			   AND (EXISTS (SELECT 1 FROM facility_events fe WHERE fe.facility_id = f.id AND fe.event_code = $1)
			        OR NOT EXISTS (SELECT 1 FROM facility_events fe WHERE fe.facility_id = f.id))`,
			e.Code, city).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, suggestion{Type: "CATEGORY", Label: e.Name + " venues" + in,
				EventCode: e.Code, City: city, Count: n})
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out, nil
}

// correction is the typed text with each unknown word replaced by the
// closest word a live venue's name or city, or an amenity, actually uses -
// "parkng hal" -> "parking hall". Empty when nothing needed correcting.
// Words the search already knows - in that vocabulary, a synonym, or under
// three letters - are left alone.
//
// ponytail: the vocabulary is rebuilt per call, and only on a relaxed search.
// Past a few thousand venues, keep it in a table refreshed with the facility.
func (h *Handler) correction(ctx context.Context, text string) (string, error) {
	typed := venuesearch.Typed(text)
	var ask []string
	for _, w := range typed {
		if len(w) > 2 && !venuesearch.HasSynonyms(w) {
			ask = append(ask, w)
		}
	}
	if len(ask) == 0 {
		return "", nil
	}
	rows, err := h.db.Query(ctx, `
		WITH vocab AS (
		  SELECT DISTINCT w FROM facilities f,
		         regexp_split_to_table(lower(f.name||' '||COALESCE(f.city,'')), '[^[:alnum:]]+') w
		   WHERE `+venuetype.LiveSQL("f")+` AND length(w) > 2
		  UNION
		  SELECT DISTINCT w FROM amenities a, regexp_split_to_table(lower(a.name), '[^[:alnum:]]+') w
		   WHERE length(w) > 2)
		SELECT q.w, best.w FROM unnest($1::text[]) q(w)
		  JOIN LATERAL (
		    SELECT v.w FROM vocab v
		     WHERE NOT EXISTS (SELECT 1 FROM vocab x WHERE x.w = q.w)
		       AND similarity(v.w, q.w) >= 0.3
		     ORDER BY similarity(v.w, q.w) DESC, v.w LIMIT 1) best ON TRUE`, ask)
	if err != nil {
		return "", err
	}
	var from, to string
	fixed := map[string]string{}
	if _, err := pgx.ForEachRow(rows, []any{&from, &to}, func() error {
		fixed[from] = to
		return nil
	}); err != nil {
		return "", err
	}
	if len(fixed) == 0 {
		return "", nil
	}
	for i, w := range typed {
		if c, ok := fixed[w]; ok {
			typed[i] = c
		}
	}
	return strings.Join(typed, " "), nil
}

// onlyCity is true when every typed word is the start of the city's name or
// of an old name for it - "banga", "bangalore" - so the query names a place
// and nothing else. "grand palace bengaluru" names a venue; offering "Hotels
// in Bengaluru" above it would push aside what is being typed.
func onlyCity(typed []string, city string) bool {
	c := strings.ToLower(city)
	for _, w := range typed {
		if !strings.HasPrefix(c, w) && !slices.Contains(venuesearch.CitiesStartingWith(w), c) {
			return false
		}
	}
	return true
}
