package handler

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/internal/notification/repository"
	"github.com/tripfcatory/marriage-hall-booking/internal/notification/service"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/jwt"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
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
	mux.Handle("POST /api/v1/bookings/{id}/acknowledge", auth(http.HandlerFunc(h.acknowledge)))

	// Deliberately unauthenticated and GET: this is the link inside an SMS or
	// WhatsApp message, opened by a phone browser with no session. The random
	// token in the path is the credential.
	mux.HandleFunc("GET /ack/{token}", h.ackByToken)
	mux.HandleFunc("GET /decline/{token}", h.declineByToken)

	// Device tokens for push. Authenticated: a token is bound to the user who
	// registered it, so one account cannot be pushed to by another's install.
	mux.Handle("POST /api/v1/devices", auth(http.HandlerFunc(h.registerDevice)))
	mux.Handle("DELETE /api/v1/devices/{token}", auth(http.HandlerFunc(h.deleteDevice)))

	// The in-app notification feed. Read-your-own only: every query is scoped
	// to the caller, so an id from someone else's feed matches nothing.
	mux.Handle("GET /api/v1/notifications", auth(http.HandlerFunc(h.list)))
	mux.Handle("GET /api/v1/notifications/unread-count", auth(http.HandlerFunc(h.unreadCount)))
	// PUT, not POST: marking read is idempotent and the app fires it on scroll.
	// read-all is registered before {id} so the literal wins the match.
	mux.Handle("PUT /api/v1/notifications/read-all", auth(http.HandlerFunc(h.markAllRead)))
	mux.Handle("PUT /api/v1/notifications/{id}/read", auth(http.HandlerFunc(h.markRead)))

	// Location drives the radius targeting; the preference is how a user opts
	// out of it without unregistering their device.
	mux.Handle("PUT /api/v1/users/me/location", auth(http.HandlerFunc(h.setLocation)))
	mux.Handle("PUT /api/v1/users/me/geo-notifications", auth(http.HandlerFunc(h.setGeoPref)))
}

// declineByToken is the owner refusing the booking. Same public-endpoint
// posture as ackByToken: one uniform failure page, nothing probeable.
func (h *Handler) declineByToken(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" || len(token) > 64 {
		ackPage(w, http.StatusBadRequest, "Link is no longer valid",
			"It may have already been used, or it has expired.")
		return
	}
	bookingID, role, err := h.svc.DeclineByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			ackPage(w, http.StatusNotFound, "Link is no longer valid",
				"It may have already been used, or it has expired.")
			return
		}
		logger.Error("decline by token", logger.Err(err))
		ackPage(w, http.StatusInternalServerError, "Something went wrong",
			"Please try again in a moment.")
		return
	}
	logger.Info("notify: declined via link", "bookingId", bookingID, "role", role)
	ackPage(w, http.StatusOK, "Booking declined",
		"Our team has been notified and will contact the customer. You will not receive any more reminders for this booking.")
}

type deviceReq struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

func (h *Handler) registerDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Device token", req.Token)
	if len(req.Token) > 255 {
		e = append(e, "Device token is too long")
	}
	platform := strings.ToUpper(strings.TrimSpace(req.Platform))
	switch platform {
	case "ANDROID", "IOS", "WEB":
	default:
		e = append(e, "Platform must be ANDROID, IOS or WEB")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	if err := h.svc.RegisterDevice(r.Context(), userID, req.Token, platform); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Device registered for push notifications", nil)
}

func (h *Handler) deleteDevice(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	n, err := h.svc.DeleteDevice(r.Context(), userID, r.PathValue("token"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if n == 0 {
		response.Error(w, http.StatusNotFound, "Device not found", "NOT_FOUND")
		return
	}
	response.OK(w, "Device removed", nil)
}

// ackByToken redeems a one-tap link and renders a page, not JSON - the caller
// is a mobile browser, not the app.
//
// Every failure renders the same "link is no longer valid" page: the endpoint
// is public, so distinguishing an unknown token from an expired or already-used
// one would let anyone probe which tokens exist.
func (h *Handler) ackByToken(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" || len(token) > 64 {
		ackPage(w, http.StatusBadRequest, "Link is no longer valid",
			"It may have already been used, or it has expired.")
		return
	}
	bookingID, role, err := h.svc.AckByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			ackPage(w, http.StatusNotFound, "Link is no longer valid",
				"It may have already been used, or it has expired.")
			return
		}
		logger.Error("ack by token", logger.Err(err))
		ackPage(w, http.StatusInternalServerError, "Something went wrong",
			"Please try again in a moment.")
		return
	}
	logger.Info("notify: acknowledged via link", "bookingId", bookingID, "role", role)
	ackPage(w, http.StatusOK, "Thank you - booking confirmed",
		"You will not receive any more reminders for this booking.")
}

