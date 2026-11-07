package handler

import (
	"net/url"
	"testing"
	"time"
)

func TestParseStay(t *testing.T) {
	today := time.Date(2026, 10, 16, 15, 0, 0, 0, time.Local)
	cases := []struct {
		query, start, end, slot, err string
	}{
		{"", "", "", "", ""},
		{"date=2026-12-12", "2026-12-12", "2026-12-12", "", ""},
		{"date=2026-10-16", "2026-10-16", "2026-10-16", "", ""}, // today is not the past
		{"startDate=2026-12-12&endDate=2026-12-14&slot=evening", "2026-12-12", "2026-12-14", "EVENING", ""},
		{"checkIn=2026-12-12&checkOut=2026-12-13", "2026-12-12", "2026-12-13", "", ""},
		{"checkIn=2026-12-12", "2026-12-12", "2026-12-12", "", ""},
		{"date=2026-10-15", "", "", "", "the date is in the past"},
		{"date=12-12-2026", "", "", "", "dates must be YYYY-MM-DD"},
		{"startDate=2026-12-12&endDate=2026-12-11", "", "", "", "the end date is before the start date"},
		{"startDate=2026-12-01&endDate=2027-01-01", "", "", "", "a date range can span at most 30 days"},
		{"endDate=2026-12-12", "", "", "", "an end date needs a start date"},
		{"checkOut=2026-12-12", "", "", "", "an end date needs a start date"},
		{"date=2026-12-12&slot=NIGHT", "", "", "", "slot must be MORNING, EVENING or FULL_DAY"},
	}
	for _, c := range cases {
		q, _ := url.ParseQuery(c.query)
		st, err := parseStay(q, today)
		got := func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		}
		if err != c.err || got(st.Start) != c.start || got(st.End) != c.end || st.Slot != c.slot {
			t.Errorf("%q: got (%q, %q, %q, err %q), want (%q, %q, %q, err %q)", c.query,
				got(st.Start), got(st.End), st.Slot, err, c.start, c.end, c.slot, c.err)
		}
	}
}
