package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
)

type mockRepo struct {
	byIDsFunc       func(ctx context.Context, ids []string) ([]repository.Facility, error)
	roomTypesFunc   func(ctx context.Context, ids []string) (map[string][]repository.RoomType, error)
	packagesFunc    func(ctx context.Context, ids []string) (map[string][]repository.HallPackage, error)
	addonsFunc      func(ctx context.Context, ids []string) (map[string][]repository.AddonService, error)
	allImagesFunc   func(ctx context.Context, ids []string) (map[string][]repository.Image, error)
	reviewsFunc     func(ctx context.Context, ids []string) (map[string][]repository.Review, error)
}

func (m *mockRepo) ByIDs(ctx context.Context, ids []string) ([]repository.Facility, error) {
	if m.byIDsFunc != nil {
		return m.byIDsFunc(ctx, ids)
	}
	return nil, nil
}

func (m *mockRepo) RoomTypesOfMany(ctx context.Context, ids []string) (map[string][]repository.RoomType, error) {
	if m.roomTypesFunc != nil {
		return m.roomTypesFunc(ctx, ids)
	}
	return map[string][]repository.RoomType{}, nil
}

func (m *mockRepo) PackagesOfMany(ctx context.Context, ids []string) (map[string][]repository.HallPackage, error) {
	if m.packagesFunc != nil {
		return m.packagesFunc(ctx, ids)
	}
	return map[string][]repository.HallPackage{}, nil
}

func (m *mockRepo) AddonsOfMany(ctx context.Context, ids []string) (map[string][]repository.AddonService, error) {
	if m.addonsFunc != nil {
		return m.addonsFunc(ctx, ids)
	}
	return map[string][]repository.AddonService{}, nil
}

func (m *mockRepo) AllImagesOfMany(ctx context.Context, ids []string) (map[string][]repository.Image, error) {
	if m.allImagesFunc != nil {
		return m.allImagesFunc(ctx, ids)
	}
	return map[string][]repository.Image{}, nil
}

func (m *mockRepo) ReviewsOfMany(ctx context.Context, ids []string) (map[string][]repository.Review, error) {
	if m.reviewsFunc != nil {
		return m.reviewsFunc(ctx, ids)
	}
	return map[string][]repository.Review{}, nil
}

func ptr[T any](v T) *T { return &v }

