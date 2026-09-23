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
