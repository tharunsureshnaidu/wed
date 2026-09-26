package handler

import (
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/eventtypes"
	"net/http"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// listEventTypes is GET /api/v1/events - the catalogue the app shows as a
// picker. Public: a visitor choosing a venue by occasion has no account yet.
func (h *Handler) listEventTypes(w http.ResponseWriter, r *http.Request) {
	category := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("category")))
	out := make([]eventtypes.EventType, 0, len(eventtypes.Catalogue))
	for _, e := range eventtypes.Catalogue {
		if category == "" || e.Category == category {
			out = append(out, e)
		}
	}
	response.OK(w, "Event types retrieved successfully", out)
}

// setFacilityEvents is PUT /api/v1/facilities/{id}/events - the owner declares
// what the venue hosts.
//
// A full replacement, not a merge: the screen is a checkbox list, and a PUT
// that only ever added would leave an unchecked box checked.
func (h *Handler) setFacilityEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req struct {
		Events []string `json:"events"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}

	// Validated before the write: an unknown code would sit in the table
	// forever, matching no filter and displaying as raw text.
	codes := make([]string, 0, len(req.Events))
	seen := map[string]bool{}
	for _, raw := range req.Events {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if code == "" {
			continue
		}
		if !eventtypes.ValidEventCode(code) {
			response.Error(w, http.StatusBadRequest,
				"Unknown event type: "+raw, "VALIDATION_ERROR")
			return
		}
		if seen[code] {
			continue
		}
		seen[code] = true
		codes = append(codes, code)
	}

	if err := h.repo.SetEvents(r.Context(), id, codes); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Event types updated successfully", eventtypes.Views(codes))
}

// getFacilityEvents is GET /api/v1/facilities/{id}/events. Public, like the
// rest of the facility reads.
func (h *Handler) getFacilityEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	codes, err := h.repo.EventsOf(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Event types retrieved successfully", eventtypes.Views(codes))
}
