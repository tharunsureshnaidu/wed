package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// Handler serves the single public recommendation API.
type Handler struct {
	svc service.Service
}

func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// Register registers the public recommendation route on the ServeMux outside authentication middleware.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/public/recommendations", h.GetRecommendations)
}

// GetRecommendations handles GET /api/v1/public/recommendations.
func (h *Handler) GetRecommendations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// 1. Validate latitude
	latStr := q.Get("lat")
	if latStr == "" {
		response.Error(w, http.StatusBadRequest, "Invalid latitude", "VALIDATION_ERROR")
		return
	}
	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil || lat < -90 || lat > 90 {
		response.Error(w, http.StatusBadRequest, "Invalid latitude", "VALIDATION_ERROR")
		return
	}

	// 2. Validate longitude
	lngStr := q.Get("lng")
	if lngStr == "" {
		response.Error(w, http.StatusBadRequest, "Invalid longitude", "VALIDATION_ERROR")
		return
	}
	lng, err := strconv.ParseFloat(lngStr, 64)
	if err != nil || lng < -180 || lng > 180 {
		response.Error(w, http.StatusBadRequest, "Invalid longitude", "VALIDATION_ERROR")
		return
	}

	// 3. Validate venue type
	rawType := strings.ToUpper(strings.TrimSpace(q.Get("type")))
	if rawType != "ALL" && rawType != "HALL" && rawType != "HOTEL" {
		response.Error(w, http.StatusBadRequest, "Invalid venue type. Allowed values: ALL, HALL, HOTEL", "VALIDATION_ERROR")
		return
	}

	// 4. Validate radiusKm (optional, default 50 km)
	radiusKm := 50.0
	if radStr := q.Get("radiusKm"); radStr != "" {
		rVal, err := strconv.ParseFloat(radStr, 64)
		if err != nil || rVal <= 0 || rVal > 500 {
			response.Error(w, http.StatusBadRequest, "Invalid radiusKm", "VALIDATION_ERROR")
			return
		}
		radiusKm = rVal
	}

	// 5. Validate page (optional, 0-based like every other list, default 0)
	page := 0
	if pageStr := q.Get("page"); pageStr != "" {
		pVal, err := strconv.Atoi(pageStr)
		if err != nil || pVal < 0 {
			response.Error(w, http.StatusBadRequest, "Invalid page number", "VALIDATION_ERROR")
			return
		}
		page = pVal
	}

	// 6. Validate size (optional, default 20, max 100)
	size := 20
	if sizeStr := q.Get("size"); sizeStr != "" {
		sVal, err := strconv.Atoi(sizeStr)
		if err != nil || sVal < 1 || sVal > 100 {
			response.Error(w, http.StatusBadRequest, "Invalid page size", "VALIDATION_ERROR")
			return
		}
		size = sVal
	}

	eventType := strings.ToUpper(strings.TrimSpace(q.Get("eventType")))
	if eventType == "" {
		eventType = strings.ToUpper(strings.TrimSpace(q.Get("event_type")))
	}

	params := dto.RecommendationParams{
		Lat:       lat,
		Lng:       lng,
		Type:      rawType,
		RadiusKm:  radiusKm,
		EventType: eventType,
		Page:      page,
		Size:      size,
	}

	data, msg, err := h.svc.GetRecommendations(r.Context(), params)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, msg, data)
}
