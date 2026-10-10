package service

import (
	"context"

	facilityrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/repository"
)

// Service defines business logic for recommendations.
type Service interface {
	GetRecommendations(ctx context.Context, params dto.RecommendationParams) (*dto.RecommendationData, string, error)
}

type serviceImpl struct {
	repo repository.Repository
}

func New(repo repository.Repository) Service {
	return &serviceImpl{repo: repo}
}

func (s *serviceImpl) GetRecommendations(ctx context.Context, params dto.RecommendationParams) (*dto.RecommendationData, string, error) {
	venues, total, err := s.repo.GetRecommendations(ctx, params)
	if err != nil {
		return nil, "", err
	}

	totalPages := 0
	if params.Size > 0 && total > 0 {
		totalPages = int((total + int64(params.Size) - 1) / int64(params.Size))
	}

	if venues == nil {
		venues = []facilityrepo.VenueResponse{}
	}

	data := &dto.RecommendationData{
		Content:       venues,
		Page:          params.Page,
		Size:          params.Size,
		TotalElements: total,
		TotalPages:    totalPages,
	}

	if total == 0 || len(venues) == 0 {
		return data, "No recommendations found in this area", nil
	}

	return data, "Recommendations fetched successfully", nil
}
