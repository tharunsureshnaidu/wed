package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

type Handler struct {
	svc          *service.Service
	signer       *jwt.Signer
	maxPayloadMB int64
}

func New(svc *service.Service, signer *jwt.Signer) *Handler {
	maxMB := int64(5) // default 5 MB
	if v := os.Getenv("PRIVACY_POLICY_MAX_BYTES"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			maxMB = parsed / (1024 * 1024)
			if maxMB < 1 {
				maxMB = 1
			}
		}
	}
	return &Handler{svc: svc, signer: signer, maxPayloadMB: maxMB}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequireAuth(h.signer),
			middleware.RequireRole(domain.RoleAdmin))
	}

	// Protected Admin CRUD APIs — no {id} in URLs, only one record exists
	mux.Handle("POST /api/admin/privacy-policy", admin(h.create))
	mux.Handle("POST /api/v1/admin/privacy-policy", admin(h.create))

	mux.Handle("GET /api/admin/privacy-policy", admin(h.getAdmin))
	mux.Handle("GET /api/v1/admin/privacy-policy", admin(h.getAdmin))

	mux.Handle("PUT /api/admin/privacy-policy", admin(h.update))
	mux.Handle("PUT /api/v1/admin/privacy-policy", admin(h.update))

	mux.Handle("DELETE /api/admin/privacy-policy", admin(h.delete))
	mux.Handle("DELETE /api/v1/admin/privacy-policy", admin(h.delete))

	// Public User / Mobile API — NO TOKEN REQUIRED
	mux.HandleFunc("GET /api/privacy-policy", h.getPublic)
	mux.HandleFunc("GET /api/v1/privacy-policy", h.getPublic)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	var req service.CreateRequest
	if !h.decodeBody(w, r, &req) {
		return
	}

	policy, err := h.svc.Create(r.Context(), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.Created(w, "Privacy Policy created successfully", "", policy)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	var req service.UpdateRequest
	if !h.decodeBody(w, r, &req) {
		return
	}

	policy, err := h.svc.Update(r.Context(), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Privacy Policy updated successfully", policy)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	if err := h.svc.Delete(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Privacy Policy deleted successfully", nil)
}

func (h *Handler) getAdmin(w http.ResponseWriter, r *http.Request) {
	policy, err := h.svc.Get(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Privacy Policy retrieved successfully", policy)
}

// getPublic handles GET /api/privacy-policy.
// No token, JWT, or authentication is required for this endpoint.
func (h *Handler) getPublic(w http.ResponseWriter, r *http.Request) {
	policy, err := h.svc.Get(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Privacy Policy retrieved successfully", policy)
}

func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	maxBytes := h.maxPayloadMB * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) || strings.Contains(err.Error(), "request body too large") {
			response.Error(w, http.StatusRequestEntityTooLarge, "HTML payload too large", "PAYLOAD_TOO_LARGE")
			return false
		}
		response.Error(w, http.StatusBadRequest, "Malformed request body", "MALFORMED_JSON")
		return false
	}
	return true
}
