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
		WHERE ` + venuetype.LiveSQL("f") + `
		  AND f.is_verified = TRUE
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
	var nearbyTotal int64
	countQuery := `SELECT count(*)` + facilityrepo.FacilityFrom + whereClause
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&nearbyTotal); err != nil {
		return nil, 0, err
	}

	offset := httpx.Offset(params.Page, params.Size)
	facilities := make([]*facilityrepo.Facility, 0, params.Size)
	ids := make([]string, 0, params.Size)
	seenIDs := make(map[string]bool)

	// Fetch nearby facilities if offset is within nearby results
	if offset < int(nearbyTotal) {
		fetchSize := params.Size
		selectQuery := `
			SELECT ` + facilityrepo.FacilityCols + facilityrepo.FacilityFrom + whereClause + `
			 ORDER BY COALESCE(f.avg_rating, 0) DESC,
			          earth_distance(ll_to_earth(f.lat::double precision, f.lng::double precision),
			                         ll_to_earth($1, $2)) ASC
			 LIMIT $6 OFFSET $7
		`
		queryArgs := append(args, fetchSize, offset)
		rows, err := r.db.Query(ctx, selectQuery, queryArgs...)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()

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
			seenIDs[fac.ID] = true
		}
		if err := rows.Err(); err != nil {
			return nil, 0, err
		}
	}

	// 2. If needed, backfill with top-rated venues across the platform (5 star -> 4 star -> 3 star)
	needed := params.Size - len(facilities)
	var fallbackTotal int64

	fallbackWhere := `
		WHERE ` + venuetype.LiveSQL("f") + `
		  AND f.is_verified = TRUE
		  AND ($1 = 'ALL' OR f.type = $1)
		  AND ($2 = '' OR EXISTS (
			SELECT 1 FROM facility_events fe
			 WHERE fe.facility_id = f.id AND fe.event_code = $2)
		      OR NOT EXISTS (
			SELECT 1 FROM facility_events fe WHERE fe.facility_id = f.id))
		  AND NOT (f.id = ANY($3::uuid[]))
	`

	var allNearbyIDs []string
	if nearbyTotal > 0 {
		nearbyIDsQuery := `SELECT f.id::text` + facilityrepo.FacilityFrom + whereClause
		nRows, err := r.db.Query(ctx, nearbyIDsQuery, args...)
		if err == nil {
			defer nRows.Close()
			for nRows.Next() {
				var nid string
				if err := nRows.Scan(&nid); err == nil {
					allNearbyIDs = append(allNearbyIDs, nid)
				}
			}
		}
	}

	fbArgs := []any{dbType, eventType, allNearbyIDs}
	fbCountQuery := `SELECT count(*)` + facilityrepo.FacilityFrom + fallbackWhere
	if err := r.db.QueryRow(ctx, fbCountQuery, fbArgs...).Scan(&fallbackTotal); err != nil {
		return nil, 0, err
	}

	total := nearbyTotal + fallbackTotal

	if needed > 0 && fallbackTotal > 0 {
		fbOffset := 0
		if offset > int(nearbyTotal) {
			fbOffset = offset - int(nearbyTotal)
		}

		fbSelectQuery := `
			SELECT ` + facilityrepo.FacilityCols + facilityrepo.FacilityFrom + fallbackWhere + `
			 ORDER BY COALESCE(f.avg_rating, 0) DESC,
			          COALESCE(f.star_rating, 0) DESC,
			          f.created_at DESC
			 LIMIT $4 OFFSET $5
		`
		fbArgsWithPagination := append(fbArgs, needed, fbOffset)
		fbRows, err := r.db.Query(ctx, fbSelectQuery, fbArgsWithPagination...)
		if err != nil {
			return nil, 0, err
		}
		defer fbRows.Close()

		for fbRows.Next() {
			fac, err := facilityrepo.ScanFacility(fbRows)
			if err != nil {
				return nil, 0, err
			}
			if !seenIDs[fac.ID] {
				if fac.Lat != nil && fac.Lng != nil {
					d := haversineKm(params.Lat, params.Lng, *fac.Lat, *fac.Lng)
					fac.DistanceKm = &d
				}
				facilities = append(facilities, fac)
				ids = append(ids, fac.ID)
				seenIDs[fac.ID] = true
			}
		}
		if err := fbRows.Err(); err != nil {
			return nil, 0, err
		}
	}

	if len(facilities) == 0 {
		return []facilityrepo.VenueResponse{}, 0, nil
	}

	// Batch-load event codes
	events, err := facilityrepo.New(r.db).EventsOfMany(ctx, ids)
	if err == nil {
		for _, fac := range facilities {
			fac.EventCodes = events[fac.ID]
		}
	}

	// Batch-load images
	if len(ids) > 0 {
		images, err := facilityrepo.New(r.db).AllImagesOfMany(ctx, ids)
		if err == nil {
			for _, fac := range facilities {
				if im, ok := images[fac.ID]; ok && im != nil {
					fac.Images = im
					fac.CoverImage = facilityrepo.DeriveCoverImage(im)
				}
			}
		}
	}

	_ = facilityrepo.New(r.db).PopulateDiscounts(ctx, facilities)

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
