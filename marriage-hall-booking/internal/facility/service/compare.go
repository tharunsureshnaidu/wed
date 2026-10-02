package service

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

const (
	MaxCompareVenues = 3
	MinCompareVenues = 2

	UnitPerNight    = "PER_NIGHT"
	UnitPerEventDay = "PER_EVENT_DAY"

	LabelPerNight    = "per night"
	LabelPerEventDay = "per event day"
)

// Repository specifies data operations required by CompareService.
type Repository interface {
	ByIDs(ctx context.Context, ids []string) ([]repository.Facility, error)
	RoomTypesOfMany(ctx context.Context, ids []string) (map[string][]repository.RoomType, error)
	PackagesOfMany(ctx context.Context, ids []string) (map[string][]repository.HallPackage, error)
	AddonsOfMany(ctx context.Context, ids []string) (map[string][]repository.AddonService, error)
	AllImagesOfMany(ctx context.Context, ids []string) (map[string][]repository.Image, error)
	ReviewsOfMany(ctx context.Context, ids []string) (map[string][]repository.Review, error)
}

// CompareRequest carries the payload for comparing venues.
type CompareRequest struct {
	Type          string   `json:"type"`
	VenueIDs      []string `json:"venue_ids"`
	VenueIDsCamel []string `json:"venueIds,omitempty"`
	IDs           []string `json:"ids,omitempty"`

	UserLat *float64 `json:"lat,omitempty"`
	UserLng *float64 `json:"lng,omitempty"`
}

// CompareResponse holds comparison results conforming to the API specification.
type CompareResponse struct {
	Type      string                          `json:"type"`
	Total     int                             `json:"total"`
	Venues    []repository.VenueCompareDetail `json:"venues"`
	Amenities []repository.CompareAmenityRow  `json:"amenities,omitempty"`
}

type CompareService struct {
	repo Repository
}

func NewCompareService(repo Repository) *CompareService {
	return &CompareService{repo: repo}
}

