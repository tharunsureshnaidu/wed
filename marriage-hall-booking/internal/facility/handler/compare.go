package handler

import (
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/tripfcatory/marriage-hall-booking/internal/facility/repository"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
)

// maxCompare matches the "SELECT VENUES TO COMPARE (MAX 3)" cap on the screen.
// Enforced server-side as well: an unbounded id list is a cheap way to pull the
// whole table in one request.
const maxCompare = 3

// Price units. A hotel quotes per night and a hall per event day, and the two
// are not the same quantity - three nights in a hotel is not a wedding.
//
// The API reports the unit and refuses to normalise. Dividing an event-day rate
// into a nightly one would make one venue look an order of magnitude cheaper
// for no reason a customer would recognise.
const (
	unitPerNight    = "PER_NIGHT"
	unitPerEventDay = "PER_EVENT_DAY"
)

type comparePrice struct {
	StartingPrice   *float64 `json:"startingPrice"`
	DiscountedPrice *float64 `json:"discountedPrice"`
	DiscountPercent *float64 `json:"discountPercent"`
	DiscountLabel   *string  `json:"discountLabel"`
	HasDiscount     bool     `json:"hasDiscount"`
	// Unit is what the number means, and Display is the label the card prints
	// under it ("per night" / "per event day").
	Unit    string `json:"unit"`
	Display string `json:"unitLabel"`
}

type compareVenue struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	City       *string `json:"city"`
	Address    *string `json:"fullAddress"`
	CoverImage *string `json:"coverImage"`

	AvgRating   float64 `json:"avgRating"`
	ReviewCount int     `json:"reviewCount"`
	StarRating  *int    `json:"starRating"`

	Price comparePrice `json:"price"`

	CapacityPax      *int `json:"capacityPax"`
	SeatingCapacity  *int `json:"seatingCapacity"`
	FloatingCapacity *int `json:"floatingCapacity"`
	AreaSqft         *int `json:"areaSqft"`

	Lat *float64 `json:"lat"`
	Lng *float64 `json:"lng"`

	// AmenityCodes is what the matrix is built from; the full objects stay out
	// of the per-venue block so the same amenity is not repeated three times.
	AmenityCodes []string `json:"amenityCodes"`

	// Missing names the attributes this venue does not publish, so the client
	// can render a dash instead of a zero. Half the hotels have no price and
	// 16 of 43 halls have no capacity - this is the common case, not an edge.
	Missing []string `json:"missing"`
}

// compareAmenityRow is one line of the comparison matrix: an amenity and whether each
// venue has it, in the same order as the venues array.
type compareAmenityRow struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Present []bool `json:"present"`
	// AllHave and NoneHave let the client collapse the rows that do not
	// differentiate - the point of the screen is the differences.
	AllHave  bool `json:"allHave"`
	NoneHave bool `json:"noneHave"`
}

// compare is GET /api/v1/facilities/compare?ids=a,b,c
//
// Public, like the rest of the facility reads: comparing venues is something a
// visitor does before signing up.
func (h *Handler) compare(w http.ResponseWriter, r *http.Request) {
	ids, errMsg := parseCompareIDs(r.URL.Query().Get("ids"))
	if errMsg != "" {
		response.Error(w, http.StatusBadRequest, errMsg, "VALIDATION_ERROR")
		return
	}

	found, err := h.repo.ByIDs(r.Context(), ids)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// Every requested id must resolve. Silently comparing two venues when three
	// were asked for would leave the client rendering a column it has no data
	// for, with no way to tell which id was wrong.
	if len(found) != len(ids) {
		response.Error(w, http.StatusNotFound,
			"One or more venues could not be found", "FACILITY_NOT_FOUND")
		return
	}

	venues := make([]compareVenue, 0, len(found))
	for _, f := range found {
		venues = append(venues, toCompareVenue(f))
	}

	response.OK(w, "Venues compared successfully", map[string]any{
		"venues":    venues,
		"amenities": amenityMatrix(found),
		// mixedTypes tells the client to show the "Hotel vs Marriage Hall Mode"
		// banner and to stop treating the two prices as one column.
		"mixedTypes":      mixedTypes(found),
		"distanceKm":      distanceMatrix(found),
		"comparablePrice": comparablePrice(found),
	})
}

// parseCompareIDs validates the id list before any of it reaches SQL.
//
// Duplicates are rejected rather than silently collapsed: comparing a venue
// with itself is a client bug, and returning two identical columns hides it.
func parseCompareIDs(raw string) ([]string, string) {
	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !httpx.ValidUUID(p) {
			return nil, "Each id must be a valid UUID"
		}
		if seen[p] {
			return nil, "The same venue cannot be compared with itself"
		}
		seen[p] = true
		ids = append(ids, p)
	}
	switch {
	case len(ids) < 2:
		return nil, "At least 2 venue ids are required to compare"
	case len(ids) > maxCompare:
		return nil, "At most 3 venues can be compared at once"
	}
	return ids, ""
}

