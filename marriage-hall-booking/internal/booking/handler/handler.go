package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/eventtypes"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
)

type Handler struct {
	svc    *service.Service
	signer *jwt.Signer

	// HostsEvent reports whether a venue accepts an event type. A hook rather
	// than an import so booking stays independent of the facility module,
	// matching the callback style used elsewhere. Nil disables the check.
	HostsEvent func(ctx context.Context, facilityID, eventCode string) (bool, error)
}

func New(svc *service.Service, signer *jwt.Signer) *Handler {
	return &Handler{svc: svc, signer: signer}
}

// isCustomer reports whether the context represents a customer who is allowed to
// make a booking: the user must hold ROLE_CUSTOMER, and must not hold administrative
// or venue-owner roles (neither ROLE_ADMIN, ROLE_SUPER_ADMIN, nor ROLE_HALL_OWNER).
func isCustomer(ctx context.Context) bool {
	return middleware.HasRole(ctx, domain.RoleCustomer) &&
		!middleware.HasRole(ctx, domain.RoleHallOwner) &&
		!middleware.HasRole(ctx, domain.RoleAdmin) &&
		!middleware.HasRole(ctx, domain.RoleSuperAdmin)
}

// requireCustomer restricts booking creation routes to customers only:
// venue owners and administrators cannot book halls or hotels.
func requireCustomer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isCustomer(r.Context()) {
			response.Error(w, http.StatusForbidden, "Only customers can book a hotel or hall", "CUSTOMER_ONLY")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) Register(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	customer := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, auth, requireCustomer)
	}
	mux.Handle("POST /api/v1/bookings/venues", customer(h.createVenue))
	mux.Handle("POST /api/v1/bookings", customer(h.createVenue))
	mux.Handle("POST /api/v1/bookings/halls", customer(h.createVenue))
	mux.Handle("POST /api/v1/bookings/hotels", customer(h.createHotel))
	mux.Handle("GET /api/v1/bookings", auth(http.HandlerFunc(h.list)))
	mux.Handle("GET /api/v1/bookings/reference/{ref}", auth(http.HandlerFunc(h.getByReference)))
	mux.Handle("GET /api/v1/bookings/{id}", auth(http.HandlerFunc(h.get)))
	mux.Handle("POST /api/v1/bookings/{id}/cancel", auth(http.HandlerFunc(h.cancel)))

	// The venue owner's side. Registered before {id} routes cannot shadow it:
	// Go's mux prefers the more specific pattern, and "owner" is a literal.
	owner := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, auth,
			middleware.RequireRole(domain.RoleHallOwner, domain.RoleAdmin))
	}
	mux.Handle("GET /api/v1/bookings/owner", owner(h.ownerBookings))
	mux.Handle("POST /api/v1/bookings/{id}/status", owner(h.status))
	mux.Handle("PUT /api/v1/bookings/{id}/status", owner(h.status))
	mux.Handle("PATCH /api/v1/bookings/{id}/status", owner(h.status))
	mux.Handle("POST /api/v1/bookings/{id}/confirm", owner(h.confirm))
	mux.Handle("POST /api/v1/bookings/{id}/reject", owner(h.reject))
	h.registerQuote(mux)
}

// venueReq is the booking form the app submits (date, guests, event type,
// rooms, etc.). Both MARRIAGE_HALL and HOTEL venues can be booked through this
// endpoint.
type venueReq struct {
	VenueID    string `json:"venueId"`
	FacilityID string `json:"facilityId"`
	HallID     string `json:"hallId"`
	HotelID    string `json:"hotelId"`

	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	CheckIn   string `json:"checkIn"`
	CheckOut  string `json:"checkOut"`
	EventDate string `json:"eventDate"` // legacy single-date form

	StartTime string `json:"startTime"` // HH:MM, optional
	EndTime   string `json:"endTime"`   // HH:MM, optional
	SlotType  string `json:"slotType"`  // MORNING, EVENING, FULL_DAY, optional

	GuestCount *int `json:"guestCount"` // optional
	// RoomCount is optional: how many guest rooms the party needs alongside
	// the venue. Not priced - the owner arranges it offline.
	RoomCount *int `json:"roomCount"`

	EventType   *string  `json:"eventType"`  // optional
	PackageIDs  []string `json:"packageIds"` // optional
	CouponCode       string   `json:"couponCode"`       // optional
	CouponCodes      []string `json:"couponCodes"`      // optional
	AdminCouponCode  string   `json:"adminCouponCode"`  // optional
	VendorCouponCode string   `json:"vendorCouponCode"` // optional

	IdempotentKey string `json:"idempotentKey"`
}

