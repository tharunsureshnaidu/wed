package handler

import (
	"testing"
	"time"
)

// A client that forgets to URL-encode the cursor turns "+05:30" into " 05:30",
// because "+" means space in a query string. The handler repairs that rather
// than 400ing on a value it handed the client itself. This is the branch that
// decides, so it is the branch worth testing.
func TestCursorRepair(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		valid bool
	}{
		{"encoded correctly", "2026-09-25T19:18:05.859+05:30", true},
		{"plus eaten by query decoding", "2026-09-25T19:18:05.859 05:30", true},
		{"UTC needs no repair", "2026-09-25T19:18:05Z", true},
		{"negative offset untouched", "2026-09-25T19:18:05.859-05:00", true},
		{"genuine garbage still rejected", "not-a-date", false},
		{"trailing space is not an offset", "2026-09-25T19:18:05Z ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := repairCursor(tc.in)
			_, err := time.Parse(time.RFC3339, got)
			if tc.valid && err != nil {
				t.Fatalf("repairCursor(%q) = %q, still unparseable: %v", tc.in, got, err)
			}
			if !tc.valid && err == nil {
				t.Fatalf("repairCursor(%q) = %q, expected it to stay invalid", tc.in, got)
			}
		})
	}
}
