package handler

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/events"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/storage"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/venuetype"
)

const (
	TypeHotel = "HOTEL"
	TypeHall  = "MARRIAGE_HALL"
)

type Handler struct {
	repo       *repository.Repo
	signer     *jwt.Signer
	media      storage.Store
	compareSvc *service.CompareService
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
	return &Handler{
		repo:       repo,
		signer:     signer,
		media:      media,
		compareSvc: service.NewCompareService(repo),
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	owner := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, auth,
			middleware.RequireRole(domain.RoleHallOwner, domain.RoleAdmin))
	}

	// The detail is public, but knows its caller so an owner can still open
	// their own BLOCKED or REJECTED listing.
	detail := middleware.OptionalAuth(h.signer)(http.HandlerFunc(h.get))

	// Public reads.
	mux.HandleFunc("GET /api/v1/venues", h.listVenues)
	mux.Handle("GET /api/v1/venues/{id}", detail)
	mux.HandleFunc("GET /api/v1/facilities", h.list)
	mux.Handle("GET /api/v1/facilities/{id}", detail)
	// Side-by-side comparison. Public, like the other facility reads: comparing
	// venues is what a visitor does before signing up. The literal path beats
	// /{id} in ServeMux's specificity rules, so "compare" is never read as an id.
	mux.HandleFunc("GET /api/v1/facilities/compare", h.compare)
	mux.HandleFunc("GET /api/v1/venues/compare", h.compare)
	mux.HandleFunc("POST /api/v1/facilities/compare", h.compareVenues)
	mux.HandleFunc("POST /api/v1/venues/compare", h.compareVenues)

	// The event catalogue and a venue's own selection. The catalogue is public
	// (a visitor picks an occasion before signing up); setting the list is the
	// owner's, via requireOwner inside the handler.
	mux.HandleFunc("GET /api/v1/events", h.listEventTypes)
	mux.HandleFunc("GET /api/v1/facilities/{id}/events", h.getFacilityEvents)
	mux.Handle("PUT /api/v1/facilities/{id}/events", owner(h.setFacilityEvents))

	// Per-venue FAQs. The read is public and also embedded in the detail
	// response; writes are the owner's.
	mux.HandleFunc("GET /api/v1/facilities/{id}/faqs", h.listFaqs)
	mux.Handle("POST /api/v1/facilities/{id}/faqs", owner(h.addFaq))
	mux.Handle("PUT /api/v1/facilities/{id}/faqs/{childId}", owner(h.updateFaq))
	mux.Handle("DELETE /api/v1/facilities/{id}/faqs/{childId}", owner(h.deleteFaq))
	mux.HandleFunc("GET /api/v1/amenities", h.listAmenities)
	mux.HandleFunc("GET /api/v1/halls", h.listHalls)
	mux.HandleFunc("GET /api/v1/hotels", h.listHotels)
	mux.Handle("GET /api/v1/hotels/{id}", detail)
	mux.Handle("GET /api/v1/halls/{id}", detail)

	// Owner-only writes.
	mux.Handle("POST /api/v1/facilities", owner(h.create))
	mux.Handle("POST /api/v1/halls", owner(h.createHall))
	mux.Handle("POST /api/v1/hotels", owner(h.createHotel))
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

	// Images accepted via JSON
	RawImages  json.RawMessage `json:"images"`
	ImageURLs  []string        `json:"imageUrls"`
	ImageURL   *string         `json:"imageUrl"`
	CoverImage *string         `json:"coverImage"`
}

type imageInput struct {
	URL     string `json:"url"`
	IsCover bool   `json:"isCover"`
}

func (req *facilityReq) extractImageInputs() []imageInput {
	var results []imageInput
	seen := make(map[string]bool)

	add := func(url string, isCover bool) {
		u := strings.TrimSpace(url)
		if u != "" && !seen[u] {
			seen[u] = true
			results = append(results, imageInput{URL: u, IsCover: isCover})
		}
	}

	if req.CoverImage != nil && *req.CoverImage != "" {
		add(*req.CoverImage, true)
	}
	if req.ImageURL != nil && *req.ImageURL != "" {
		add(*req.ImageURL, len(results) == 0)
	}
	for _, u := range req.ImageURLs {
		add(u, len(results) == 0)
	}

	if len(req.RawImages) > 0 {
		var strArr []string
		if err := json.Unmarshal(req.RawImages, &strArr); err == nil {
			for _, u := range strArr {
				add(u, len(results) == 0)
			}
		} else {
			var objArr []struct {
				URL     string `json:"url"`
				IsCover bool   `json:"isCover"`
			}
			if err := json.Unmarshal(req.RawImages, &objArr); err == nil {
				for _, obj := range objArr {
					add(obj.URL, obj.IsCover || len(results) == 0)
				}
			} else {
				var singleStr string
				if err := json.Unmarshal(req.RawImages, &singleStr); err == nil {
					add(singleStr, len(results) == 0)
				}
			}
		}
	}

	hasCover := false
	for _, img := range results {
		if img.IsCover {
			hasCover = true
			break
		}
	}
	if !hasCover && len(results) > 0 {
		results[0].IsCover = true
	}

	return results
}

