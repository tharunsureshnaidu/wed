package handler

import (
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/internal/facility/repository"
	"github.com/tripfcatory/marriage-hall-booking/pkg/events"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/jwt"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
	"github.com/tripfcatory/marriage-hall-booking/pkg/storage"
	"github.com/tripfcatory/marriage-hall-booking/pkg/validate"
)

const (
	TypeHotel = "HOTEL"
	TypeHall  = "MARRIAGE_HALL"
)

type Handler struct {
	repo   *repository.Repo
	signer *jwt.Signer
	media  storage.Store
	// OnMediaUpload queues a file for the worker to upload. Nil means no
	// publisher (Kafka disabled), and uploads run inline on the request.
	OnMediaUpload func(ctx context.Context, m events.MediaUpload) error
	// OnFacilityCreated tells the platform a new listing needs approval. It
	// goes to admins, not the owner: the owner just created it and knows.
	OnFacilityCreated func(ctx context.Context, facilityID, name string, ownerID int64)
	// OnAmenitiesAdded announces new facilities at an existing venue to nearby
	// users. Fired only from the add-amenity route, not from create: a new
	// listing already has its own announcement.
	OnAmenitiesAdded func(ctx context.Context, facilityID string, names []string)
}

func New(repo *repository.Repo, signer *jwt.Signer, media storage.Store) *Handler {
	return &Handler{repo: repo, signer: signer, media: media}
}

func (h *Handler) Register(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	owner := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, auth,
			middleware.RequireRole(domain.RoleHallOwner, domain.RoleAdmin))
	}

	// Public reads.
	mux.HandleFunc("GET /api/v1/facilities", h.list)
	// Side-by-side comparison. Public, like the other facility reads: comparing
	// venues is what a visitor does before signing up. The literal path beats
	// /{id} in ServeMux's specificity rules, so "compare" is never read as an id.
	mux.HandleFunc("GET /api/v1/facilities/compare", h.compare)
	mux.HandleFunc("GET /api/v1/amenities", h.listAmenities)
	mux.HandleFunc("GET /api/v1/halls", h.listHalls)
	mux.HandleFunc("GET /api/v1/hotels/{id}", h.get)
	mux.HandleFunc("GET /api/v1/halls/{id}", h.get)

	// Owner-only writes.
	mux.Handle("POST /api/v1/facilities", owner(h.create))
	mux.Handle("PUT /api/v1/hotels/{id}", owner(h.update))
	mux.Handle("PUT /api/v1/halls/{id}", owner(h.update))
	mux.Handle("DELETE /api/v1/hotels/{id}", owner(h.delete))
	mux.Handle("DELETE /api/v1/halls/{id}", owner(h.delete))
	mux.Handle("GET /api/v1/hotels/my-hotels", owner(h.myHotels))
	mux.Handle("GET /api/v1/halls/my-halls", owner(h.myHalls))
	mux.Handle("POST /api/v1/facilities/{id}/amenities/{amenityId}", owner(h.addAmenity))
	mux.Handle("DELETE /api/v1/facilities/{id}/amenities/{amenityId}", owner(h.removeAmenity))
}

// requireOwner is the authorization boundary for every mutating facility route:
// the caller must own the facility, or be an admin. Without this any hall owner
// could edit or delete any other owner's listing.
func (h *Handler) requireOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return "", false
	}
	ownerID, err := h.repo.OwnerOf(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
		return "", false
	}
	if err != nil {
		httpx.Fail(w, err)
		return "", false
	}
	userID, _ := middleware.UserID(r.Context())
	// HasRole, not Role: Role returns only the primary role, so an admin whose
	// token lists ROLE_ADMIN second was refused access to a facility they are
	// entitled to edit.
	if ownerID != userID && !middleware.HasRole(r.Context(), domain.RoleAdmin) {
		response.Error(w, http.StatusForbidden, "You do not own this facility", "NOT_FACILITY_OWNER")
		return "", false
	}
	return id, true
}