func toCompareVenue(f repository.Facility) compareVenue {
	v := compareVenue{
		ID: f.ID, Name: f.Name, Type: f.Type, City: f.City, Address: f.FullAddress,
		AvgRating: f.AvgRating, ReviewCount: f.ReviewCount, StarRating: f.StarRating,
		CapacityPax: f.CapacityPax, SeatingCapacity: f.SeatingCapacity,
		FloatingCapacity: f.FloatingCapacity, AreaSqft: f.AreaSqft,
		Lat: f.Lat, Lng: f.Lng,
		Price: comparePrice{
			StartingPrice: f.StartingPrice, DiscountedPrice: f.DiscountedPrice,
			DiscountPercent: f.DiscountPercent, DiscountLabel: f.DiscountLabel,
			HasDiscount: f.HasDiscount,
			Unit:        priceUnit(f.Type), Display: priceLabel(f.Type),
		},
		AmenityCodes: []string{},
		Missing:      []string{},
	}
	for _, a := range f.Images {
		if a.URL != "" {
			url := a.URL
			v.CoverImage = &url
			break
		}
	}
	for _, a := range f.Amenities {
		v.AmenityCodes = append(v.AmenityCodes, amenityKey(a))
	}

	// What this venue does not publish. Reported rather than defaulted: a
	// missing price is not a free venue, and a missing capacity is not zero
	// guests.
	if f.StartingPrice == nil {
		v.Missing = append(v.Missing, "startingPrice")
	}
	if f.CapacityPax == nil && f.SeatingCapacity == nil && f.FloatingCapacity == nil {
		v.Missing = append(v.Missing, "capacity")
	}
	if f.AreaSqft == nil {
		v.Missing = append(v.Missing, "areaSqft")
	}
	if v.CoverImage == nil {
		v.Missing = append(v.Missing, "coverImage")
	}
	if f.Lat == nil || f.Lng == nil {
		v.Missing = append(v.Missing, "location")
	}
	return v
}

func priceUnit(t string) string {
	if t == "HOTEL" {
		return unitPerNight
	}
	return unitPerEventDay
}

func priceLabel(t string) string {
	if t == "HOTEL" {
		return "per night"
	}
	return "per event day"
}

// amenityKey prefers the code, which is stable, and falls back to the name for
// rows where code is NULL. Without the fallback those amenities would all share
// an empty key and collapse into one row.
func amenityKey(a repository.Amenity) string {
	if a.Code != nil && *a.Code != "" {
		return *a.Code
	}
	return a.Name
}

// amenityMatrix is the union of every venue's amenities, each with a per-venue
// present flag in the venues' order.
func amenityMatrix(fs []repository.Facility) []compareAmenityRow {
	names := map[string]string{}
	has := map[string]map[int]bool{}
	for i, f := range fs {
		for _, a := range f.Amenities {
			k := amenityKey(a)
			names[k] = a.Name
			if has[k] == nil {
				has[k] = map[int]bool{}
			}
			has[k][i] = true
		}
	}

	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	// Sorted by display name so the matrix is stable between calls; a map's
	// iteration order would reshuffle the table on every refresh.
	sort.Slice(keys, func(i, j int) bool { return names[keys[i]] < names[keys[j]] })

	rows := make([]compareAmenityRow, 0, len(keys))
	for _, k := range keys {
		row := compareAmenityRow{Code: k, Name: names[k], Present: make([]bool, len(fs)), AllHave: true}
		count := 0
		for i := range fs {
			if has[k][i] {
				row.Present[i] = true
				count++
			} else {
				row.AllHave = false
			}
		}
		row.NoneHave = count == 0
		rows = append(rows, row)
	}
	return rows
}

func mixedTypes(fs []repository.Facility) bool {
	for i := 1; i < len(fs); i++ {
		if fs[i].Type != fs[0].Type {
			return true
		}
	}
	return false
}

// comparablePrice says whether the price column is like-for-like. False when
// the venues quote in different units, so the client can label the column
// rather than let a customer read 12,500 as cheaper than 185,000.
func comparablePrice(fs []repository.Facility) bool {
	for i := 1; i < len(fs); i++ {
		if priceUnit(fs[i].Type) != priceUnit(fs[0].Type) {
			return false
		}
	}
	return true
}

// distanceMatrix is the straight-line distance in km between each pair, in the
// venues' order. nil for a pair where either venue has no coordinates - 35 of
// 52 facilities still have none, and a guessed distance is worse than none.
func distanceMatrix(fs []repository.Facility) [][]*float64 {
	out := make([][]*float64, len(fs))
	for i := range fs {
		out[i] = make([]*float64, len(fs))
		for j := range fs {
			if i == j {
				zero := 0.0
				out[i][j] = &zero
				continue
			}
			if fs[i].Lat == nil || fs[i].Lng == nil || fs[j].Lat == nil || fs[j].Lng == nil {
				continue
			}
			d := haversineKm(*fs[i].Lat, *fs[i].Lng, *fs[j].Lat, *fs[j].Lng)
			out[i][j] = &d
		}
	}
	return out
}

// haversineKm is great-circle distance. Computed in Go rather than by the
// earthdistance extension because the coordinates are already loaded - a round
// trip to Postgres to subtract two numbers would be the slower option.
func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKm = 6371.0
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLng := rad(lat2-lat1), rad(lng2-lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	km := earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	// Two decimals: metre precision on a straight-line estimate between two
	// venue pins is false confidence.
	return math.Round(km*100) / 100
}
