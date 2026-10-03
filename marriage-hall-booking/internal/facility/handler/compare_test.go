package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
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

type handlerMockRepo struct {
	byIDsFunc func(ctx context.Context, ids []string) ([]repository.Facility, error)
}

func (m *handlerMockRepo) ByIDs(ctx context.Context, ids []string) ([]repository.Facility, error) {
	if m.byIDsFunc != nil {
		return m.byIDsFunc(ctx, ids)
	}
	return nil, nil
}
func (m *handlerMockRepo) RoomTypesOfMany(ctx context.Context, ids []string) (map[string][]repository.RoomType, error) {
	return map[string][]repository.RoomType{}, nil
}
func (m *handlerMockRepo) PackagesOfMany(ctx context.Context, ids []string) (map[string][]repository.HallPackage, error) {
	return map[string][]repository.HallPackage{}, nil
}
func (m *handlerMockRepo) AddonsOfMany(ctx context.Context, ids []string) (map[string][]repository.AddonService, error) {
	return map[string][]repository.AddonService{}, nil
}
func (m *handlerMockRepo) AllImagesOfMany(ctx context.Context, ids []string) (map[string][]repository.Image, error) {
	return map[string][]repository.Image{}, nil
}
func (m *handlerMockRepo) ReviewsOfMany(ctx context.Context, ids []string) (map[string][]repository.Review, error) {
	return map[string][]repository.Review{}, nil
}

