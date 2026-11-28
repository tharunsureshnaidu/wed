package handler

import (
	"testing"
	"time"
)

func TestBareDateIsAWholeISTDay(t *testing.T) {
	from, _ := parseWhen("2026-10-10", false)
	until, _ := parseWhen("2026-10-10", true)
	if want := time.Date(2026, 10, 9, 18, 30, 0, 0, time.UTC); !from.Equal(want) {
		t.Fatalf("from = %v, want %v", from.UTC(), want)
	}
	// 23:59 IST on the 10th is still inside the campaign.
	if late := time.Date(2026, 10, 10, 18, 29, 0, 0, time.UTC); until.Before(late) {
		t.Fatalf("until %v ends before 23:59 IST", until.UTC())
	}
	if !until.After(from) {
		t.Fatal("a one-day campaign must have until after from")
	}
	if _, err := parseWhen("10/10/2026", true); err == nil {
		t.Fatal("garbage date accepted")
	}
}
