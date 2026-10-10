package dto

import (
	facilityrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
)

// RecommendationParams represents validated input query parameters for the Recommendation API.
type RecommendationParams struct {
	Lat       float64
	Lng       float64
	Type      string  // "ALL", "HALL", or "HOTEL"
	RadiusKm  float64 // Maximum distance in km (default: 50.0)
	EventType string  // Optional event type filter, e.g. "WEDDING"
	Page      int     // Page number (default: 1)
	Size      int     // Page size (default: 20)
}

// RecommendationItem aliases VenueResponse to match the Venues API structure.
type RecommendationItem = facilityrepo.VenueResponse

// RecommendationData holds the payload returned in the ApiResponse matching the Venues API structure.
type RecommendationData struct {
	Content       []facilityrepo.VenueResponse `json:"content"`
	Page          int                          `json:"page"`
	Size          int                          `json:"size"`
	TotalElements int64                        `json:"totalElements"`
	TotalPages    int                          `json:"totalPages"`
}