func setupTestMux(mock *handlerMockRepo) *http.ServeMux {
	signer, _ := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	h := &Handler{
		signer:     signer,
		compareSvc: service.NewCompareService(mock),
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestCompareVenuesAPI(t *testing.T) {
	h1 := "11111111-1111-1111-1111-111111111111"
	h2 := "22222222-2222-2222-2222-222222222222"
	h3 := "33333333-3333-3333-3333-333333333333"

	m1 := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	m2 := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	m3 := "cccccccc-cccc-cccc-cccc-cccccccccccc"

	mock := &handlerMockRepo{
		byIDsFunc: func(ctx context.Context, ids []string) ([]repository.Facility, error) {
			var out []repository.Facility
			for _, id := range ids {
				switch id {
				case h1:
					out = append(out, repository.Facility{ID: h1, Name: "Grand Hotel", Type: "HOTEL", City: ptr("Lucknow"), StartingPrice: ptr(4000.0)})
				case h2:
					out = append(out, repository.Facility{ID: h2, Name: "Palace Hotel", Type: "HOTEL", City: ptr("Kanpur"), StartingPrice: ptr(5000.0)})
				case h3:
					out = append(out, repository.Facility{ID: h3, Name: "Resort Hotel", Type: "HOTEL", City: ptr("Varanasi"), StartingPrice: ptr(6000.0)})
				case m1:
					out = append(out, repository.Facility{ID: m1, Name: "Royal Hall", Type: "MARRIAGE_HALL", City: ptr("Lucknow"), CapacityPax: ptr(400), BasePricePerDay: ptr(50000.0)})
				case m2:
					out = append(out, repository.Facility{ID: m2, Name: "Diamond Hall", Type: "MARRIAGE_HALL", City: ptr("Kanpur"), CapacityPax: ptr(600), BasePricePerDay: ptr(70000.0)})
				case m3:
					out = append(out, repository.Facility{ID: m3, Name: "Pearl Hall", Type: "MARRIAGE_HALL", City: ptr("Agra"), CapacityPax: ptr(800), BasePricePerDay: ptr(90000.0)})
				}
			}
			return out, nil
		},
	}

	mux := setupTestMux(mock)

	t.Run("POST /api/v1/facilities/compare - 2 Hotels Success", func(t *testing.T) {
		body := map[string]any{
			"type":      "hotel",
			"venue_ids": []string{h1, h2},
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/facilities/compare", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
		}

		var res struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
			Data    struct {
				Type   string `json:"type"`
				Total  int    `json:"total"`
				Venues []struct {
					ID       string  `json:"id"`
					Name     string  `json:"name"`
					Type     string  `json:"type"`
					Price    float64 `json:"price"`
					Location string  `json:"location"`
				} `json:"venues"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !res.Success || res.Data.Type != "hotel" || res.Data.Total != 2 {
			t.Errorf("unexpected response shape: %+v", res)
		}
		if len(res.Data.Venues) != 2 || res.Data.Venues[0].ID != h1 || res.Data.Venues[1].ID != h2 {
			t.Errorf("unexpected venues returned: %+v", res.Data.Venues)
		}
	})

	t.Run("POST /api/v1/venues/compare - Alias route works for 3 Hotels", func(t *testing.T) {
		body := map[string]any{
			"type":      "hotel",
			"venue_ids": []string{h1, h2, h3},
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/venues/compare", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("POST /api/v1/facilities/compare - 2 Marriage Halls Success", func(t *testing.T) {
		body := map[string]any{
			"type":      "hall",
			"venue_ids": []string{m1, m2},
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/facilities/compare", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
		}

		var res struct {
			Success bool `json:"success"`
			Data    struct {
				Type   string `json:"type"`
				Total  int    `json:"total"`
				Venues []struct {
					ID       string  `json:"id"`
					Name     string  `json:"name"`
					Type     string  `json:"type"`
					Capacity int     `json:"capacity"`
					Price    float64 `json:"price"`
				} `json:"venues"`
			} `json:"data"`
		}
		json.Unmarshal(rr.Body.Bytes(), &res)
		if res.Data.Type != "hall" || res.Data.Total != 2 {
			t.Errorf("unexpected hall response: %+v", res)
		}
	})

	t.Run("POST /api/v1/facilities/compare - 3 Marriage Halls Success", func(t *testing.T) {
		body := map[string]any{
			"type":      "hall",
			"venue_ids": []string{m1, m2, m3},
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/facilities/compare", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("POST /api/v1/facilities/compare - Rejects Mixed Hotel and Marriage Hall", func(t *testing.T) {
		body := map[string]any{
			"type":      "hotel",
			"venue_ids": []string{h1, m1},
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/facilities/compare", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rr.Code)
		}
		var errRes struct {
			Success   bool   `json:"success"`
			ErrorCode string `json:"errorCode"`
		}
		json.Unmarshal(rr.Body.Bytes(), &errRes)
		if errRes.ErrorCode != "INVALID_COMPARISON_TYPE" {
			t.Errorf("expected INVALID_COMPARISON_TYPE, got %s", errRes.ErrorCode)
		}
	})

	t.Run("POST /api/v1/facilities/compare - Rejects Only 1 Venue", func(t *testing.T) {
		body := map[string]any{
			"type":      "hotel",
			"venue_ids": []string{h1},
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/facilities/compare", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rr.Code)
		}
	})

	t.Run("POST /api/v1/facilities/compare - Rejects More Than 3 Venues", func(t *testing.T) {
		body := map[string]any{
			"type":      "hotel",
			"venue_ids": []string{h1, h2, h3, "44444444-4444-4444-4444-444444444444"},
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/facilities/compare", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rr.Code)
		}
	})

	t.Run("POST /api/v1/facilities/compare - Rejects Duplicate Venue IDs", func(t *testing.T) {
		body := map[string]any{
			"type":      "hotel",
			"venue_ids": []string{h1, h1},
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/facilities/compare", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rr.Code)
		}
	})

	t.Run("POST /api/v1/facilities/compare - Rejects Malformed JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/facilities/compare", bytes.NewReader([]byte("{invalid-json")))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rr.Code)
		}
	})

	t.Run("POST /api/v1/facilities/compare - Non-existent venue gives 404", func(t *testing.T) {
		body := map[string]any{
			"type":      "hotel",
			"venue_ids": []string{h1, "99999999-9999-9999-9999-999999999999"},
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/facilities/compare", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found, got %d", rr.Code)
		}
	})

	t.Run("GET /api/v1/facilities/compare with type and venue_ids query params", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/facilities/compare?type=hotel&venue_ids="+h1+","+h2, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
		}
	})
}