const (
	h1ID = "11111111-1111-1111-1111-111111111111"
	h2ID = "22222222-2222-2222-2222-222222222222"
	h3ID = "33333333-3333-3333-3333-333333333333"

	m1ID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	m2ID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	m3ID = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

func sampleHotel(id, name string) repository.Facility {
	return repository.Facility{
		ID:            id,
		Name:          name,
		Type:          "HOTEL",
		City:          ptr("Lucknow"),
		FullAddress:   ptr("Hazratganj, Lucknow"),
		StarRating:    ptr(4),
		StartingPrice: ptr(4500.0),
		Lat:           ptr(26.8467),
		Lng:           ptr(80.9462),
		Amenities: []repository.Amenity{
			{Name: "WiFi", Code: ptr("WIFI")},
			{Name: "Swimming Pool", Code: ptr("POOL")},
		},
	}
}

func sampleHall(id, name string) repository.Facility {
	return repository.Facility{
		ID:              id,
		Name:            name,
		Type:            "MARRIAGE_HALL",
		City:            ptr("Kanpur"),
		FullAddress:     ptr("Civil Lines, Kanpur"),
		CapacityPax:     ptr(500),
		BasePricePerDay: ptr(75000.0),
		StartingPrice:   ptr(75000.0),
		Lat:             ptr(26.4499),
		Lng:             ptr(80.3319),
		Amenities: []repository.Amenity{
			{Name: "Parking", Code: ptr("PARKING")},
			{Name: "AC Hall", Code: ptr("AC")},
		},
	}
}

func TestCompareTwoHotelsSuccess(t *testing.T) {
	repo := &mockRepo{
		byIDsFunc: func(ctx context.Context, ids []string) ([]repository.Facility, error) {
			return []repository.Facility{
				sampleHotel(h1ID, "Hotel Grand"),
				sampleHotel(h2ID, "Hotel Palace"),
			}, nil
		},
		roomTypesFunc: func(ctx context.Context, ids []string) (map[string][]repository.RoomType, error) {
			return map[string][]repository.RoomType{
				h1ID: {{ID: "rt-1", Name: "Deluxe Room", BasePricePerNight: 4500, CapacityAdults: 2, TotalRooms: 10}},
				h2ID: {{ID: "rt-2", Name: "Executive Suite", BasePricePerNight: 6000, CapacityAdults: 2, TotalRooms: 5}},
			}, nil
		},
	}

	svc := NewCompareService(repo)
	res, err := svc.Compare(context.Background(), CompareRequest{
		Type:     "hotel",
		VenueIDs: []string{h1ID, h2ID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Type != "hotel" {
		t.Errorf("expected wire type 'hotel', got %q", res.Type)
	}
	if res.Total != 2 {
		t.Errorf("expected total 2, got %d", res.Total)
	}
	if len(res.Venues) != 2 {
		t.Fatalf("expected 2 venues, got %d", len(res.Venues))
	}

	v1 := res.Venues[0]
	v2 := res.Venues[1]

	if v1.ID != h1ID || v1.Name != "Hotel Grand" || v1.Type != "HOTEL" {
		t.Errorf("venue 1 unexpected: %+v", v1)
	}
	if v2.ID != h2ID || v2.Name != "Hotel Palace" || v2.Type != "HOTEL" {
		t.Errorf("venue 2 unexpected: %+v", v2)
	}
	if len(v1.RoomTypes) != 1 || v1.RoomTypes[0].Name != "Deluxe Room" {
		t.Errorf("hotel 1 room types missing or wrong: %+v", v1.RoomTypes)
	}
	if v1.PriceUnit != UnitPerNight {
		t.Errorf("expected price unit %s, got %s", UnitPerNight, v1.PriceUnit)
	}
}

func TestCompareThreeHotelsSuccess(t *testing.T) {
	repo := &mockRepo{
		byIDsFunc: func(ctx context.Context, ids []string) ([]repository.Facility, error) {
			return []repository.Facility{
				sampleHotel(h1ID, "Hotel One"),
				sampleHotel(h2ID, "Hotel Two"),
				sampleHotel(h3ID, "Hotel Three"),
			}, nil
		},
	}

	svc := NewCompareService(repo)
	res, err := svc.Compare(context.Background(), CompareRequest{
		Type:     "HOTEL",
		VenueIDs: []string{h1ID, h2ID, h3ID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Total != 3 || len(res.Venues) != 3 {
		t.Errorf("expected 3 venues, got %d", res.Total)
	}
	if res.Venues[0].ID != h1ID || res.Venues[1].ID != h2ID || res.Venues[2].ID != h3ID {
		t.Errorf("order not preserved: %v, %v, %v", res.Venues[0].ID, res.Venues[1].ID, res.Venues[2].ID)
	}
}

func TestCompareTwoMarriageHallsSuccess(t *testing.T) {
	repo := &mockRepo{
		byIDsFunc: func(ctx context.Context, ids []string) ([]repository.Facility, error) {
			return []repository.Facility{
				sampleHall(m1ID, "Royal Marriage Hall"),
				sampleHall(m2ID, "Grand Banquet Hall"),
			}, nil
		},
		packagesFunc: func(ctx context.Context, ids []string) (map[string][]repository.HallPackage, error) {
			return map[string][]repository.HallPackage{
				m1ID: {{ID: "pkg-1", Name: "Gold Wedding Package", Price: 150000, IncludesCatering: true}},
			}, nil
		},
		addonsFunc: func(ctx context.Context, ids []string) (map[string][]repository.AddonService, error) {
			return map[string][]repository.AddonService{
				m1ID: {{ID: "add-1", Name: "DJ & Sound", Price: 15000}},
			}, nil
		},
	}

	svc := NewCompareService(repo)
	res, err := svc.Compare(context.Background(), CompareRequest{
		Type:     "hall",
		VenueIDs: []string{m1ID, m2ID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Type != "hall" {
		t.Errorf("expected wire type 'hall', got %q", res.Type)
	}
	if res.Total != 2 || len(res.Venues) != 2 {
		t.Fatalf("expected 2 venues, got %d", res.Total)
	}

	h1 := res.Venues[0]
	if h1.ID != m1ID || h1.Type != "HALL" {
		t.Errorf("unexpected hall 1: %+v", h1)
	}
	if h1.PriceUnit != UnitPerEventDay {
		t.Errorf("expected price unit %s, got %s", UnitPerEventDay, h1.PriceUnit)
	}
	if len(h1.Packages) != 1 || h1.Packages[0].Name != "Gold Wedding Package" {
		t.Errorf("hall packages missing: %+v", h1.Packages)
	}
	if len(h1.Addons) != 1 || h1.Addons[0].Name != "DJ & Sound" {
		t.Errorf("hall addons missing: %+v", h1.Addons)
	}
}

func TestCompareThreeMarriageHallsSuccess(t *testing.T) {
	repo := &mockRepo{
		byIDsFunc: func(ctx context.Context, ids []string) ([]repository.Facility, error) {
			return []repository.Facility{
				sampleHall(m1ID, "Hall 1"),
				sampleHall(m2ID, "Hall 2"),
				sampleHall(m3ID, "Hall 3"),
			}, nil
		},
	}

	svc := NewCompareService(repo)
	res, err := svc.Compare(context.Background(), CompareRequest{
		Type:     "marriage_hall",
		VenueIDs: []string{m1ID, m2ID, m3ID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Total != 3 || len(res.Venues) != 3 {
		t.Errorf("expected 3 venues, got %d", res.Total)
	}
}

func TestPreserveRequestedOrder(t *testing.T) {
	// Requesting in order: h2, h1
	repo := &mockRepo{
		byIDsFunc: func(ctx context.Context, ids []string) ([]repository.Facility, error) {
			// Database returns them in different order (e.g. h1, h2)
			return []repository.Facility{
				sampleHotel(h1ID, "Hotel 1"),
				sampleHotel(h2ID, "Hotel 2"),
			}, nil
		},
	}

	svc := NewCompareService(repo)
	res, err := svc.Compare(context.Background(), CompareRequest{
		Type:     "hotel",
		VenueIDs: []string{h2ID, h1ID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Venues[0].ID != h2ID || res.Venues[1].ID != h1ID {
		t.Errorf("expected order [h2, h1], got [%s, %s]", res.Venues[0].ID, res.Venues[1].ID)
	}
}

func TestValidationScenarios(t *testing.T) {
	repo := &mockRepo{
		byIDsFunc: func(ctx context.Context, ids []string) ([]repository.Facility, error) {
			return []repository.Facility{
				sampleHotel(h1ID, "Hotel 1"),
				sampleHotel(h2ID, "Hotel 2"),
			}, nil
		},
	}
	svc := NewCompareService(repo)

	tests := []struct {
		name        string
		req         CompareRequest
		wantCode    string
		wantStatus  int
		errContains string
	}{
		{
			name:        "empty venue list",
			req:         CompareRequest{Type: "hotel", VenueIDs: []string{}},
			wantCode:    "VALIDATION_ERROR",
			wantStatus:  http.StatusBadRequest,
			errContains: "At least 2 venue ids are required",
		},
		{
			name:        "only 1 venue",
			req:         CompareRequest{Type: "hotel", VenueIDs: []string{h1ID}},
			wantCode:    "VALIDATION_ERROR",
			wantStatus:  http.StatusBadRequest,
			errContains: "At least 2 venue ids are required",
		},
		{
			name:        "more than 3 venues",
			req:         CompareRequest{Type: "hotel", VenueIDs: []string{h1ID, h2ID, h3ID, "44444444-4444-4444-4444-444444444444"}},
			wantCode:    "VALIDATION_ERROR",
			wantStatus:  http.StatusBadRequest,
			errContains: "At most 3 venues can be compared",
		},
		{
			name:        "duplicate venue IDs",
			req:         CompareRequest{Type: "hotel", VenueIDs: []string{h1ID, h1ID}},
			wantCode:    "VALIDATION_ERROR",
			wantStatus:  http.StatusBadRequest,
			errContains: "Duplicate venue ids are not allowed",
		},
		{
			name:        "invalid UUID format",
			req:         CompareRequest{Type: "hotel", VenueIDs: []string{h1ID, "not-a-uuid"}},
			wantCode:    "VALIDATION_ERROR",
			wantStatus:  http.StatusBadRequest,
			errContains: "Each venue id must be a valid UUID",
		},
		{
			name:        "invalid venue type",
			req:         CompareRequest{Type: "resort", VenueIDs: []string{h1ID, h2ID}},
			wantCode:    "VALIDATION_ERROR",
			wantStatus:  http.StatusBadRequest,
			errContains: "Invalid venue type",
		},
		{
			name:        "empty venue type",
			req:         CompareRequest{Type: "", VenueIDs: []string{h1ID, h2ID}},
			wantCode:    "VALIDATION_ERROR",
			wantStatus:  http.StatusBadRequest,
			errContains: "Venue type is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Compare(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
			var ae *apperr.Error
			if !errors.As(err, &ae) {
				t.Fatalf("expected *apperr.Error, got %T: %v", err, err)
			}
			if ae.Status != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, ae.Status)
			}
			if ae.Code != tc.wantCode {
				t.Errorf("expected code %s, got %s", tc.wantCode, ae.Code)
			}
		})
	}
}

func TestRejectionOfMixedComparisons(t *testing.T) {
	tests := []struct {
		name       string
		reqType    string
		ids        []string
		dbReturn   []repository.Facility
		wantErrMsg string
	}{
		{
			name:    "Hotel requested but contains Marriage Hall (Hotel + Marriage Hall)",
			reqType: "hotel",
			ids:     []string{h1ID, m1ID},
			dbReturn: []repository.Facility{
				sampleHotel(h1ID, "Hotel 1"),
				sampleHall(m1ID, "Hall 1"),
			},
			wantErrMsg: "Cannot compare venues of different types. All venues must be hotels. Mixed comparisons are not allowed.",
		},
		{
			name:    "Marriage Hall requested but contains Hotel (Hall + Hotel)",
			reqType: "hall",
			ids:     []string{m1ID, h1ID},
			dbReturn: []repository.Facility{
				sampleHall(m1ID, "Hall 1"),
				sampleHotel(h1ID, "Hotel 1"),
			},
			wantErrMsg: "Cannot compare venues of different types. All venues must be marriage halls. Mixed comparisons are not allowed.",
		},
		{
			name:    "Hotel requested but contains 2 Hotels + 1 Marriage Hall",
			reqType: "hotel",
			ids:     []string{h1ID, h2ID, m1ID},
			dbReturn: []repository.Facility{
				sampleHotel(h1ID, "Hotel 1"),
				sampleHotel(h2ID, "Hotel 2"),
				sampleHall(m1ID, "Hall 1"),
			},
			wantErrMsg: "Cannot compare venues of different types. All venues must be hotels. Mixed comparisons are not allowed.",
		},
		{
			name:    "Hall requested but contains 1 Hotel + 2 Marriage Halls",
			reqType: "hall",
			ids:     []string{h1ID, m1ID, m2ID},
			dbReturn: []repository.Facility{
				sampleHotel(h1ID, "Hotel 1"),
				sampleHall(m1ID, "Hall 1"),
				sampleHall(m2ID, "Hall 2"),
			},
			wantErrMsg: "Cannot compare venues of different types. All venues must be marriage halls. Mixed comparisons are not allowed.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockRepo{
				byIDsFunc: func(ctx context.Context, ids []string) ([]repository.Facility, error) {
					return tc.dbReturn, nil
				},
			}
			svc := NewCompareService(repo)
			_, err := svc.Compare(context.Background(), CompareRequest{
				Type:     tc.reqType,
				VenueIDs: tc.ids,
			})
			if err == nil {
				t.Fatalf("expected mixed comparison to be rejected, got nil")
			}
			var ae *apperr.Error
			if !errors.As(err, &ae) {
				t.Fatalf("expected apperr.Error, got %T: %v", err, err)
			}
			if ae.Status != http.StatusBadRequest {
				t.Errorf("expected 400 Bad Request, got %d", ae.Status)
			}
			if ae.Code != "INVALID_COMPARISON_TYPE" {
				t.Errorf("expected INVALID_COMPARISON_TYPE, got %s", ae.Code)
			}
		})
	}
}

func TestVenueNotFound(t *testing.T) {
	repo := &mockRepo{
		byIDsFunc: func(ctx context.Context, ids []string) ([]repository.Facility, error) {
			// Only 1 of 2 found
			return []repository.Facility{
				sampleHotel(h1ID, "Hotel 1"),
			}, nil
		},
	}

	svc := NewCompareService(repo)
	_, err := svc.Compare(context.Background(), CompareRequest{
		Type:     "hotel",
		VenueIDs: []string{h1ID, h2ID},
	})
	if err == nil {
		t.Fatalf("expected error for non-existent venue, got nil")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apperr.Error, got %T: %v", err, err)
	}
	if ae.Status != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", ae.Status)
	}
	if ae.Code != "FACILITY_NOT_FOUND" {
		t.Errorf("expected FACILITY_NOT_FOUND, got %s", ae.Code)
	}
}

func TestDatabaseFailure(t *testing.T) {
	dbErr := errors.New("db connection failure")
	repo := &mockRepo{
		byIDsFunc: func(ctx context.Context, ids []string) ([]repository.Facility, error) {
			return nil, dbErr
		},
	}

	svc := NewCompareService(repo)
	_, err := svc.Compare(context.Background(), CompareRequest{
		Type:     "hotel",
		VenueIDs: []string{h1ID, h2ID},
	})
	if err == nil {
		t.Fatalf("expected error on DB failure, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected dbErr, got %v", err)
	}
}