// Compare performs dynamic validation and side-by-side comparison for 2 or 3 venues of the same type.
func (s *CompareService) Compare(ctx context.Context, req CompareRequest) (*CompareResponse, error) {
	// 1. Resolve venue IDs from supported JSON property aliases
	ids := req.VenueIDs
	if len(ids) == 0 && len(req.VenueIDsCamel) > 0 {
		ids = req.VenueIDsCamel
	}
	if len(ids) == 0 && len(req.IDs) > 0 {
		ids = req.IDs
	}

	// 2. Validate Venue Type
	reqType := strings.TrimSpace(req.Type)
	if reqType == "" {
		return nil, apperr.BadRequest("VALIDATION_ERROR", "Venue type is required. Supported types: hotel, hall")
	}

	normType := strings.ToUpper(reqType)
	var expectedStoredType string
	var wireType string

	switch normType {
	case "HOTEL":
		expectedStoredType = "HOTEL"
		wireType = "hotel"
	case "HALL", "MARRIAGE_HALL":
		expectedStoredType = "MARRIAGE_HALL"
		wireType = "hall"
	default:
		return nil, apperr.BadRequest("VALIDATION_ERROR", "Invalid venue type. Supported types: hotel, hall")
	}

	// 3. Validate Venue ID Count (minimum 2, maximum 3)
	if len(ids) < MinCompareVenues {
		return nil, apperr.BadRequest("VALIDATION_ERROR", "At least 2 venue ids are required to compare")
	}
	if len(ids) > MaxCompareVenues {
		return nil, apperr.BadRequest("VALIDATION_ERROR", "At most 3 venues can be compared at once")
	}

	// 4. Validate UUID format and check duplicates
	seen := make(map[string]bool, len(ids))
	cleanIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" || !httpx.ValidUUID(trimmed) {
			return nil, apperr.BadRequest("VALIDATION_ERROR", "Each venue id must be a valid UUID")
		}
		if seen[trimmed] {
			return nil, apperr.BadRequest("VALIDATION_ERROR", "Duplicate venue ids are not allowed")
		}
		seen[trimmed] = true
		cleanIDs = append(cleanIDs, trimmed)
	}

	// 5. Query Facilities from Database
	found, err := s.repo.ByIDs(ctx, cleanIDs)
	if err != nil {
		return nil, err
	}

	// All requested venues must exist in the database
	if len(found) != len(cleanIDs) {
		return nil, apperr.New(http.StatusNotFound, "FACILITY_NOT_FOUND", "One or more venues could not be found")
	}

	// Map found facilities by ID to easily preserve requested input order
	byID := make(map[string]repository.Facility, len(found))
	for _, f := range found {
		byID[f.ID] = f
	}

	// 6. Strict Type Validation and Mixed Type Rejection
	for _, f := range found {
		if f.Type != expectedStoredType {
			if expectedStoredType == "HOTEL" {
				return nil, apperr.BadRequest("INVALID_COMPARISON_TYPE", "Cannot compare venues of different types. All venues must be hotels. Mixed comparisons are not allowed.")
			}
			return nil, apperr.BadRequest("INVALID_COMPARISON_TYPE", "Cannot compare venues of different types. All venues must be marriage halls. Mixed comparisons are not allowed.")
		}
	}

	for i := 1; i < len(found); i++ {
		if found[i].Type != found[0].Type {
			return nil, apperr.BadRequest("INVALID_COMPARISON_TYPE", "Mixed comparisons are not allowed. All venues must be of the same type.")
		}
	}

	// 7. Order-preserved facilities slice
	ordered := make([]repository.Facility, 0, len(cleanIDs))
	for _, id := range cleanIDs {
		if f, ok := byID[id]; ok {
			ordered = append(ordered, f)
		}
	}

	// 8. Fetch auxiliary details in batch (No N+1 queries)
	imagesMap, err := s.repo.AllImagesOfMany(ctx, cleanIDs)
	if err != nil {
		return nil, err
	}

	reviewsMap, err := s.repo.ReviewsOfMany(ctx, cleanIDs)
	if err != nil {
		return nil, err
	}

	var roomTypesMap map[string][]repository.RoomType
	var packagesMap map[string][]repository.HallPackage
	var addonsMap map[string][]repository.AddonService

	if expectedStoredType == "HOTEL" {
		roomTypesMap, err = s.repo.RoomTypesOfMany(ctx, cleanIDs)
		if err != nil {
			return nil, err
		}
	} else {
		packagesMap, err = s.repo.PackagesOfMany(ctx, cleanIDs)
		if err != nil {
			return nil, err
		}
		addonsMap, err = s.repo.AddonsOfMany(ctx, cleanIDs)
		if err != nil {
			return nil, err
		}
	}

	// 9. Assemble comparison details for each venue
	venues := make([]repository.VenueCompareDetail, 0, len(ordered))
	for _, f := range ordered {
		detail := buildVenueCompareDetail(f, req.UserLat, req.UserLng, imagesMap[f.ID], reviewsMap[f.ID], roomTypesMap[f.ID], packagesMap[f.ID], addonsMap[f.ID])
		venues = append(venues, detail)
	}

	// 10. Compute amenity matrix across venues
	matrix := computeAmenityMatrix(ordered)

	return &CompareResponse{
		Type:      wireType,
		Total:     len(venues),
		Venues:    venues,
		Amenities: matrix,
	}, nil
}

