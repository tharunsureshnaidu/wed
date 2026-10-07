package handler

import (
	"net/http"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// decideReq carries the owner's decision (type: "confirm" | "reject") and reason.
type decideReq struct {
	Type            string `json:"type"`                      // "confirm" or "reject"
	Action          string `json:"action,omitempty"`          // alias for type
	Status          string `json:"status,omitempty"`          // alias for type
	Reason          string `json:"reason,omitempty"`
	RejectionReason string `json:"rejectionReason,omitempty"` // alias for reason
}

// status is POST/PUT/PATCH /api/v1/bookings/{id}/status - single unified endpoint
// to confirm or reject a booking based on the "type" field in the body.
func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	var req decideReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	confirm, ok := parseDecisionType(req.Type, req.Action, req.Status)
	if !ok {
		response.Error(w, http.StatusBadRequest,
			"type must be 'confirm' or 'reject'", "VALIDATION_ERROR")
		return
	}
	reason := req.Reason
	if reason == "" && req.RejectionReason != "" {
		reason = req.RejectionReason
	}
	h.decide(w, r, confirm, reason)
}

func parseDecisionType(typeVal, actionVal, statusVal string) (bool, bool) {
	candidates := []string{typeVal, actionVal, statusVal}
	for _, c := range candidates {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "confirm", "confirmed":
			return true, true
		case "reject", "rejected":
			return false, true
		}
	}
	return false, false
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	var req decideReq
	if r.ContentLength > 0 && !httpx.Decode(w, r, &req) {
		return
	}
	h.decide(w, r, true, req.Reason)
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	var req decideReq
	if r.ContentLength > 0 && !httpx.Decode(w, r, &req) {
		return
	}
	reason := req.Reason
	if reason == "" && req.RejectionReason != "" {
		reason = req.RejectionReason
	}
	h.decide(w, r, false, reason)
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request, confirm bool, reason string) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid booking id", "VALIDATION_ERROR")
		return
	}

	actorID, _ := middleware.UserID(r.Context())
	b, err := h.svc.Decide(r.Context(), id, actorID,
		middleware.HasRole(r.Context(), domain.RoleAdmin),
		confirm, strings.TrimSpace(reason))
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
