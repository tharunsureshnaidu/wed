package handler

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/eventtypes"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

// facets is what the filter sheet draws: how many venues each choice would
// leave, and the price range a slider can span.
type facets struct {
	Types      []facetCount `json:"types"`
	EventTypes []facetCount `json:"eventTypes"`
	Amenities  []facetCount `json:"amenities"`
	Price      priceRange   `json:"price"`
}

type facetCount struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// priceRange is nil at either end when no matching venue publishes a price.
type priceRange struct {
	Min *float64 `json:"min"`
	Max *float64 `json:"max"`
}

var typeNames = map[string]string{"HALL": "Marriage hall", "HOTEL": "Hotel"}

// facets counts over the same search - text, filters, dates and distance -
// with one exception per facet: a single-choice facet ignores its own filter.
// With HOTEL chosen the type facet still says how many halls there are, and
// the price range ignores the budget, or picking a budget would shrink the
// slider to it. Amenities combine (a venue must have all), so their counts
// keep every filter: each says what adding that amenity would leave.
//
// args are the search's 16 WHERE parameters; each query gets its own copy.
func (h *Handler) facets(ctx context.Context, where string, args []any) (facets, error) {
	var out facets
	without := func(pos int, zero any, extra ...any) []any {
		a := append(append([]any{}, args...), extra...)
		if pos > 0 {
			a[pos-1] = zero
		}
		return a
	}

	codeCount := func(row pgx.CollectableRow) (c facetCount, err error) {
		return c, row.Scan(&c.Code, &c.Count)
	}

	rows, err := h.db.Query(ctx, `SELECT f.type, count(*) FROM facilities f`+where+
		` GROUP BY f.type ORDER BY 2 DESC`, without(3, "")...)
	if err != nil {
		return out, err
	}
	if out.Types, err = pgx.CollectRows(rows, codeCount); err != nil {
		return out, err
	}
	for i := range out.Types {
		out.Types[i].Code = venuetype.API(out.Types[i].Code)
		out.Types[i].Name = typeNames[out.Types[i].Code]
	}

	// A venue that declares no events matches every event filter, so it counts
	// toward every event here too - or the facet would say 3 and the filter
	// return 40.
	codes := make([]string, len(eventtypes.Catalogue))
	for i, e := range eventtypes.Catalogue {
		codes[i] = e.Code
	}
	rows, err = h.db.Query(ctx, `SELECT e.code, count(*) FROM facilities f
		CROSS JOIN unnest($17::text[]) e(code)`+where+`
		  AND (EXISTS (SELECT 1 FROM facility_events fe WHERE fe.facility_id = f.id AND fe.event_code = e.code)
		       OR NOT EXISTS (SELECT 1 FROM facility_events fe WHERE fe.facility_id = f.id))
		GROUP BY e.code ORDER BY 2 DESC, 1`, without(9, "", codes)...)
	if err != nil {
		return out, err
	}
	if out.EventTypes, err = pgx.CollectRows(rows, codeCount); err != nil {
		return out, err
	}
	for i := range out.EventTypes {
		out.EventTypes[i].Name = eventtypes.EventName(out.EventTypes[i].Code)
	}

	// Code falls back to the name, as the amenity filter matches either.
	rows, err = h.db.Query(ctx, `SELECT COALESCE(a.code, a.name), a.name, count(*) FROM facilities f
		JOIN facility_amenities fa ON fa.facility_id = f.id
		JOIN amenities a ON a.id = fa.amenity_id`+where+`
		GROUP BY a.code, a.name ORDER BY 3 DESC, 2`, args...)
	if err != nil {
		return out, err
	}
	if out.Amenities, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (c facetCount, err error) {
		return c, row.Scan(&c.Code, &c.Name, &c.Count)
	}); err != nil {
		return out, err
	}

	priceArgs := without(6, 0.0)
	priceArgs[6] = 0.0 // $7, maxBudget
	err = h.db.QueryRow(ctx, `SELECT min(f.base_price_per_day)::float8, max(f.base_price_per_day)::float8
		FROM facilities f`+where, priceArgs...).Scan(&out.Price.Min, &out.Price.Max)
	return out, err
}
