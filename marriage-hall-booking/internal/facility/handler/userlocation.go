package handler

import (
	"math"
	"net/http"
	"strconv"
)

// userLocation is the caller's live position, taken from ?lat= and ?lng=.
//
// Optional everywhere it is accepted: a browser can refuse the permission and a
// phone can fail to get a fix, and a venue list that 400s because the GPS was
// slow is worse than one without distances.
type userLocation struct {
	Lat, Lng float64
	OK       bool
}

// parseUserLocation reads the caller's position off the query string.
//
// Both values are required together - a lone latitude is a client bug, not half
// a location - and each must be in range. An out-of-range or unparseable value
// is treated as "no location" rather than an error: the rest of the response is
// still exactly what was asked for, and failing the whole request over a
// malformed optional parameter would take the screen down with it.
func parseUserLocation(r *http.Request) userLocation {
	q := r.URL.Query()
	rawLat, rawLng := q.Get("lat"), q.Get("lng")
	if rawLat == "" || rawLng == "" {
		return userLocation{}
	}
	lat, err1 := strconv.ParseFloat(rawLat, 64)
	lng, err2 := strconv.ParseFloat(rawLng, 64)
	if err1 != nil || err2 != nil {
		return userLocation{}
	}
	// Written as "in range" rather than "out of range": NaN fails every
	// comparison, so it slipped through the old form and turned into a
	// distanceKm the JSON encoder cannot write - a 200 with an empty body.
	if !(lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180) {
		return userLocation{}
	}
	// 0,0 is Null Island - the Atlantic. It is what an uninitialised location
	// object serialises to far more often than it is a real position, and
	// accepting it would put every Indian venue ~6000 km away.
	if lat == 0 && lng == 0 {
		return userLocation{}
	}
	return userLocation{Lat: lat, Lng: lng, OK: true}
}

// distanceFrom returns the straight-line km from the caller to a venue, or nil
// when either end has no coordinates.
//
// nil means "unknown", never 0: 35 of 52 facilities still have no lat/lng, and
// a guessed zero reads as "you are standing in the venue".
func (u userLocation) distanceFrom(lat, lng *float64) *float64 {
	if !u.OK || lat == nil || lng == nil {
		return nil
	}
	d := haversineKm(u.Lat, u.Lng, *lat, *lng)
	return &d
}

// haversineKm is the great-circle distance in kilometres, rounded to two
// decimals. Straight-line, not driving distance: there is no routing service
// here, and metre precision on a crow-flies estimate is false confidence.
func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKm = 6371.0
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLng := rad(lat2-lat1), rad(lng2-lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	km := earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return math.Round(km*100) / 100
}
