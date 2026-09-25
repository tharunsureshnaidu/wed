package handler

import (
	"math"
	"testing"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
)

func ptr[T any](v T) *T { return &v }

func TestParseCompareIDs(t *testing.T) {
	a := "11111111-1111-1111-1111-111111111111"
	b := "22222222-2222-2222-2222-222222222222"
	c := "33333333-3333-3333-3333-333333333333"
	d := "44444444-4444-4444-4444-444444444444"

	for _, tc := range []struct {
		name, in string
		want     int
		wantErr  bool
	}{
		{"two ids", a + "," + b, 2, false},
		{"three ids", a + "," + b + "," + c, 3, false},
		{"whitespace tolerated", a + " , " + b, 2, false},
		{"one id is not a comparison", a, 0, true},
		{"four exceeds the cap", a + "," + b + "," + c + "," + d, 0, true},
		{"a venue cannot face itself", a + "," + a, 0, true},
		{"non-uuid rejected before SQL", a + ",not-a-uuid", 0, true},
		{"empty", "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids, msg := parseCompareIDs(tc.in)
			if tc.wantErr {
				if msg == "" {
					t.Fatalf("parseCompareIDs(%q) accepted it, wanted a rejection", tc.in)
				}
				return
			}
			if msg != "" {
				t.Fatalf("parseCompareIDs(%q) rejected it: %s", tc.in, msg)
			}
			if len(ids) != tc.want {
				t.Fatalf("got %d ids, want %d", len(ids), tc.want)
			}
		})
	}
}

// A hotel quotes per night and a hall per event day. Reporting those as one
// comparable number would make a 4,500 hotel look 10x cheaper than a 50,000
// hall for no reason a customer would recognise.
func TestPriceUnitsAreNotNormalised(t *testing.T) {
	hotel := repository.Facility{Type: "HOTEL", StartingPrice: ptr(4500.0)}
	hall := repository.Facility{Type: "MARRIAGE_HALL", StartingPrice: ptr(50000.0)}

	if got := toCompareVenue(hotel).Price.Unit; got != unitPerNight {
		t.Errorf("hotel unit = %q, want %q", got, unitPerNight)
	}
	if got := toCompareVenue(hall).Price.Unit; got != unitPerEventDay {
		t.Errorf("hall unit = %q, want %q", got, unitPerEventDay)
	}
	if comparablePrice([]repository.Facility{hotel, hall}) {
		t.Error("a hotel and a hall were reported as having comparable prices")
	}
	if !comparablePrice([]repository.Facility{hall, hall}) {
		t.Error("two halls were reported as not comparable")
	}
	if !mixedTypes([]repository.Facility{hotel, hall}) {
		t.Error("mixedTypes missed a hotel next to a hall")
	}
}

// A venue that publishes no price must say so, not report zero: a missing price
// is not a free venue.
func TestMissingAttributesAreReportedNotDefaulted(t *testing.T) {
	bare := toCompareVenue(repository.Facility{Type: "HOTEL"})
	want := map[string]bool{"startingPrice": true, "capacity": true,
		"areaSqft": true, "coverImage": true, "location": true}
	for _, m := range bare.Missing {
		delete(want, m)
	}
	if len(want) > 0 {
		t.Fatalf("these missing attributes were not reported: %v", want)
	}
	if bare.Price.StartingPrice != nil {
		t.Error("an absent price was materialised into a number")
	}

	full := toCompareVenue(repository.Facility{
		Type: "HOTEL", StartingPrice: ptr(4500.0), CapacityPax: ptr(300),
		AreaSqft: ptr(3000), Lat: ptr(12.9), Lng: ptr(77.5),
		Images: []repository.Image{{URL: "https://example.com/a.jpg"}},
	})
	if len(full.Missing) != 0 {
		t.Fatalf("a fully populated venue reported missing: %v", full.Missing)
	}
}

func TestAmenityMatrix(t *testing.T) {
	mk := func(codes ...string) repository.Facility {
		f := repository.Facility{Type: "HOTEL"}
		for _, c := range codes {
			f.Amenities = append(f.Amenities, repository.Amenity{Name: c, Code: ptr(c)})
		}
		return f
	}
	rows := amenityMatrix([]repository.Facility{mk("AC", "PARKING"), mk("AC", "POOL")})

	got := map[string]compareAmenityRow{}
	for _, r := range rows {
		got[r.Code] = r
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (the union of both venues)", len(rows))
	}
	if !got["AC"].AllHave {
		t.Error("AC is on both venues but allHave is false")
	}
	if got["PARKING"].AllHave || !got["PARKING"].Present[0] || got["PARKING"].Present[1] {
		t.Errorf("PARKING should be on venue 0 only, got %v", got["PARKING"].Present)
	}
	// Ordering must be stable, or the table reshuffles on every refresh.
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Name > rows[i].Name {
			t.Fatalf("rows are not sorted by name: %q before %q", rows[i-1].Name, rows[i].Name)
		}
	}
}

// An amenity whose code is NULL must not collapse into one row with every
// other code-less amenity.
func TestAmenityMatrixFallsBackToName(t *testing.T) {
	f := repository.Facility{Type: "HOTEL", Amenities: []repository.Amenity{
		{Name: "Valet"}, {Name: "Spa"},
	}}
	if rows := amenityMatrix([]repository.Facility{f}); len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 - code-less amenities collapsed", len(rows))
	}
}

func TestDistanceMatrix(t *testing.T) {
	blr := repository.Facility{Type: "HOTEL", Lat: ptr(12.97), Lng: ptr(77.59)}
	mys := repository.Facility{Type: "HOTEL", Lat: ptr(12.30), Lng: ptr(76.64)}
	noloc := repository.Facility{Type: "HOTEL"}

	m := distanceMatrix([]repository.Facility{blr, mys, noloc})
	if *m[0][0] != 0 {
		t.Error("a venue is not zero km from itself")
	}
	// Bengaluru to Mysuru is ~125 km straight line.
	if d := *m[0][1]; math.Abs(d-125) > 15 {
		t.Errorf("BLR->MYS = %.2f km, expected roughly 125", d)
	}
	if *m[0][1] != *m[1][0] {
		t.Error("distance is not symmetric")
	}
	// A venue with no coordinates yields nil, never a guessed 0 - which would
	// read as "same location".
	if m[0][2] != nil || m[2][0] != nil {
		t.Error("distance to a venue with no coordinates should be nil")
	}
}
