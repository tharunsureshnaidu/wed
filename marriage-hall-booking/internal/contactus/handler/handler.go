package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/contactus/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// Handler serves public and admin Contact Us HTTP endpoints.
type Handler struct {
	svc    service.Service
	signer *jwt.Signer
}

// New creates a new Contact Us handler with database dependencies.
func New(db *pgxpool.Pool, signer *jwt.Signer) *Handler {
	repo := repository.New(db)
	svc := service.New(repo)
	return NewWithService(svc, signer)
}

// NewWithService constructs a Contact Us handler with an injected service interface.
func NewWithService(svc service.Service, signer *jwt.Signer) *Handler {
	return &Handler{
		svc:    svc,
		signer: signer,
	}
}

// Register mounts the Contact Us endpoints on the provided ServeMux.
func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequireAuth(h.signer),
			middleware.RequireRole(domain.RoleAdmin, domain.RoleSuperAdmin))
	}

	// Public API — Unauthenticated submission
	mux.HandleFunc("POST /api/v1/contact-us", h.create)
	mux.HandleFunc("POST /api/contact-us", h.create)

	// Admin APIs — ROLE_ADMIN and ROLE_SUPER_ADMIN only
	mux.Handle("GET /api/v1/contact-us", admin(h.list))
	mux.Handle("GET /api/v1/contact-us/{id}", admin(h.getByID))
	mux.Handle("PUT /api/v1/contact-us/{id}", admin(h.update))
	mux.Handle("DELETE /api/v1/contact-us/{id}", admin(h.delete))

	// Unversioned aliases for admin endpoints
	mux.Handle("GET /api/contact-us", admin(h.list))
	mux.Handle("GET /api/contact-us/{id}", admin(h.getByID))
	mux.Handle("PUT /api/contact-us/{id}", admin(h.update))
	mux.Handle("DELETE /api/contact-us/{id}", admin(h.delete))
}

// create processes public contact form submissions.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateContactRequest
	if !httpx.Decode(w, r, &req) {
		return
	}

	res, err := h.svc.Create(r.Context(), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.Created(w, "Contact inquiry submitted successfully", "/api/v1/contact-us/"+res.ID, res)
}

// list retrieves a paginated and status-filtered list of contact requests for administrators.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)

	// Support custom size / limit
	if lStr := strings.TrimSpace(r.URL.Query().Get("limit")); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			if l > 100 {
				l = 100
			}
			size = l
		}
	}

	filter := dto.ContactFilter{
		Page: page,
		Size: size,
	}

	if st := strings.TrimSpace(r.URL.Query().Get("status")); st != "" {
		filter.Status = &st
	}

	paged, err := h.svc.List(r.Context(), filter)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Contact inquiries retrieved successfully", paged)
}

// getByID fetches a single contact inquiry by ID for administrators.
func (h *Handler) getByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		response.Error(w, http.StatusBadRequest, "Invalid contact request ID", "VALIDATION_ERROR")
		return
	}

	res, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Contact inquiry retrieved successfully", res)
}

// update allows administrators to update the status and adminResponse of a contact inquiry.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		response.Error(w, http.StatusBadRequest, "Invalid contact request ID", "VALIDATION_ERROR")
		return
	}

	var req dto.UpdateContactRequest
	if !httpx.Decode(w, r, &req) {
		return
	}

	res, err := h.svc.Update(r.Context(), id, req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Contact inquiry updated successfully", res)
}

// delete marks a contact inquiry as soft-deleted for administrators.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		response.Error(w, http.StatusBadRequest, "Invalid contact request ID", "VALIDATION_ERROR")
		return
	}

	if err := h.svc.Delete(r.Context(), id); err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Contact inquiry deleted successfully", nil)
}

