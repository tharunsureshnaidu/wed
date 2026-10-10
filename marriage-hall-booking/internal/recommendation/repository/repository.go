package repository

import (
	"context"
	"math"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	facilityrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

// Repository defines data access for recommendations.
type Repository interface {
	GetRecommendations(ctx context.Context, params dto.RecommendationParams) ([]facilityrepo.VenueResponse, int64, error)
}

type Repo struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

func (r *Repo) GetRecommendations(ctx context.Context, params dto.RecommendationParams) ([]facilityrepo.VenueResponse, int64, error) {
	// Map API venue type filter to DB representation.
	// Postgres facilities.type stores 'MARRIAGE_HALL' or 'HOTEL'.
	dbType := "ALL"
	switch strings.ToUpper(strings.TrimSpace(params.Type)) {
	case "HALL":
		dbType = venuetype.StoredHall
	case "HOTEL":
		dbType = venuetype.Hotel
	}

	eventType := strings.ToUpper(strings.TrimSpace(params.EventType))

	whereClause := `
		WHERE f.is_deleted = FALSE
		  AND f.status = 'APPROVED'
		  AND f.lat IS NOT NULL
		  AND f.lng IS NOT NULL
		  AND f.lat BETWEEN -90 AND 90
		  AND f.lng BETWEEN -180 AND 180
		  AND ($3 = 'ALL' OR f.type = $3)
		  AND earth_distance(ll_to_earth(f.lat::double precision, f.lng::double precision),
		                     ll_to_earth($1, $2)) <= ($4 * 1000.0)
		  AND ($5 = '' OR EXISTS (
			SELECT 1 FROM facility_events fe
			 WHERE fe.facility_id = f.id AND fe.event_code = $5)
		      OR NOT EXISTS (
			SELECT 1 FROM facility_events fe WHERE fe.facility_id = f.id))
	`

	args := []any{params.Lat, params.Lng, dbType, params.RadiusKm, eventType}

	// 1. Count total matching items within radius
	var total int64
	countQuery := `SELECT count(*)` + facilityrepo.FacilityFrom + whereClause
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if total == 0 {
		return []facilityrepo.VenueResponse{}, 0, nil
	}

	// 2. Fetch paginated results ordered by rating DESC, then distance ASC
	offset := httpx.Offset(params.Page, params.Size)
	args = append(args, params.Size, offset)

	selectQuery := `
		SELECT ` + facilityrepo.FacilityCols + facilityrepo.FacilityFrom + whereClause + `
		 ORDER BY COALESCE(f.avg_rating, 0) DESC,
		          earth_distance(ll_to_earth(f.lat::double precision, f.lng::double precision),
		                         ll_to_earth($1, $2)) ASC
		 LIMIT $6 OFFSET $7
	`

	rows, err := r.db.Query(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	facilities := make([]*facilityrepo.Facility, 0, params.Size)
	ids := make([]string, 0, params.Size)
	byID := make(map[string]*facilityrepo.Facility, params.Size)

	for rows.Next() {
		fac, err := facilityrepo.ScanFacility(rows)
		if err != nil {
			return nil, 0, err
		}
		if fac.Lat != nil && fac.Lng != nil {
			d := haversineKm(params.Lat, params.Lng, *fac.Lat, *fac.Lng)
			fac.DistanceKm = &d
		}
		facilities = append(facilities, fac)
		ids = append(ids, fac.ID)
		byID[fac.ID] = fac
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Batch-load event codes
	events, err := facilityrepo.New(r.db).EventsOfMany(ctx, ids)
	if err == nil {
		for _, fac := range facilities {
			fac.EventCodes = events[fac.ID]
		}
	}

	// Batch-load cover images if any
	if len(ids) > 0 {
		irows, err := r.db.Query(ctx, `
			SELECT DISTINCT ON (i.facility_id)
			       i.facility_id::text, i.id, i.url, i.is_cover, i.sort_order
			  FROM facility_images i
			 WHERE i.facility_id::text = ANY($1)
			 ORDER BY i.facility_id, i.is_cover DESC, i.sort_order`, ids)
		if err == nil {
			defer irows.Close()
			for irows.Next() {
				var fid string
				var im facilityrepo.Image
				if err := irows.Scan(&fid, &im.ID, &im.URL, &im.IsCover, &im.SortOrder); err == nil {
					if f, ok := byID[fid]; ok {
						f.Images = append(f.Images, im)
					}
				}
			}
		}
	}

	venues := make([]facilityrepo.VenueResponse, len(facilities))
	for i, fac := range facilities {
		venues[i] = fac.ToVenueResponse()
	}

	return venues, total, nil
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0
	lat1Rad := lat1 * math.Pi / 180.0
	lat2Rad := lat2 * math.Pi / 180.0

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return math.Round(earthRadiusKm*c*100) / 100
}
