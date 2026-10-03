package handler

import (
	"net/http"
	"strconv"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// dashboard is GET /api/v1/users/me/dashboard - the customer home screen.
//
// A customer and a vendor are separate entities with separate screens: a
// vendor is sent to /api/v1/vendors/dashboard instead of being shown an empty
// customer one, which would look like lost data rather than the wrong screen.
func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	if middleware.HasRole(r.Context(), domain.RoleHallOwner) &&
		!middleware.HasRole(r.Context(), domain.RoleCustomer) {
		response.Error(w, http.StatusForbidden,
			"This is the customer dashboard - vendors use /api/v1/vendors/dashboard",
			"WRONG_DASHBOARD")
		return
	}

	userID, _ := middleware.UserID(r.Context())
	lat, lng := optFloat(r, "lat"), optFloat(r, "lng")
	// Both or neither: a lone latitude is a client bug, not half a location.
	if lat == nil || lng == nil {
		lat, lng = nil, nil
	}

	d, err := h.repo.Dashboard(r.Context(), userID, lat, lng)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Dashboard retrieved successfully", d)
}

// optFloat reads an optional numeric query parameter. An unparseable or
// out-of-range value is treated as absent rather than an error: the dashboard
// is still worth serving without a location.
func optFloat(r *http.Request, key string) *float64 {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	switch key {
	case "lat":
		if v < -90 || v > 90 {
			return nil
		}
	case "lng":
		if v < -180 || v > 180 {
			return nil
		}
	}
	// 0,0 is Null Island, which is what an uninitialised location object
	// serialises to far more often than it is a real position.
	if v == 0 {
		return nil
	}
	return &v
}
