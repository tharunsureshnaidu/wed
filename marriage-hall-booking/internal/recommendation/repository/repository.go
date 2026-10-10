package repository

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

// Repository defines data access for recommendations.
type Repository interface {
	GetRecommendations(ctx context.Context, params dto.RecommendationParams) ([]dto.RecommendationItem, int64, error)
}

type Repo struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

func (r *Repo) GetRecommendations(ctx context.Context, params dto.RecommendationParams) ([]dto.RecommendationItem, int64, error) {
	// Map API venue type filter to DB representation.
	// Postgres facilities.type stores 'MARRIAGE_HALL' or 'HOTEL'.
	dbType := "ALL"
	switch strings.ToUpper(strings.TrimSpace(params.Type)) {
	case "HALL":
		dbType = venuetype.StoredHall
	case "HOTEL":
		dbType = venuetype.Hotel
	}

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
	`

	// 1. Count total matching items within radius
	var total int64
	countQuery := `SELECT count(*) FROM facilities f` + whereClause
	if err := r.db.QueryRow(ctx, countQuery, params.Lat, params.Lng, dbType, params.RadiusKm).Scan(&total); err != nil {
		return nil, 0, err
	}

	if total == 0 {
		return []dto.RecommendationItem{}, 0, nil
	}

	// 2. Fetch paginated results ordered by rating DESC, then distance ASC
	offset := httpx.Offset(params.Page, params.Size)

	selectQuery := `
		SELECT f.id::text,
		       f.type,
		       f.name,
		       COALESCE(f.avg_rating, 0)::double precision AS avg_rating,
		       COALESCE(f.review_count, 0) AS review_count,
		       round((earth_distance(ll_to_earth(f.lat::double precision, f.lng::double precision),
		                             ll_to_earth($1, $2)) / 1000.0)::numeric, 2)::double precision AS distance_km,
		       f.lat::double precision AS latitude,
		       f.lng::double precision AS longitude,
		       COALESCE(NULLIF(f.city, ''), COALESCE(f.full_address, '')) AS location,
		       (SELECT url FROM facility_images
		         WHERE facility_id = f.id
		         ORDER BY is_cover DESC, sort_order ASC, created_at ASC
		         LIMIT 1) AS image
		  FROM facilities f
		` + whereClause + `
		 ORDER BY COALESCE(f.avg_rating, 0) DESC,
		          earth_distance(ll_to_earth(f.lat::double precision, f.lng::double precision),
		                         ll_to_earth($1, $2)) ASC
		 LIMIT $5 OFFSET $6
	`

	rows, err := r.db.Query(ctx, selectQuery, params.Lat, params.Lng, dbType, params.RadiusKm, params.Size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]dto.RecommendationItem, 0, params.Size)
	for rows.Next() {
		var item dto.RecommendationItem
		if err := rows.Scan(
			&item.ID,
			&item.Type,
			&item.Name,
			&item.Rating,
			&item.TotalReviews,
			&item.DistanceKm,
			&item.Latitude,
			&item.Longitude,
			&item.Location,
			&item.Image,
		); err != nil {
			return nil, 0, err
		}
		// Convert database MARRIAGE_HALL to public API 'HALL'
		item.Type = venuetype.API(item.Type)
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return items, total, nil
}
