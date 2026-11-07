package handler

import (
	"net/url"
	"strings"
	"time"
)

// maxSearchDays bounds a date-range search; each day is a check per venue.
const maxSearchDays = 30

// stay is the dates a search asks to be free. Start is nil when the search
// has no date filter.
type stay struct {
	Start, End *string // YYYY-MM-DD, bound as $13/$14
	Slot       string  // "" (any), MORNING, EVENING or FULL_DAY - halls only
}

// parseStay reads ?date=, ?startDate=&endDate= or ?checkIn=&checkOut= - the
// same words the booking routes take - and ?slot=. A bad value is an error,
// not "no filter": a mistyped date silently searching every date reads as
// "all of these are free".
func parseStay(q url.Values, today time.Time) (stay, string) {
	start, end := q.Get("date"), ""
	for _, pair := range [][2]string{{"startDate", "endDate"}, {"checkIn", "checkOut"}} {
		if start == "" && end == "" {
			start, end = q.Get(pair[0]), q.Get(pair[1])
		}
	}
	slot := strings.ToUpper(strings.TrimSpace(q.Get("slot")))
	switch slot {
	case "", "MORNING", "EVENING", "FULL_DAY":
	default:
		return stay{}, "slot must be MORNING, EVENING or FULL_DAY"
	}
	if start == "" {
		if end != "" {
			return stay{}, "an end date needs a start date"
		}
		return stay{}, ""
	}
	if end == "" {
		end = start
	}
	s, err1 := time.Parse(time.DateOnly, start)
	e, err2 := time.Parse(time.DateOnly, end)
	switch {
	case err1 != nil || err2 != nil:
		return stay{}, "dates must be YYYY-MM-DD"
	case start < today.Format(time.DateOnly): // ISO dates order as strings
		return stay{}, "the date is in the past"
	case e.Before(s):
		return stay{}, "the end date is before the start date"
	case e.Sub(s) > maxSearchDays*24*time.Hour:
		return stay{}, "a date range can span at most 30 days"
	}
	return stay{Start: &start, End: &end, Slot: slot}, ""
}

// availableSQL is true when venue f is free over $13..$14 - the same rules
// the booking routes enforce when they claim it, so a venue a date search
// shows is one that can be booked then:
//
//   - A hall, on every day: with no slot asked for, at least one half is free
//     (no FULL_DAY taken, not both halves); FULL_DAY needs the whole day; a
//     half needs that half and no FULL_DAY. An owner's BLOCKED counts as taken.
//     A day with no row has never been claimed, so it is free.
//   - A hotel, every night of [$13, $14) - or the one night $13 when the dates
//     are equal: room types whose free rooms across all nights add up to the
//     $16 rooms asked for. A night with no row is at the room type's full
//     capacity, as booking materialises it. A hotel with no room types at all
//     is shown: not stated is not "full".
const availableSQL = `($13::date IS NULL OR CASE WHEN f.type = 'HOTEL' THEN (
		  NOT EXISTS (SELECT 1 FROM room_types rt WHERE rt.facility_id = f.id AND rt.is_deleted IS NOT TRUE)
		  OR (SELECT COALESCE(sum(free), 0) FROM (
		        SELECT min(COALESCE(ra.total_rooms - ra.booked_rooms, rt.total_rooms)) AS free
		          FROM room_types rt
		          CROSS JOIN generate_series($13::date, GREATEST($14::date, $13::date + 1) - 1, interval '1 day') d(day)
		          LEFT JOIN room_availability ra ON ra.room_type_id = rt.id AND ra.date = d.day::date
		         WHERE rt.facility_id = f.id AND rt.is_deleted IS NOT TRUE
		         GROUP BY rt.id) per_type) >= $16::int)
		ELSE NOT EXISTS (
		  SELECT 1 FROM generate_series($13::date, $14::date, interval '1 day') d(day)
		   WHERE (SELECT CASE $15::text
		            WHEN '' THEN bool_or(ha.slot_type = 'FULL_DAY')
		                      OR (bool_or(ha.slot_type = 'MORNING') AND bool_or(ha.slot_type = 'EVENING'))
		            WHEN 'FULL_DAY' THEN count(*) > 0
		            ELSE bool_or(ha.slot_type IN ($15::text, 'FULL_DAY')) END
		            FROM hall_availability ha
		           WHERE ha.facility_id = f.id AND ha.date = d.day::date
		             AND ha.status IN ('BOOKED', 'BLOCKED')))
		END)`
