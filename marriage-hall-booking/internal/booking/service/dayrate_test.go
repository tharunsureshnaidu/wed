package service

import "testing"

// The booking must charge the number the listing card prints: the card rounds
// base x (100-pct)/100 to whole units, so the day rate does too.
func TestDayRateMatchesTheCard(t *testing.T) {
	pct := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		base float64
		pct  *float64
		want float64
	}{
		{100000, nil, 100000},
		{100000, pct(15), 85000},
		{99999, pct(15), 84999}, // 84999.15, printed as 84999
		{100000, pct(0), 100000},
	} {
		if got := DayRate(c.base, c.pct); got != c.want {
			t.Errorf("DayRate(%v, %v) = %v, want %v", c.base, c.pct, got, c.want)
		}
	}
}
