package handler

import (
	"encoding/json"
	"errors"
	"fmt"
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
	maxMB := int64(5) // default 5 MB to support large HTML documents
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

	// Protected Admin APIs
	mux.Handle("POST /api/admin/privacy-policy", admin(h.create))
	mux.Handle("POST /api/v1/admin/privacy-policy", admin(h.create))

	mux.Handle("GET /api/admin/privacy-policy", admin(h.getAll))
	mux.Handle("GET /api/v1/admin/privacy-policy", admin(h.getAll))

	mux.Handle("GET /api/admin/privacy-policy/{id}", admin(h.getByID))
	mux.Handle("GET /api/v1/admin/privacy-policy/{id}", admin(h.getByID))

	mux.Handle("PUT /api/admin/privacy-policy/{id}", admin(h.update))
	mux.Handle("PUT /api/v1/admin/privacy-policy/{id}", admin(h.update))

	mux.Handle("PATCH /api/admin/privacy-policy/{id}/activate", admin(h.activate))
	mux.Handle("PATCH /api/v1/admin/privacy-policy/{id}/activate", admin(h.activate))

	mux.Handle("PATCH /api/admin/privacy-policy/{id}/deactivate", admin(h.deactivate))
	mux.Handle("PATCH /api/v1/admin/privacy-policy/{id}/deactivate", admin(h.deactivate))

	// Public User / Mobile API — NO TOKEN REQUIRED
	mux.HandleFunc("GET /api/privacy-policy", h.getActive)
	mux.HandleFunc("GET /api/v1/privacy-policy", h.getActive)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	var req service.CreateRequest
	if !h.decodeBody(w, r, &req) {
		return
	}

	policy, err := h.svc.Create(r.Context(), req, &actorID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	location := fmt.Sprintf("/api/v1/admin/privacy-policy/%d", policy.ID)
	response.Created(w, "Privacy Policy created successfully", location, policy)
}

func (h *Handler) getAll(w http.ResponseWriter, r *http.Request) {
	policies, err := h.svc.GetAll(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Privacy policies retrieved successfully", policies)
}

func (h *Handler) getByID(w http.ResponseWriter, r *http.Request) {
	id, err := h.parseID(r)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Invalid policy ID", "INVALID_ID")
		return
	}

	policy, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Privacy Policy retrieved successfully", policy)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	id, err := h.parseID(r)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Invalid policy ID", "INVALID_ID")
		return
	}

	var req service.UpdateRequest
	if !h.decodeBody(w, r, &req) {
		return
	}

	policy, err := h.svc.Update(r.Context(), id, req, &actorID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Privacy Policy updated successfully", policy)
}

func (h *Handler) activate(w http.ResponseWriter, r *http.Request) {
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	id, err := h.parseID(r)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Invalid policy ID", "INVALID_ID")
		return
	}

	policy, err := h.svc.Activate(r.Context(), id, &actorID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Privacy Policy activated successfully", policy)
}

func (h *Handler) deactivate(w http.ResponseWriter, r *http.Request) {
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	id, err := h.parseID(r)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Invalid policy ID", "INVALID_ID")
		return
	}

	policy, err := h.svc.Deactivate(r.Context(), id, &actorID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Privacy Policy deactivated successfully", policy)
}

// getActive handles GET /api/privacy-policy and GET /api/v1/privacy-policy.
// No token or authentication is required. Returns only the active Privacy Policy.
func (h *Handler) getActive(w http.ResponseWriter, r *http.Request) {
	policy, err := h.svc.GetActive(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Privacy Policy retrieved successfully", policy)
}

func (h *Handler) parseID(r *http.Request) (int64, error) {
	val := r.PathValue("id")
	if val == "" {
		return 0, errors.New("missing policy id")
	}
	id, err := strconv.ParseInt(val, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid policy id")
	}
	return id, nil
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
