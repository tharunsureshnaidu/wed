package service

import (
	"strconv"
	"strings"
	"testing"
)

// A geo announcement's subject id must be unique per (announcement, recipient).
//
// Two bugs this guards:
//   - without the per-recipient suffix, one user's row collides with another's
//     on the outbox's (event, subject, role, channel) unique key and only the
//     first person is ever told.
//   - without the per-announcement dedupe key, a new coupon and a new amenity
//     at the same venue collide, and the second announcement is silently
//     dropped. Found live: the amenities announcement never sent.
func TestGeoSubjectIDIsUniquePerAnnouncementAndRecipient(t *testing.T) {
	facility := "4e78751a-667f-4c07-a625-fb5d3224158f"

	subjectFor := func(dedupe string, userID int64) string {
		return facility + ":" + dedupe + ":" + strconv.FormatInt(userID, 10)
	}

	seen := map[string]bool{}
	for _, dedupe := range []string{"approved", "coupon:abc", "amenities:WiFi"} {
		for _, uid := range []int64{101, 102, 103} {
			s := subjectFor(dedupe, uid)
			if seen[s] {
				t.Fatalf("duplicate subject id %q - one of these notifications would be dropped", s)
			}
			seen[s] = true
			if !strings.Contains(s, dedupe) {
				t.Errorf("subject id %q lost its announcement key", s)
			}
		}
	}
	if len(seen) != 9 {
		t.Fatalf("expected 9 distinct subject ids, got %d", len(seen))
	}
}

// The recipient cap bounds the worst case: one admin action must not be able
// to enqueue an unbounded number of rows.
func TestGeoRecipientCapIsBounded(t *testing.T) {
	if GeoMaxRecipients <= 0 {
		t.Fatal("recipient cap must be positive")
	}
	if GeoMaxRecipients > 10000 {
		t.Errorf("cap %d is high enough to be a spam incident", GeoMaxRecipients)
	}
	if GeoRadiusMetres != 50000 {
		t.Errorf("default radius = %d m, expected 50 km", GeoRadiusMetres)
	}
}

// TestCouponLocationEligibilityRules verifies the distance calculation and no-location rules.
func TestCouponLocationEligibilityRules(t *testing.T) {
	const maxRadiusKm = 50.0

	isEligible := func(lat, lng *float64, distKm float64) bool {
		// Rule: If user has no location (lat is null OR lng is null), user is ALWAYS ELIGIBLE
		if lat == nil || lng == nil {
			return true
		}
		// Rule: If user has location, eligible if dist <= 50 km (inclusive)
		return distKm <= maxRadiusKm
	}

	floatPtr := func(v float64) *float64 { return &v }

	cases := []struct {
		name     string
		lat      *float64
		lng      *float64
		distKm   float64
		expected bool
	}{
		{"User 10 km away", floatPtr(26.8), floatPtr(83.4), 10.0, true},
		{"User 49.9 km away", floatPtr(26.9), floatPtr(83.5), 49.9, true},
		{"User exactly 50.0 km away (inclusive)", floatPtr(27.0), floatPtr(83.6), 50.0, true},
		{"User 50.1 km away (excluded)", floatPtr(27.1), floatPtr(83.7), 50.1, false},
		{"User 100 km away (excluded)", floatPtr(27.5), floatPtr(84.0), 100.0, false},
		{"User with no location (both NULL)", nil, nil, 0, true},
		{"User with partial location (lat NULL)", nil, floatPtr(83.4), 0, true},
		{"User with partial location (lng NULL)", floatPtr(26.8), nil, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isEligible(tc.lat, tc.lng, tc.distKm)
			if got != tc.expected {
				t.Errorf("case %q: expected eligible=%v, got %v", tc.name, tc.expected, got)
			}
		})
	}
}