type hallReq = venueReq

// slotFor maps a time range onto the slot that availability is tracked by.
// Anything touching both halves of the day takes the whole day; otherwise it is
// the half it starts in. Midday is the boundary.
func slotFor(start, end string) string {
	if start == "" && end == "" {
		return "FULL_DAY"
	}
	if start != "" && end != "" {
		sh, _ := strconv.Atoi(start[:2])
		eh, _ := strconv.Atoi(end[:2])
		if eh <= sh { // runs past midnight
			return "FULL_DAY"
		}
		switch {
		case eh <= 12:
			return "MORNING"
		case sh >= 12:
			return "EVENING"
		default:
			return "FULL_DAY"
		}
	}
	if start != "" {
		sh, _ := strconv.Atoi(start[:2])
		if sh >= 12 {
			return "EVENING"
		}
		return "MORNING"
	}
	eh, _ := strconv.Atoi(end[:2])
	if eh <= 12 {
		return "MORNING"
	}
	return "EVENING"
}

// hhmm accepts "HH:MM" (and "HH:MM:SS", which is what a time input may send).
func hhmm(v string) (string, bool) {
	if v == "" {
		return "", true
	}
	t, err := time.Parse("15:04", v)
	if err != nil {
		if t, err = time.Parse("15:04:05", v); err != nil {
			return "", false
		}
	}
	return t.Format("15:04"), true
}

