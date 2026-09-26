// Package venuetype translates the facility type between what the database
// stores and what the API speaks.
//
// The column holds MARRIAGE_HALL (153 rows and a CHECK constraint), while
// bookings.target_type has always said HALL. A client reading a booking saw
// "targetType": "HALL" next to a nested "type": "MARRIAGE_HALL" for the same
// venue - two words for one thing, which is the bug this package removes.
//
// The API speaks HALL. The column is left alone: renaming the stored value
// would break every client and script still sending the old word, for a
// difference no user can see.
package venuetype

// The two vocabularies. Stored is what is in facilities.type; the API constant
// is what goes on the wire.
const (
	StoredHall = "MARRIAGE_HALL"
	Hall       = "HALL"
	Hotel      = "HOTEL"
)

// API converts a stored type to what the API returns. Anything else - HOTEL,
// or a value added later - passes through untouched, so a new facility type
// does not need a change here to be serialised correctly.
func API(stored string) string {
	if stored == StoredHall {
		return Hall
	}
	return stored
}

// Stored converts a type from a request into the value the column holds.
// HALL and MARRIAGE_HALL both map to the stored value: clients already send
// the long form in saved requests and import scripts, and rejecting it would
// break them for no gain.
func Stored(api string) string {
	if api == Hall || api == StoredHall {
		return StoredHall
	}
	return api
}

// Valid reports whether a type from a request names a real facility type.
func Valid(api string) bool {
	return api == Hall || api == StoredHall || api == Hotel
}
