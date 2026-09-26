// Package eventtypes is the catalogue of events a venue can host.
//
// Its own package because two modules need it: internal/facility declares what
// a venue hosts, and internal/booking validates what a customer books. Handler
// importing handler would be the wrong direction - see the layout rules in
// CLAUDE.md.
package eventtypes

import (
	"sort"
	"strings"
)

// EventType is one entry in the catalogue of events a venue can host.
//
// Hardcoded rather than a table: the list changes with a release, not at
// runtime, and a seeded table nobody edits is a migration pretending to be
// data. Adding one here is a one-line change.
type EventType struct {
	Code string `json:"code"`
	Name string `json:"name"`
	// Category groups the picker. Purely presentational - filtering is always
	// by code.
	Category string `json:"category"`
}

// Catalogue is the full list. Codes are UPPER_SNAKE and must never change
// once shipped: they are stored in facility_events and in bookings.event_type,
// so renaming one orphans every row that used it.
var Catalogue = []EventType{
	{"WEDDING", "Wedding", "WEDDING"},
	{"RECEPTION", "Reception", "WEDDING"},
	{"ENGAGEMENT", "Engagement", "WEDDING"},
	{"SANGEET", "Sangeet", "WEDDING"},
	{"MEHENDI", "Mehendi", "WEDDING"},
	{"HALDI", "Haldi", "WEDDING"},
	{"BRIDAL_SHOWER", "Bridal Shower", "WEDDING"},

	{"BIRTHDAY", "Birthday Party", "SOCIAL"},
	{"ANNIVERSARY", "Anniversary", "SOCIAL"},
	{"BABY_SHOWER", "Baby Shower", "SOCIAL"},
	{"NAMING_CEREMONY", "Naming Ceremony", "SOCIAL"},
	{"HOUSE_WARMING", "House Warming", "SOCIAL"},
	{"GET_TOGETHER", "Get-together", "SOCIAL"},
	{"FAREWELL", "Farewell", "SOCIAL"},

	{"CORPORATE_EVENT", "Corporate Event", "CORPORATE"},
	{"CONFERENCE", "Conference", "CORPORATE"},
	{"SEMINAR", "Seminar", "CORPORATE"},
	{"PRODUCT_LAUNCH", "Product Launch", "CORPORATE"},
	{"TRAINING", "Training / Workshop", "CORPORATE"},
	{"AWARD_CEREMONY", "Award Ceremony", "CORPORATE"},

	{"EXHIBITION", "Exhibition", "OTHER"},
	{"CONCERT", "Concert / Live Show", "OTHER"},
	{"RELIGIOUS", "Religious Ceremony", "OTHER"},
	{"PHOTOSHOOT", "Photoshoot", "OTHER"},
	// Kept because the booking screen has always offered it and bookings
	// already carry it. A venue never declares OTHER; it is what a customer
	// picks when nothing else fits.
	{"OTHER", "Other", "OTHER"},
}

// ByCode indexes the catalogue for validation. Built once: a linear scan
// per request over a list this size is fine, but the map also makes "is this a
// real code" a single lookup at every call site.
var ByCode = func() map[string]EventType {
	m := make(map[string]EventType, len(Catalogue))
	for _, e := range Catalogue {
		m[e.Code] = e
	}
	return m
}()

// ValidEventCode reports whether a code is in the catalogue. Exported so the
// booking handler can validate without importing the whole catalogue.
func ValidEventCode(code string) bool {
	_, ok := ByCode[strings.ToUpper(strings.TrimSpace(code))]
	return ok
}

// EventName returns the display name for a code, falling back to the code
// itself. A venue may hold a code from an older release; showing the raw code
// is better than showing nothing.
func EventName(code string) string {
	if e, ok := ByCode[strings.ToUpper(strings.TrimSpace(code))]; ok {
		return e.Name
	}
	return code
}

// Views turns stored codes into the same {code,name,category} shape the
// catalogue returns, so a client renders both with one component.
func Views(codes []string) []EventType {
	out := make([]EventType, 0, len(codes))
	for _, c := range codes {
		if e, ok := ByCode[c]; ok {
			out = append(out, e)
			continue
		}
		// A code from an older release: show it rather than dropping it.
		out = append(out, EventType{Code: c, Name: c, Category: "OTHER"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