func (h *Handler) createVenue(w http.ResponseWriter, r *http.Request) {
	if !isCustomer(r.Context()) {
		response.Error(w, http.StatusForbidden, "Only customers can book a hotel or hall", "CUSTOMER_ONLY")
		return
	}
	var req venueReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	facilityID := req.VenueID
	if facilityID == "" {
		facilityID = req.FacilityID
	}
	if facilityID == "" {
		facilityID = req.HallID
	}
	if facilityID == "" {
		facilityID = req.HotelID
	}

	var e validate.Errors
	if !httpx.ValidUUID(facilityID) {
		if req.HallID != "" {
			e = append(e, "A valid hallId is required")
		} else if req.HotelID != "" {
			e = append(e, "A valid hotelId is required")
		} else {
			e = append(e, "A valid venueId is required")
		}
	}

	// startDate is the primary form; checkIn and eventDate are also accepted.
	rawStart := req.StartDate
	if rawStart == "" {
		rawStart = req.CheckIn
	}
	if rawStart == "" {
		rawStart = req.EventDate
	}
	rawEnd := req.EndDate
	if rawEnd == "" {
		rawEnd = req.CheckOut
	}
	if rawEnd == "" {
		rawEnd = rawStart
	}
	startDate, err := time.Parse("2006-01-02", rawStart)
	if err != nil {
		e = append(e, "startDate must be YYYY-MM-DD")
	}
	endDate, err2 := time.Parse("2006-01-02", rawEnd)
	if err2 != nil {
		e = append(e, "endDate must be YYYY-MM-DD")
	}
	if err == nil && err2 == nil && endDate.Before(startDate) {
		e = append(e, "endDate cannot be before startDate")
	}

	// startTime and endTime are optional.
	var startTime, endTime string
	if req.StartTime != "" {
		st, ok := hhmm(req.StartTime)
		if !ok {
			e = append(e, "startTime must be HH:MM")
		} else {
			startTime = st
		}
	}
	if req.EndTime != "" {
		et, ok := hhmm(req.EndTime)
		if !ok {
			e = append(e, "endTime must be HH:MM")
		} else {
			endTime = et
		}
	}

	// slotType is optional.
	slot := strings.ToUpper(strings.TrimSpace(req.SlotType))
	if slot != "" && slot != "MORNING" && slot != "EVENING" && slot != "FULL_DAY" {
		e = append(e, "slotType must be MORNING, EVENING or FULL_DAY")
	}
	if slot == "" {
		if startTime != "" || endTime != "" {
			slot = slotFor(startTime, endTime)
		} else {
			slot = "FULL_DAY"
		}
	}

	if req.GuestCount != nil && *req.GuestCount <= 0 {
		e = append(e, "guestCount must be positive")
	}
	// roomCount is optional: between 0 and 1000 if given.
	if req.RoomCount != nil && (*req.RoomCount < 0 || *req.RoomCount > 1000) {
		e = append(e, "roomCount must be between 0 and 1000")
	}
	if req.EventType != nil && strings.TrimSpace(*req.EventType) != "" && !eventtypes.ValidEventCode(*req.EventType) {
		e = append(e, "eventType is not a known event - see GET /api/v1/events")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	var eventType *string
	if req.EventType != nil && strings.TrimSpace(*req.EventType) != "" {
		v := strings.ToUpper(strings.TrimSpace(*req.EventType))
		eventType = &v

		if h.HostsEvent != nil {
			ok, err := h.HostsEvent(r.Context(), facilityID, v)
			if err != nil {
				httpx.Fail(w, err)
				return
			}
			if !ok {
				response.Error(w, http.StatusBadRequest,
					"This venue does not host "+eventtypes.EventName(v)+" events",
					"EVENT_NOT_HOSTED")
				return
			}
		}
	}

	codes := req.CouponCodes
	addCode := func(raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return
		}
		for _, c := range codes {
			if strings.EqualFold(c, trimmed) {
				return
			}
		}
		codes = append(codes, trimmed)
	}
	addCode(req.CouponCode)
	addCode(req.AdminCouponCode)
	addCode(req.VendorCouponCode)

	if len(codes) > 2 {
		response.Error(w, http.StatusBadRequest, "Maximum 2 coupons can be applied per booking", "COUPON_LIMIT_EXCEEDED")
		return
	}

	userID, _ := middleware.UserID(r.Context())
	b, err := h.svc.CreateVenueBooking(r.Context(), userID, service.VenueBookingRequest{
		FacilityID: facilityID, EventDate: startDate, EndDate: endDate,
		StartTime: startTime, EndTime: endTime,
		GuestCount: req.GuestCount, RoomCount: req.RoomCount, EventType: eventType,
		SlotType: slot, PackageIDs: req.PackageIDs, IdempotentKey: req.IdempotentKey,
		CouponCode:       strings.TrimSpace(req.CouponCode),
		CouponCodes:      codes,
		AdminCouponCode:  strings.TrimSpace(req.AdminCouponCode),
		VendorCouponCode: strings.TrimSpace(req.VendorCouponCode),
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Booking created successfully", b)
}

// createHall is an alias for createVenue for backward compatibility.
func (h *Handler) createHall(w http.ResponseWriter, r *http.Request) {
	h.createVenue(w, r)
}

type hotelReq struct {
	HotelID    string `json:"hotelId"`
	FacilityID string `json:"facilityId"`
	CheckIn    string `json:"checkIn"`
	CheckOut   string `json:"checkOut"`
	Rooms      []struct {
		RoomTypeID string `json:"roomTypeId"`
		Quantity   int    `json:"quantity"`
	} `json:"rooms"`
	IdempotentKey string  `json:"idempotentKey"`
	GuestName     *string `json:"guestName"`
	GuestEmail    *string `json:"guestEmail"`
	GuestPhone    *string `json:"guestPhone"`
	// CouponCode is optional; the discount is computed server-side.
	CouponCode       string   `json:"couponCode"`
	CouponCodes      []string `json:"couponCodes"`
	AdminCouponCode  string   `json:"adminCouponCode"`
	VendorCouponCode string   `json:"vendorCouponCode"`
}

func (h *Handler) createHotel(w http.ResponseWriter, r *http.Request) {
	if !isCustomer(r.Context()) {
		response.Error(w, http.StatusForbidden, "Only customers can book a hotel or hall", "CUSTOMER_ONLY")
		return
	}
	var req hotelReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	facilityID := req.HotelID
	if facilityID == "" {
		facilityID = req.FacilityID
	}

	var e validate.Errors
	if !httpx.ValidUUID(facilityID) {
		e = append(e, "A valid hotelId is required")
	}
	checkIn, err1 := time.Parse("2006-01-02", req.CheckIn)
	checkOut, err2 := time.Parse("2006-01-02", req.CheckOut)
	if err1 != nil || err2 != nil {
		e = append(e, "checkIn and checkOut must be YYYY-MM-DD")
	}
	if len(req.Rooms) == 0 {
		e = append(e, "At least one room is required")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	rooms := make([]repository.RoomLine, 0, len(req.Rooms))
	for _, room := range req.Rooms {
		rooms = append(rooms, repository.RoomLine{
			RoomTypeID: room.RoomTypeID, Quantity: room.Quantity,
		})
	}

	codes := req.CouponCodes
	addCode := func(raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return
		}
		for _, c := range codes {
			if strings.EqualFold(c, trimmed) {
				return
			}
		}
		codes = append(codes, trimmed)
	}
	addCode(req.CouponCode)
	addCode(req.AdminCouponCode)
	addCode(req.VendorCouponCode)

	if len(codes) > 2 {
		response.Error(w, http.StatusBadRequest, "Maximum 2 coupons can be applied per booking", "COUPON_LIMIT_EXCEEDED")
		return
	}

	userID, _ := middleware.UserID(r.Context())
	b, err := h.svc.CreateHotelBooking(r.Context(), userID, service.HotelBookingRequest{
		FacilityID: facilityID, CheckIn: checkIn, CheckOut: checkOut, Rooms: rooms,
		IdempotentKey: req.IdempotentKey, GuestName: req.GuestName,
		GuestEmail: req.GuestEmail, GuestPhone: req.GuestPhone,
		CouponCode:       strings.TrimSpace(req.CouponCode),
		CouponCodes:      codes,
		AdminCouponCode:  strings.TrimSpace(req.AdminCouponCode),
		VendorCouponCode: strings.TrimSpace(req.VendorCouponCode),
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Booking created successfully", b)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		response.Error(w, http.StatusBadRequest, "Invalid booking id", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	isAdmin := middleware.HasRole(r.Context(), domain.RoleAdmin) || middleware.HasRole(r.Context(), domain.RoleSuperAdmin)
	b, err := h.svc.Get(r.Context(), id, userID, isAdmin)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Booking retrieved successfully", b)
}

func (h *Handler) getByReference(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" {
		response.Error(w, http.StatusBadRequest, "Booking reference is required", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	isAdmin := middleware.HasRole(r.Context(), domain.RoleAdmin) || middleware.HasRole(r.Context(), domain.RoleSuperAdmin)
	b, err := h.svc.Get(r.Context(), ref, userID, isAdmin)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Booking retrieved successfully", b)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	ref := strings.TrimSpace(r.URL.Query().Get("referenceId"))
	if ref == "" {
		ref = strings.TrimSpace(r.URL.Query().Get("bookingReference"))
	}
	if ref == "" {
		ref = strings.TrimSpace(r.URL.Query().Get("reference"))
	}
	if ref != "" {
		isAdmin := middleware.HasRole(r.Context(), domain.RoleAdmin) || middleware.HasRole(r.Context(), domain.RoleSuperAdmin)
		b, err := h.svc.Get(r.Context(), ref, userID, isAdmin)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		response.OK(w, "Bookings retrieved successfully", httpx.NewPaged([]repository.Booking{*b}, 1, 1, 1))
		return
	}

	page, size := httpx.Page(r)
	items, total, err := h.svc.List(r.Context(), userID, page, size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Bookings retrieved successfully", httpx.NewPaged(items, page, size, total))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		response.Error(w, http.StatusBadRequest, "Invalid booking id", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	isAdmin := middleware.HasRole(r.Context(), domain.RoleAdmin) || middleware.HasRole(r.Context(), domain.RoleSuperAdmin)
	if err := h.svc.Cancel(r.Context(), id, userID, isAdmin); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Booking cancelled successfully", nil)
}
