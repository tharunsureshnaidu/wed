package handler

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// Service defines the contract expected by the FAQ HTTP handler.
type Service interface {
	Create(ctx context.Context, req dto.CreateFAQRequest) (*dto.FAQResponse, error)
	GetByID(ctx context.Context, id string) (*dto.FAQResponse, error)
	ListAdmin(ctx context.Context, page, size int) (*httpx.Paged, error)
	Update(ctx context.Context, id string, req dto.UpdateFAQRequest) (*dto.FAQResponse, error)
	Delete(ctx context.Context, id string) error
	ListPublic(ctx context.Context) ([]dto.PublicFAQResponse, error)
}

// Handler handles HTTP requests for FAQ CRUD operations and public reading.
type Handler struct {
	svc    Service
	signer *jwt.Signer
}

// New creates a new FAQ handler with real database dependencies.
func New(db *pgxpool.Pool, signer *jwt.Signer) *Handler {
	repo := repository.New(db)
	svc := service.New(repo)
	return NewWithService(svc, signer)
}

// NewWithService constructs an FAQ handler with an injected service (e.g. for testing).
func NewWithService(svc Service, signer *jwt.Signer) *Handler {
	return &Handler{
		svc:    svc,
		signer: signer,
	}
}

// Register mounts all FAQ endpoints on the provided ServeMux.
func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequireAuth(h.signer),
			middleware.RequireRole(domain.RoleAdmin, domain.RoleSuperAdmin))
	}

	// Admin CRUD APIs — requires ROLE_ADMIN or ROLE_SUPER_ADMIN
	mux.Handle("POST /api/v1/admin/faqs", admin(h.create))
	mux.Handle("GET /api/v1/admin/faqs", admin(h.listAdmin))
	mux.Handle("GET /api/v1/admin/faqs/{faqId}", admin(h.getByIDAdmin))
	mux.Handle("PUT /api/v1/admin/faqs/{faqId}", admin(h.update))
	mux.Handle("DELETE /api/v1/admin/faqs/{faqId}", admin(h.delete))

	// Unversioned aliases for backward compatibility
	mux.Handle("POST /api/admin/faqs", admin(h.create))
	mux.Handle("GET /api/admin/faqs", admin(h.listAdmin))
	mux.Handle("GET /api/admin/faqs/{faqId}", admin(h.getByIDAdmin))
	mux.Handle("PUT /api/admin/faqs/{faqId}", admin(h.update))
	mux.Handle("DELETE /api/admin/faqs/{faqId}", admin(h.delete))

	// Public API — NO TOKEN OR AUTHENTICATION REQUIRED
	mux.HandleFunc("GET /api/v1/public/faqs", h.listPublic)
	mux.HandleFunc("GET /api/public/faqs", h.listPublic)
}

// create handles POST /api/v1/admin/faqs.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateFAQRequest
	if !httpx.Decode(w, r, &req) {
		return
	}

	res, err := h.svc.Create(r.Context(), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.Created(w, "FAQ created successfully", "/api/v1/admin/faqs/"+res.ID, res)
}

// listAdmin handles GET /api/v1/admin/faqs.
func (h *Handler) listAdmin(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)

	paged, err := h.svc.ListAdmin(r.Context(), page, size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "FAQs retrieved successfully", paged)
}

// getByIDAdmin handles GET /api/v1/admin/faqs/{faqId}.
func (h *Handler) getByIDAdmin(w http.ResponseWriter, r *http.Request) {
	faqID := r.PathValue("faqId")
	if !httpx.ValidUUID(faqID) {
		response.Error(w, http.StatusBadRequest, "Invalid FAQ ID format", "INVALID_ID")
		return
	}

	res, err := h.svc.GetByID(r.Context(), faqID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "FAQ retrieved successfully", res)
}

// update handles PUT /api/v1/admin/faqs/{faqId}.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	faqID := r.PathValue("faqId")
	if !httpx.ValidUUID(faqID) {
		response.Error(w, http.StatusBadRequest, "Invalid FAQ ID format", "INVALID_ID")
		return
	}

	var req dto.UpdateFAQRequest
	if !httpx.Decode(w, r, &req) {
		return
	}

	res, err := h.svc.Update(r.Context(), faqID, req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "FAQ updated successfully", res)
}

// delete handles DELETE /api/v1/admin/faqs/{faqId}.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	faqID := r.PathValue("faqId")
	if !httpx.ValidUUID(faqID) {
		response.Error(w, http.StatusBadRequest, "Invalid FAQ ID format", "INVALID_ID")
		return
	}

	if err := h.svc.Delete(r.Context(), faqID); err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "FAQ deleted successfully", nil)
}

// listPublic handles GET /api/v1/public/faqs (unauthenticated).
func (h *Handler) listPublic(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.ListPublic(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "FAQs retrieved successfully", res)
}