func buildVenueCompareDetail(
	f repository.Facility,
	userLat, userLng *float64,
	images []repository.Image,
	reviews []repository.Review,
	roomTypes []repository.RoomType,
	packages []repository.HallPackage,
	addons []repository.AddonService,
) repository.VenueCompareDetail {
	var loc *string
	if f.City != nil && *f.City != "" {
		loc = f.City
	} else if f.FullAddress != nil && *f.FullAddress != "" {
		loc = f.FullAddress
	}

	// Cover image
	var coverImage *string
	for _, img := range images {
		if img.IsCover && img.URL != "" {
			c := img.URL
			coverImage = &c
			break
		}
	}
	if coverImage == nil && len(images) > 0 && images[0].URL != "" {
		c := images[0].URL
		coverImage = &c
	}
	if coverImage == nil && len(f.Images) > 0 && f.Images[0].URL != "" {
		c := f.Images[0].URL
		coverImage = &c
	}

	if images == nil {
		images = f.Images
	}
	if images == nil {
		images = []repository.Image{}
	}
	if reviews == nil {
		reviews = []repository.Review{}
	}

	amenities := f.Amenities
	if amenities == nil {
		amenities = []repository.Amenity{}
	}

	detail := repository.VenueCompareDetail{
		ID:          f.ID,
		Name:        f.Name,
		Type:        venuetype.API(f.Type),
		Description: f.Description,
		City:        f.City,
		Address:     f.FullAddress,
		FullAddress: f.FullAddress,
		State:       f.State,
		Zipcode:     f.Zipcode,
		Country:     f.Country,
		Location:    loc,
		CoverImage:  coverImage,
		Images:      images,
		AvgRating:   f.AvgRating,
		ReviewCount: f.ReviewCount,
		Reviews:     reviews,
		Lat:         f.Lat,
		Lng:         f.Lng,
		Amenities:   amenities,

		StartingPrice:   f.StartingPrice,
		DiscountedPrice: f.DiscountedPrice,
		DiscountPercent: f.DiscountPercent,
		DiscountLabel:   f.DiscountLabel,
		HasDiscount:     f.HasDiscount,
	}

	// Distance from caller location if coordinates provided
	if userLat != nil && userLng != nil && f.Lat != nil && f.Lng != nil {
		d := haversineKm(*userLat, *userLng, *f.Lat, *f.Lng)
		detail.DistanceKm = &d
	}

	if f.Type == "HOTEL" {
		detail.PriceUnit = UnitPerNight
		detail.PriceUnitLabel = LabelPerNight
		detail.StarRating = f.StarRating
		detail.CheckInTime = f.CheckInTime
		detail.CheckOutTime = f.CheckOutTime

		if roomTypes == nil {
			roomTypes = []repository.RoomType{}
		}
		detail.RoomTypes = roomTypes

		// Price: headline price for hotel (cheapest room rate)
		if f.StartingPrice != nil {
			detail.Price = f.StartingPrice
		} else if len(roomTypes) > 0 {
			minRate := roomTypes[0].BasePricePerNight
			for _, rt := range roomTypes[1:] {
				if rt.BasePricePerNight < minRate {
					minRate = rt.BasePricePerNight
				}
			}
			detail.Price = &minRate
		}

		// Capacity: total adult capacity across rooms if available
		if len(roomTypes) > 0 {
			totalCap := 0
			for _, rt := range roomTypes {
				rooms := rt.TotalRooms
				if rooms <= 0 {
					rooms = 1
				}
				totalCap += rt.CapacityAdults * rooms
			}
			if totalCap > 0 {
				detail.Capacity = &totalCap
			}
		}
	} else {
		detail.PriceUnit = UnitPerEventDay
		detail.PriceUnitLabel = LabelPerEventDay
		detail.CapacityPax = f.CapacityPax
		detail.SeatingCapacity = f.SeatingCapacity
		detail.FloatingCapacity = f.FloatingCapacity
		detail.AreaSqft = f.AreaSqft
		detail.BasePricePerDay = f.BasePricePerDay
		detail.MinBookingSize = f.MinBookingSize

		if packages == nil {
			packages = []repository.HallPackage{}
		}
		detail.Packages = packages

		if addons == nil {
			addons = []repository.AddonService{}
		}
		detail.Addons = addons

		// Price: base price per day for marriage hall
		if f.BasePricePerDay != nil {
			detail.Price = f.BasePricePerDay
		} else if f.StartingPrice != nil {
			detail.Price = f.StartingPrice
		}

		// Capacity: guest capacity pax
		if f.CapacityPax != nil {
			detail.Capacity = f.CapacityPax
		} else if f.SeatingCapacity != nil {
			detail.Capacity = f.SeatingCapacity
		} else if f.FloatingCapacity != nil {
			detail.Capacity = f.FloatingCapacity
		}
	}

	return detail
}

func computeAmenityMatrix(fs []repository.Facility) []repository.CompareAmenityRow {
	names := map[string]string{}
	has := map[string]map[int]bool{}

	for i, f := range fs {
		for _, a := range f.Amenities {
			k := amenityKey(a)
			names[k] = a.Name
			if has[k] == nil {
				has[k] = map[int]bool{}
			}
			has[k][i] = true
		}
	}

	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return names[keys[i]] < names[keys[j]] })

	rows := make([]repository.CompareAmenityRow, 0, len(keys))
	for _, k := range keys {
		row := repository.CompareAmenityRow{
			Code:    k,
			Name:    names[k],
			Present: make([]bool, len(fs)),
			AllHave: true,
		}
		count := 0
		for i := range fs {
			if has[k][i] {
				row.Present[i] = true
				count++
			} else {
				row.AllHave = false
			}
		}
		row.NoneHave = count == 0
		rows = append(rows, row)
	}
	return rows
}

func amenityKey(a repository.Amenity) string {
	if a.Code != nil && *a.Code != "" {
		return *a.Code
	}
	return a.Name
}

func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKm = 6371.0
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLng := rad(lat2-lat1), rad(lng2-lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	km := earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return math.Round(km*100) / 100
}