func (req facilityReq) toInput(ownerID int64) repository.CreateInput {
	return repository.CreateInput{
		OwnerID: ownerID, Name: req.Name, Description: req.Description,
		Type: venuetype.Stored(req.Type),
		City: req.City, FullAddress: req.FullAddress, State: req.State, Zipcode: req.Zipcode,
		Country: req.Country, Lat: req.Lat, Lng: req.Lng,
		StarRating: req.StarRating, CheckInTime: req.CheckInTime,
		CheckOutTime: req.CheckOutTime, CapacityPax: req.CapacityPax, AreaSqft: req.AreaSqft,
		BasePricePerDay: req.BasePricePerDay, SeatingCapacity: req.SeatingCapacity,
		FloatingCapacity: req.FloatingCapacity, MinBookingSize: req.MinBookingSize,
	}
}

func (h *Handler) createHall(w http.ResponseWriter, r *http.Request) {
	h.createWithType(w, r, TypeHall)
}

func (h *Handler) createHotel(w http.ResponseWriter, r *http.Request) {
	h.createWithType(w, r, TypeHotel)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	h.createWithType(w, r, "")
}

func (h *Handler) createWithType(w http.ResponseWriter, r *http.Request, forcedType string) {
	var req facilityReq
	// The listing form submits the venue and its photos together as multipart;
	// API clients send plain JSON. Both are accepted on the same endpoint.
	var uploads []*multipart.FileHeader
	if isMultipart(r) {
		var err error
		// ParseMultipartForm's limit only bounds memory; the rest spills to
		// disk unbounded. Cap the whole body: every image at the limit, plus
		// a megabyte for the form fields.
		r.Body = http.MaxBytesReader(w, r.Body, maxImages*maxImageSize+1<<20)
		if uploads, err = decodeMultipart(r, &req); err != nil {
			response.Error(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
			return
		}
	} else if !httpx.Decode(w, r, &req) {
		return
	}

	if forcedType != "" && (req.Type == "" || !venuetype.Valid(req.Type)) {
		req.Type = forcedType
	}

	var e validate.Errors
	e.Required("Name", req.Name)
	// HALL is the word the API speaks; MARRIAGE_HALL is still accepted so
	// saved requests and import scripts keep working.
	if !venuetype.Valid(req.Type) {
		e = append(e, "Type must be HOTEL or HALL")
	}
	if req.StarRating != nil && (*req.StarRating < 1 || *req.StarRating > 5) {
		e = append(e, "Star rating must be between 1 and 5")
	}
	if req.BasePricePerDay != nil && *req.BasePricePerDay < 0 {
		e = append(e, "Base price cannot be negative")
	}

	// Minimum 1 image is strictly required for Super Admin and Vendor
	jsonImages := req.extractImageInputs()
	if len(uploads) == 0 && len(jsonImages) == 0 {
		e = append(e, "At least one image is required")
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
		// A listing carries a contact real customers will use, so the vendor
		// confirms theirs before publishing one. This is the only thing
		// verification gates - a customer never has to verify to browse or
		// book. Checked before HasVendor so an unverified vendor is told what
		// to fix rather than being sent to create a business they already have.
		verified, err := h.repo.IsUserVerified(r.Context(), userID)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		if !verified {
			response.Error(w, http.StatusForbidden,
				"Verify your email or phone before listing a venue",
				"UNVERIFIED_ACCOUNT")
			return
		}

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
	// Save uploaded files to storage and database
	hasUploadCover := false
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
			isCover := i == 0
			if isCover {
				hasUploadCover = true
			}
			if _, err := h.repo.Pool().Exec(r.Context(),
				`INSERT INTO facility_images (facility_id, url, is_cover, sort_order)
				 VALUES ($1,$2,$3,$4)`, f.ID, url, isCover, i); err != nil {
				failed = append(failed, fh.Filename+": could not be recorded")
				continue
			}
		}
		if len(failed) > 0 {
			logger.Warn("facility: some upload images rejected", "facilityId", f.ID, "errors", strings.Join(failed, "; "))
		}
	}

	// Save JSON image URLs if provided
	if len(jsonImages) > 0 {
		startIndex := len(uploads)
		for i, img := range jsonImages {
			isCover := img.IsCover && !hasUploadCover
			if !hasUploadCover && i == 0 {
				isCover = true
				hasUploadCover = true
			}
			if _, err := h.repo.Pool().Exec(r.Context(),
				`INSERT INTO facility_images (facility_id, url, is_cover, sort_order, status)
				 VALUES ($1,$2,$3,$4,'READY')`, f.ID, img.URL, isCover, startIndex+i); err != nil {
				logger.Error("facility: could not record image url", "facilityId", f.ID, "url", img.URL, logger.Err(err))
			}
		}
	}

	if reloaded, err := h.repo.Get(r.Context(), f.ID); err == nil {
		f = reloaded
	}
	h.notifyCreated(r.Context(), f.ID, f.Name, userID)
	response.Created(w, "Facility created successfully", "/api/v1/facilities/"+f.ID, f)
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
	// A venue the list would hide is hidden here too, except from its owner
	// and admins. 404, not 403: a rejected listing is simply not there.
	userID, _ := middleware.UserID(r.Context())
	if !venuetype.Live(f.Status) && f.OwnerID != userID && !middleware.HasRole(r.Context(), domain.RoleAdmin) {
		response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
		return
	}
	// Same field as the list card, so the app reads one key on both screens.
	if here := parseUserLocation(r); here.OK {
		f.DistanceKm = here.distanceFrom(f.Lat, f.Lng)
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

func (h *Handler) list(w http.ResponseWriter, r *http.Request)       { h.listWithType(w, r, "") }
func (h *Handler) listHalls(w http.ResponseWriter, r *http.Request)  { h.listWithType(w, r, TypeHall) }
func (h *Handler) listHotels(w http.ResponseWriter, r *http.Request) { h.listWithType(w, r, TypeHotel) }

func (h *Handler) listVenues(w http.ResponseWriter, r *http.Request) {
	rawType := strings.TrimSpace(r.URL.Query().Get("type"))
	var dbType string
	if rawType != "" {
		upper := strings.ToUpper(rawType)
		if upper != "HALL" && upper != "HOTEL" {
			response.Error(w, http.StatusBadRequest, "Invalid type. Supported types are HALL and HOTEL.", "VALIDATION_ERROR")
			return
		}
		dbType = venuetype.Stored(upper)
	}

	eventType := strings.TrimSpace(r.URL.Query().Get("eventType"))
	if eventType == "" {
		eventType = strings.TrimSpace(r.URL.Query().Get("event_type"))
	}

	page, size := httpx.Page(r)
	items, total, err := h.repo.List(r.Context(), repository.ListFilter{
		Type:      dbType,
		EventType: eventType,
		Search:    r.URL.Query().Get("search"),
		City:      r.URL.Query().Get("city"),
		Page:      page,
		Size:      size,
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	if here := parseUserLocation(r); here.OK {
		for i := range items {
			items[i].DistanceKm = here.distanceFrom(items[i].Lat, items[i].Lng)
		}
	}

	venues := make([]repository.VenueResponse, len(items))
	for i, item := range items {
		venues[i] = item.ToVenueResponse()
	}

	response.OK(w, "Venues retrieved successfully", httpx.NewPaged(venues, page, size, total))
}

func (h *Handler) listWithType(w http.ResponseWriter, r *http.Request, forced string) {
	page, size := httpx.Page(r)
	t := forced
	if t == "" {
		t = venuetype.Stored(r.URL.Query().Get("type"))
	}
	eventType := strings.TrimSpace(r.URL.Query().Get("eventType"))
	if eventType == "" {
		eventType = strings.TrimSpace(r.URL.Query().Get("event_type"))
	}
	items, total, err := h.repo.List(r.Context(), repository.ListFilter{
		Type: t, EventType: eventType, Search: r.URL.Query().Get("search"), City: r.URL.Query().Get("city"),
		Page: page, Size: size,
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// Distance is per caller, not per venue, so it is filled in here rather
	// than scanned. No location on the request leaves every key absent.
	if here := parseUserLocation(r); here.OK {
		for i := range items {
			items[i].DistanceKm = here.distanceFrom(items[i].Lat, items[i].Lng)
		}
	}
	venues := make([]repository.VenueResponse, len(items))
	for i, item := range items {
		venues[i] = item.ToVenueResponse()
	}
	response.OK(w, "Facilities retrieved successfully", httpx.NewPaged(venues, page, size, total))
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
	venues := make([]repository.VenueResponse, len(items))
	for i, item := range items {
		venues[i] = item.ToVenueResponse()
	}
	response.OK(w, "Facilities retrieved successfully", httpx.NewPaged(venues, page, size, total))
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
	// amenity_id is a uuid: an unvalidated code here was a cast failure, a 500.
	amenityID := r.PathValue("amenityId")
	if !httpx.ValidUUID(amenityID) {
		response.Error(w, http.StatusBadRequest, "Invalid amenity id", "VALIDATION_ERROR")
		return
	}
	if err := h.repo.RemoveAmenity(r.Context(), id, amenityID); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Amenity removed", nil)
}