// ackPage renders a minimal self-contained page. No template file and no CSS
// framework: this is two lines of text on a phone, and an asset it had to
// fetch would be one more thing to serve and cache-bust.
func ackPage(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A link in a message may be prefetched by the messaging client; keep it
	// out of shared caches either way.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<style>
 body{font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
      display:flex;align-items:center;justify-content:center;min-height:100vh;
      margin:0;padding:24px;background:#f6f7f9;color:#1a1a1a}
 main{max-width:28rem;text-align:center;background:#fff;padding:2rem;
      border-radius:12px;box-shadow:0 1px 3px rgba(0,0,0,.1)}
 h1{font-size:1.25rem;margin:0 0 .5rem}
 p{margin:0;color:#555;line-height:1.5}
</style></head>
<body><main><h1>%s</h1><p>%s</p></main></body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(detail))
}

// acknowledge stops the retry loop for a booking. Ownership is enforced in the
// UPDATE's join rather than by a prior lookup, so a non-owner cannot silence
// someone else's notifications and gets the same 404 as a stranger - it reveals
// nothing about whether the booking exists.
func (h *Handler) acknowledge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid booking id", "VALIDATION_ERROR")
		return
	}
	// An admin clears the ops copy, anyone else clears the owner copy they own.
	// Checked in this order so an admin who also owns the venue still silences
	// the ops reminder they were actually sent.
	var (
		n   int64
		err error
	)
	if middleware.HasRole(r.Context(), domain.RoleAdmin) {
		n, err = h.svc.AckAdmin(r.Context(), id)
	} else {
		ownerID, _ := middleware.UserID(r.Context())
		n, err = h.svc.Ack(r.Context(), id, ownerID)
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if n == 0 {
		// Either not the owner, or already acknowledged. Both are a no-op from
		// the caller's point of view.
		response.Error(w, http.StatusNotFound, "No pending notifications for this booking", "NOT_FOUND")
		return
	}
	response.OK(w, "Booking acknowledged, reminders stopped", nil)
}

// --- location, for radius-targeted announcements ---

type locationReq struct {
	Lat *float64 `json:"lat"`
	Lng *float64 `json:"lng"`
	// DEVICE (a GPS fix) or CITY (a place the user picked). A client cannot
	// claim BOOKING - that source is only ever set by the server's inference.
	Source string `json:"source"`
}

func (h *Handler) setLocation(w http.ResponseWriter, r *http.Request) {
	var req locationReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	if req.Lat == nil || req.Lng == nil {
		e = append(e, "lat and lng are required")
	} else {
		// Reject impossible coordinates rather than storing them: a swapped
		// lat/lng silently puts the user in the wrong hemisphere and they
		// quietly stop matching any radius.
		if *req.Lat < -90 || *req.Lat > 90 {
			e = append(e, "lat must be between -90 and 90")
		}
		if *req.Lng < -180 || *req.Lng > 180 {
			e = append(e, "lng must be between -180 and 180")
		}
	}
	source := strings.ToUpper(strings.TrimSpace(req.Source))
	if source == "" {
		source = "DEVICE"
	}
	if source != "DEVICE" && source != "CITY" {
		e = append(e, "source must be DEVICE or CITY")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	if err := h.svc.SaveUserLocation(r.Context(), userID, *req.Lat, *req.Lng, source); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Location updated", nil)
}

type geoPrefReq struct {
	Enabled *bool `json:"enabled"`
}

func (h *Handler) setGeoPref(w http.ResponseWriter, r *http.Request) {
	var req geoPrefReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		response.Error(w, http.StatusBadRequest, "enabled is required", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	if err := h.svc.SetGeoNotifications(r.Context(), userID, *req.Enabled); err != nil {
		httpx.Fail(w, err)
		return
	}
	msg := "You will receive notifications about venues near you"
	if !*req.Enabled {
		msg = "You will no longer receive notifications about venues near you"
	}
	response.OK(w, msg, nil)
}
