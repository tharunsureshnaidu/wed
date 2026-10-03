package handler

import (
	"net/http"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// decideReq carries the owner's reason. Optional on a confirm, and worth
// asking for on a reject: "the venue said no" with no explanation is the
// message the customer is left with otherwise.
type decideReq struct {
	Reason string `json:"reason"`
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) { h.decide(w, r, true) }
func (h *Handler) reject(w http.ResponseWriter, r *http.Request)  { h.decide(w, r, false) }

func (h *Handler) decide(w http.ResponseWriter, r *http.Request, confirm bool) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid booking id", "VALIDATION_ERROR")
		return
	}
	var req decideReq
	// A body is optional: confirming needs nothing more than the decision.
	if r.ContentLength > 0 && !httpx.Decode(w, r, &req) {
		return
	}

	actorID, _ := middleware.UserID(r.Context())
	b, err := h.svc.Decide(r.Context(), id, actorID,
		middleware.HasRole(r.Context(), domain.RoleAdmin),
		confirm, strings.TrimSpace(req.Reason))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	msg := "Booking confirmed successfully"
	if !confirm {
		msg = "Booking rejected successfully"
	}
	response.OK(w, msg, b)
}

// ownerBookings is the venue owner's inbox. Without it a booking request
// arrived as a notification and could then be found nowhere in the API.
func (h *Handler) ownerBookings(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)
	ownerID, _ := middleware.UserID(r.Context())
	status := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	if status != "" && !validBookingStatus(status) {
		response.Error(w, http.StatusBadRequest,
			"status must be one of: "+strings.Join(bookingStatuses, ", "), "VALIDATION_ERROR")
		return
	}
	items, total, err := h.svc.OwnerBookings(r.Context(), ownerID, status, page, size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Bookings retrieved successfully", httpx.NewPaged(items, page, size, total))
}

// bookingStatuses is what ?status= accepts. An unknown value is a 400 rather
// than an empty page, which would read as "no bookings" instead of a typo.
var bookingStatuses = []string{
	"PENDING", "CONFIRMED", "REJECTED", "CANCELLED", "COMPLETED", "EXPIRED",
}

func validBookingStatus(s string) bool {
	for _, v := range bookingStatuses {
		if v == s {
			return true
		}
	}
	return false
}