type facilityReq struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Type        string  `json:"type"`
	City        *string `json:"city"`
	// Java's CreateFacilityRequest spells these fullAddress/zipcode.
	FullAddress *string  `json:"fullAddress"`
	State       *string  `json:"state"`
	Zipcode     *string  `json:"zipcode"`
	Country     *string  `json:"country"`
	Lat         *float64 `json:"lat"`
	Lng         *float64 `json:"lng"`

	StarRating   *int    `json:"starRating"`
	CheckInTime  *string `json:"checkInTime"`
	CheckOutTime *string `json:"checkOutTime"`

	// Amenity codes (PARKING, AIR_CONDITIONING...) or ids, as Java's
	// CreateFacilityRequest takes them. Sent as a JSON array, or repeated once
	// per checkbox in a multipart form.
	AmenityIDs []string `json:"amenityIds"`

	CapacityPax      *int     `json:"capacityPax"`
	AreaSqft         *int     `json:"areaSqft"`
	BasePricePerDay  *float64 `json:"basePricePerDay"`
	SeatingCapacity  *int     `json:"seatingCapacity"`
	FloatingCapacity *int     `json:"floatingCapacity"`
	MinBookingSize   *int     `json:"minBookingSize"`
}

func (req facilityReq) toInput(ownerID int64) repository.CreateInput {
	return repository.CreateInput{
		OwnerID: ownerID, Name: req.Name, Description: req.Description, Type: req.Type,
		City: req.City, FullAddress: req.FullAddress, State: req.State, Zipcode: req.Zipcode,
		Country: req.Country, Lat: req.Lat, Lng: req.Lng,
		StarRating: req.StarRating, CheckInTime: req.CheckInTime,
		CheckOutTime: req.CheckOutTime, CapacityPax: req.CapacityPax, AreaSqft: req.AreaSqft,
		BasePricePerDay: req.BasePricePerDay, SeatingCapacity: req.SeatingCapacity,
		FloatingCapacity: req.FloatingCapacity, MinBookingSize: req.MinBookingSize,
	}
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req facilityReq
	// The listing form submits the venue and its photos together as multipart;
	// API clients send plain JSON. Both are accepted on the same endpoint.
	var uploads []*multipart.FileHeader
	if isMultipart(r) {
		var err error
		if uploads, err = decodeMultipart(r, &req); err != nil {
			response.Error(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
			return
		}
	} else if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Name", req.Name)
	if req.Type != TypeHotel && req.Type != TypeHall {
		e = append(e, "Type must be HOTEL or MARRIAGE_HALL")
	}
	if req.StarRating != nil && (*req.StarRating < 1 || *req.StarRating > 5) {
		e = append(e, "Star rating must be between 1 and 5")
	}
	if req.BasePricePerDay != nil && *req.BasePricePerDay < 0 {
		e = append(e, "Base price cannot be negative")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	userID, _ := middleware.UserID(r.Context())
	// Only a registered vendor may list a property. Admin is exempt, matching
	// Java's assertIsVendor - an admin-created listing belongs to no vendor.
	// Java's message names POST /api/v1/vendors, which exists in neither
	// codebase; the business is created with PUT /api/v1/vendors/me.
	if !middleware.HasRole(r.Context(), "ROLE_ADMIN") {
		ok, err := h.repo.HasVendor(r.Context(), userID)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		if !ok {
			response.Error(w, http.StatusForbidden,
				"Only registered vendors can create a facility - create your vendor business first (PUT /api/v1/vendors/me)",
				"VENDOR_REQUIRED")
			return
		}
	}
	// Resolved before the insert: an unknown or inapplicable amenity is a 400,
	// and doing it after would leave a facility created by a request that then
	// failed.
	amenities, aerr := h.resolveAmenities(r.Context(), req.AmenityIDs, req.Type)
	if aerr != nil {
		var ae *amenityError
		if errors.As(aerr, &ae) {
			response.Error(w, http.StatusBadRequest, ae.msg, ae.code)
			return
		}
		httpx.Fail(w, aerr)
		return
	}

	f, err := h.repo.Create(r.Context(), req.toInput(userID))
	if err != nil {
		if isUniqueViolation(err) {
			response.Error(w, http.StatusConflict,
				"You already have a facility with this name", "FACILITY_EXISTS")
			return
		}
		httpx.Fail(w, err)
		return
	}

	// The create path does not announce: a brand-new listing is PENDING and
	// gets its own announcement when an admin approves it.
	if _, err := h.attachAmenities(r.Context(), f.ID, amenities); err != nil {
		httpx.Fail(w, err)
		return
	}

	// Images are saved after the row exists, so a rejected file cannot leave an
	// orphaned upload behind. A file that fails to save is reported but does
	// not undo the listing - re-uploading one photo is easier than re-entering
	// the whole venue.
	if len(uploads) > 0 {
		var vendorID string
		if f.VendorID != nil {
			vendorID = *f.VendorID
		}
		var failed []string
		for i, fh := range uploads {
			url, err := h.saveUpload(r.Context(), fh, storage.Image, f.ID, vendorID)
			if err != nil {
				failed = append(failed, err.Error())
				continue
			}
			if _, err := h.repo.Pool().Exec(r.Context(),
				`INSERT INTO facility_images (facility_id, url, is_cover, sort_order)
				 VALUES ($1,$2,$3,$4)`, f.ID, url, i == 0, i); err != nil {
				failed = append(failed, fh.Filename+": could not be recorded")
				continue
			}
		}
		if reloaded, err := h.repo.Get(r.Context(), f.ID); err == nil {
			f = reloaded
		}
		if len(failed) > 0 {
			h.notifyCreated(r.Context(), f.ID, f.Name, userID)
			response.OK(w, "Facility created, but some images were rejected: "+
				strings.Join(failed, "; "), f)
			return
		}
	}
	if reloaded, err := h.repo.Get(r.Context(), f.ID); err == nil {
		f = reloaded
	}
	h.notifyCreated(r.Context(), f.ID, f.Name, userID)
	response.OK(w, "Facility created successfully", f)
}

// notifyCreated fires the hook if one is wired. The listing exists either way:
// a notification that cannot be queued must not fail the create.
func (h *Handler) notifyCreated(ctx context.Context, id, name string, ownerID int64) {
	if h.OnFacilityCreated != nil {
		h.OnFacilityCreated(ctx, id, name, ownerID)
	}
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	f, err := h.repo.Get(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Facility retrieved successfully", f)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req facilityReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Name", req.Name)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	if err := h.repo.Update(r.Context(), id, req.toInput(0)); err != nil {
		httpx.Fail(w, err)
		return
	}
	f, err := h.repo.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Facility updated successfully", f)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	if err := h.repo.SoftDelete(r.Context(), id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Facility deleted successfully", nil)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request)      { h.listWithType(w, r, "") }
func (h *Handler) listHalls(w http.ResponseWriter, r *http.Request) { h.listWithType(w, r, TypeHall) }

func (h *Handler) listWithType(w http.ResponseWriter, r *http.Request, forced string) {
	page, size := httpx.Page(r)
	t := forced
	if t == "" {
		t = r.URL.Query().Get("type")
	}
	items, total, err := h.repo.List(r.Context(), repository.ListFilter{
		Type: t, Search: r.URL.Query().Get("search"), City: r.URL.Query().Get("city"),
		Page: page, Size: size,
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Facilities retrieved successfully", httpx.NewPaged(items, page, size, total))
}

func (h *Handler) myHotels(w http.ResponseWriter, r *http.Request) { h.mine(w, r, TypeHotel) }
func (h *Handler) myHalls(w http.ResponseWriter, r *http.Request)  { h.mine(w, r, TypeHall) }

func (h *Handler) mine(w http.ResponseWriter, r *http.Request, t string) {
	page, size := httpx.Page(r)
	userID, _ := middleware.UserID(r.Context())
	items, total, err := h.repo.List(r.Context(), repository.ListFilter{
		Type: t, OwnerID: userID, Search: r.URL.Query().Get("search"), Page: page, Size: size,
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Facilities retrieved successfully", httpx.NewPaged(items, page, size, total))
}

func (h *Handler) listAmenities(w http.ResponseWriter, r *http.Request) {
	list, err := h.repo.ListAmenities(r.Context(), r.URL.Query().Get("type"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Amenities retrieved successfully", list)
}

func (h *Handler) addAmenity(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	// A code or an id: the same resolver the create path uses, so both routes
	// agree on what is valid and on which type each amenity applies to.
	f, err := h.repo.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	list, aerr := h.resolveAmenities(r.Context(), []string{r.PathValue("amenityId")}, f.Type)
	if aerr != nil {
		var ae *amenityError
		if errors.As(aerr, &ae) {
			response.Error(w, http.StatusBadRequest, ae.msg, ae.code)
			return
		}
		httpx.Fail(w, aerr)
		return
	}
	added, err := h.attachAmenities(r.Context(), id, list)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// Only genuinely new amenities are announced; re-adding an existing one
	// changes nothing and must not notify anyone.
	if h.OnAmenitiesAdded != nil && len(added) > 0 {
		h.OnAmenitiesAdded(r.Context(), id, added)
	}
	response.OK(w, "Amenity added", nil)
}

func (h *Handler) removeAmenity(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	if err := h.repo.RemoveAmenity(r.Context(), id, r.PathValue("amenityId")); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Amenity removed", nil)
}
