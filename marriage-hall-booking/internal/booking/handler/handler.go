package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/internal/booking/repository"
	"github.com/tripfcatory/marriage-hall-booking/internal/booking/service"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/jwt"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
	"github.com/tripfcatory/marriage-hall-booking/pkg/validate"
)

type Handler struct {
	svc    *service.Service
	signer *jwt.Signer
}

func New(svc *service.Service, signer *jwt.Signer) *Handler {
	return &Handler{svc: svc, signer: signer}
}

func (h *Handler) Register(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	mux.Handle("POST /api/v1/bookings/halls", auth(http.HandlerFunc(h.createHall)))
	mux.Handle("POST /api/v1/bookings/hotels", auth(http.HandlerFunc(h.createHotel)))
	mux.Handle("GET /api/v1/bookings", auth(http.HandlerFunc(h.list)))
	mux.Handle("GET /api/v1/bookings/{id}", auth(http.HandlerFunc(h.get)))
	mux.Handle("POST /api/v1/bookings/{id}/cancel", auth(http.HandlerFunc(h.cancel)))
	h.registerQuote(mux)
}

// hallReq is the booking form the app submits (screen 01: date, guests, event
// type). An event can run across midnight or over several days, so it carries a
// date range and times rather than a single date.
//
// eventDate/slotType are still accepted: they are what every existing client
// sends, and dropping them would break those callers for no gain.
type hallReq struct {
	HallID     string   `json:"hallId"`
	FacilityID string   `json:"facilityId"`
	StartDate  string   `json:"startDate"`
	EndDate    string   `json:"endDate"`
	StartTime  string   `json:"startTime"` // HH:MM, optional
	EndTime    string   `json:"endTime"`   // HH:MM, optional
	GuestCount *int     `json:"guestCount"`
	EventType  *string  `json:"eventType"`
	PackageIDs []string `json:"packageIds"`

	// Legacy single-date form.
	EventDate string `json:"eventDate"`
	SlotType  string `json:"slotType"`

	IdempotentKey string `json:"idempotentKey"`
}

// eventTypes are the options on the booking screen.
var eventTypes = map[string]bool{
	"WEDDING": true, "RECEPTION": true, "ENGAGEMENT": true,
	"BIRTHDAY": true, "OTHER": true,
}

// slotFor maps a time range onto the slot that availability is tracked by.
// Anything touching both halves of the day takes the whole day; otherwise it is
// the half it starts in. Midday is the boundary.
func slotFor(start, end string) string {
	if start == "" || end == "" {
		return "FULL_DAY"
	}
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

func (h *Handler) createHall(w http.ResponseWriter, r *http.Request) {
	var req hallReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	facilityID := req.HallID
	if facilityID == "" {
		facilityID = req.FacilityID
	}

	var e validate.Errors
	if !httpx.ValidUUID(facilityID) {
		e = append(e, "A valid hallId is required")
	}

	// startDate is the new form; eventDate the old one. Either is accepted.
	rawStart, rawEnd := req.StartDate, req.EndDate
	if rawStart == "" {
		rawStart = req.EventDate
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

	startTime, ok1 := hhmm(req.StartTime)
	endTime, ok2 := hhmm(req.EndTime)
	if !ok1 || !ok2 {
		e = append(e, "startTime and endTime must be HH:MM")
	}
	if (startTime == "") != (endTime == "") {
		e = append(e, "startTime and endTime must be given together")
	}

	// An explicit time range decides the slot; otherwise the caller's slotType
	// stands, defaulting to the whole day.
	slot := req.SlotType
	if startTime != "" {
		slot = slotFor(startTime, endTime)
	} else if slot == "" {
		slot = "FULL_DAY"
	}
	if slot != "MORNING" && slot != "EVENING" && slot != "FULL_DAY" {
		e = append(e, "slotType must be MORNING, EVENING or FULL_DAY")
	}

	if req.GuestCount != nil && *req.GuestCount <= 0 {
		e = append(e, "guestCount must be positive")
	}
	if req.EventType != nil && !eventTypes[strings.ToUpper(*req.EventType)] {
		e = append(e, "eventType must be WEDDING, RECEPTION, ENGAGEMENT, BIRTHDAY or OTHER")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	var eventType *string
	if req.EventType != nil {
		v := strings.ToUpper(*req.EventType)
		eventType = &v
	}

	userID, _ := middleware.UserID(r.Context())
	b, err := h.svc.CreateHallBooking(r.Context(), userID, service.HallBookingRequest{
		FacilityID: facilityID, EventDate: startDate, EndDate: endDate,
		StartTime: startTime, EndTime: endTime,
		GuestCount: req.GuestCount, EventType: eventType,
		SlotType: slot, PackageIDs: req.PackageIDs, IdempotentKey: req.IdempotentKey,
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Booking created successfully", b)
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
}

func (h *Handler) createHotel(w http.ResponseWriter, r *http.Request) {
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

	userID, _ := middleware.UserID(r.Context())
	b, err := h.svc.CreateHotelBooking(r.Context(), userID, service.HotelBookingRequest{
		FacilityID: facilityID, CheckIn: checkIn, CheckOut: checkOut, Rooms: rooms,
		IdempotentKey: req.IdempotentKey, GuestName: req.GuestName,
		GuestEmail: req.GuestEmail, GuestPhone: req.GuestPhone,
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Booking created successfully", b)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid booking id", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	b, err := h.svc.Get(r.Context(), id, userID, middleware.HasRole(r.Context(), domain.RoleAdmin))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Booking retrieved successfully", b)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)
	userID, _ := middleware.UserID(r.Context())
	items, total, err := h.svc.List(r.Context(), userID, page, size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Bookings retrieved successfully", httpx.NewPaged(items, page, size, total))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid booking id", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	if err := h.svc.Cancel(r.Context(), id, userID, middleware.HasRole(r.Context(), domain.RoleAdmin)); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Booking cancelled successfully", nil)
}
