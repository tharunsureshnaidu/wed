package dto

// RecommendationParams represents validated input query parameters for the Recommendation API.
type RecommendationParams struct {
	Lat      float64
	Lng      float64
	Type     string  // "ALL", "HALL", or "HOTEL"
	RadiusKm float64 // Maximum distance in km (default: 50.0)
	Page     int     // 1-based page number (default: 1)
	Size     int     // Page size (default: 20)
}

// RecommendationItem represents a single recommended Hall or Hotel venue.
type RecommendationItem struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"` // "HALL" or "HOTEL"
	Name         string   `json:"name"`
	Rating       float64  `json:"rating"`
	TotalReviews int      `json:"totalReviews"`
	DistanceKm   float64  `json:"distanceKm"`
	Latitude     float64  `json:"latitude"`
	Longitude    float64  `json:"longitude"`
	Location     string   `json:"location"`
	Image        *string  `json:"image"`
}

// Pagination metadata matching the project specification.
type Pagination struct {
	Page       int   `json:"page"`
	Size       int   `json:"size"`
	TotalItems int64 `json:"totalItems"`
	TotalPages int   `json:"totalPages"`
}

// RecommendationData holds the payload returned in the ApiResponse.
type RecommendationData struct {
	Items      []RecommendationItem `json:"items"`
	Pagination Pagination           `json:"pagination"`
}
