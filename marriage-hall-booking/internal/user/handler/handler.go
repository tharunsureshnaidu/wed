package handler

import (
	"errors"
	"net/http"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/user/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
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
	get("PUT /api/v1/users/me", h.update)
	get("DELETE /api/v1/users/me", h.delete)
	get("GET /api/v1/users/me/favourites", h.listFavourites)
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
	response.OK(w, "Account deleted successfully", nil)
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

func (h *Handler) listFavourites(w http.ResponseWriter, r *http.Request) { h.favourites(w, r, "") }
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
