package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/user/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

type Handler struct {
	repo   *repository.Repo
	signer *jwt.Signer
}

func New(repo *repository.Repo, signer *jwt.Signer) *Handler {
	return &Handler{repo: repo, signer: signer}
}

func (h *Handler) Register(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	get := func(p string, fn http.HandlerFunc) { mux.Handle(p, auth(fn)) }

	get("GET /api/v1/users/me", h.get)
	get("GET /api/v1/users/me/dashboard", h.dashboard)
	get("PUT /api/v1/users/me", h.update)
	get("DELETE /api/v1/users/me", h.delete)
	get("GET /api/v1/users/me/favourites", h.listFavourites)
	get("POST /api/v1/users/me/favourites", h.toggleFavourite)
	get("POST /api/v1/users/me/favourites/{facilityId}", h.addFavourite)
	get("DELETE /api/v1/users/me/favourites/{facilityId}", h.removeFavourite)

	// The Postman collection still has the pre-merge hotel/hall split. The Java
	// controller unified them onto favourites/{facilityId}; these aliases keep
	// the older collection working against the same storage.
	get("POST /api/v1/users/me/favourites/hotels/{facilityId}", h.addFavourite)
	get("DELETE /api/v1/users/me/favourites/hotels/{facilityId}", h.removeFavourite)
	get("GET /api/v1/users/me/favourites/hotels", h.listHotelFavourites)
	get("POST /api/v1/users/me/favourites/halls/{facilityId}", h.addFavourite)
	get("DELETE /api/v1/users/me/favourites/halls/{facilityId}", h.removeFavourite)
	get("GET /api/v1/users/me/favourites/halls", h.listHallFavourites)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	p, err := h.repo.Get(r.Context(), userID)
	if errors.Is(err, repository.ErrNotFound) {
		response.Error(w, http.StatusNotFound, "Profile not found", "PROFILE_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Profile retrieved successfully", p)
}

type updateReq struct {
	FirstName string  `json:"firstName"`
	LastName  *string `json:"lastName"`
	Bio       *string `json:"bio"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var req updateReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("First name", req.FirstName)
	e.Length("First name", req.FirstName, 1, 100)
	if req.LastName != nil && len(*req.LastName) > 100 {
		e = append(e, "Last name must be at most 100 characters")
	}
	if req.Bio != nil && len(*req.Bio) > 500 {
		e = append(e, "Bio must be at most 500 characters")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	userID, _ := middleware.UserID(r.Context())
	if err := h.repo.Update(r.Context(), userID, req.FirstName, req.LastName, req.Bio); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.Error(w, http.StatusNotFound, "Profile not found", "PROFILE_NOT_FOUND")
			return
		}
		httpx.Fail(w, err)
		return
	}
	p, err := h.repo.Get(r.Context(), userID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Profile updated successfully", p)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	if err := h.repo.SoftDelete(r.Context(), userID); err != nil {
		httpx.Fail(w, err)
		return
	}
	// The refresh tokens died in SoftDelete; the access token in hand must too.
	middleware.RevokeAccessTokens(r.Context(), userID)
	response.OK(w, "Account deleted successfully", nil)
}

type favouriteToggleReq struct {
	EntityID   string `json:"entityId"`
	FacilityID string `json:"facilityId"`
	Type       string `json:"type"`
	Favorite   *bool  `json:"favorite"`
}

type favouriteToggleData struct {
	EntityID string `json:"entityId"`
	Type     string `json:"type"`
	Favorite bool   `json:"favorite"`
}

func (h *Handler) toggleFavourite(w http.ResponseWriter, r *http.Request) {
	var req favouriteToggleReq
	if !httpx.Decode(w, r, &req) {
		return
	}

	if req.EntityID == "" && req.FacilityID != "" {
		req.EntityID = req.FacilityID
	}

	var e validate.Errors
	trimmedID := strings.TrimSpace(req.EntityID)
	if trimmedID == "" {
		e.Required("entityId", req.EntityID)
	} else if !httpx.ValidUUID(trimmedID) {
		e = append(e, "Invalid entity id")
	}

	rawType := strings.TrimSpace(req.Type)
	if rawType == "" {
		e.Required("type", req.Type)
	} else {
		upper := strings.ToUpper(rawType)
		if upper != "HALL" && upper != "HOTEL" {
			e = append(e, "Invalid type. Supported types are HALL and HOTEL.")
		}
	}

	if req.Favorite == nil {
		e = append(e, "favorite is required")
	}

	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	userID, _ := middleware.UserID(r.Context())
	reqType := strings.ToUpper(rawType)
	favorite := *req.Favorite

	if favorite {
		actualStoredType, err := h.repo.GetFacilityStoredType(r.Context(), trimmedID)
		if errors.Is(err, repository.ErrNotFound) {
			response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
			return
		}
		if err != nil {
			httpx.Fail(w, err)
			return
		}

		expectedStoredType := venuetype.Stored(reqType)
		if actualStoredType != expectedStoredType {
			response.Error(w, http.StatusBadRequest, "Invalid entity type for facility", "VALIDATION_ERROR")
			return
		}

		if err := h.repo.AddFavourite(r.Context(), userID, trimmedID); err != nil {
			httpx.Fail(w, err)
			return
		}

		response.OK(w, "Added to favourites successfully", favouriteToggleData{
			EntityID: trimmedID,
			Type:     reqType,
			Favorite: true,
		})
		return
	}

	storedType := venuetype.Stored(reqType)
	if err := h.repo.RemoveFavouriteTyped(r.Context(), userID, trimmedID, storedType); err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Removed from favourites successfully", favouriteToggleData{
		EntityID: trimmedID,
		Type:     reqType,
		Favorite: false,
	})
}

func (h *Handler) addFavourite(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	id := r.PathValue("facilityId")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	if err := h.repo.AddFavourite(r.Context(), userID, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Added to favourites", nil)
}

func (h *Handler) removeFavourite(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	id := r.PathValue("facilityId")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	if err := h.repo.RemoveFavourite(r.Context(), userID, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Removed from favourites", nil)
}

func (h *Handler) listFavourites(w http.ResponseWriter, r *http.Request) {
	rawType := strings.TrimSpace(r.URL.Query().Get("type"))
	if rawType == "" {
		h.favourites(w, r, "")
		return
	}
	upper := strings.ToUpper(rawType)
	if upper != "HALL" && upper != "HOTEL" && upper != "MARRIAGE_HALL" {
		response.Error(w, http.StatusBadRequest, "Invalid type. Supported types are HALL and HOTEL.", "VALIDATION_ERROR")
		return
	}
	h.favourites(w, r, venuetype.Stored(upper))
}

func (h *Handler) listHotelFavourites(w http.ResponseWriter, r *http.Request) {
	h.favourites(w, r, "HOTEL")
}

func (h *Handler) listHallFavourites(w http.ResponseWriter, r *http.Request) {
	h.favourites(w, r, "MARRIAGE_HALL")
}

func (h *Handler) favourites(w http.ResponseWriter, r *http.Request, typeFilter string) {
	userID, _ := middleware.UserID(r.Context())
	list, err := h.repo.ListFavourites(r.Context(), userID, typeFilter)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Favourites retrieved successfully", list)
}
