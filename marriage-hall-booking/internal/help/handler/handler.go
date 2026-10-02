package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	helpdomain "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// Handler serves the Help Center / Customer Support HTTP endpoints.
type Handler struct {
	svc    service.Service
	signer *jwt.Signer

	// OnMessageCreated hook exposed for external notification wiring (e.g., in cmd/api/main.go)
	OnMessageCreated func(ctx context.Context, msg *helpdomain.HelpCenterMessage)
}

// New creates a new Help Center HTTP handler with standard dependencies.
func New(db *pgxpool.Pool, signer *jwt.Signer) *Handler {
	repo := repository.New(db)
	svc := service.New(repo)
	return NewWithService(svc, signer)
}

// NewWithService creates a new Help Center HTTP handler with an injected service interface.
func NewWithService(svc service.Service, signer *jwt.Signer) *Handler {
	return &Handler{
		svc:    svc,
		signer: signer,
	}
}

// Register registers all User and Super Admin Help Center routes on the given ServeMux.
func (h *Handler) Register(mux *http.ServeMux) {
	if h.OnMessageCreated != nil {
		h.svc.SetOnMessageCreated(h.OnMessageCreated)
	}

	auth := middleware.RequireAuth(h.signer)
	superAdmin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, auth, middleware.RequireRole(domain.RoleSuperAdmin))
	}

	// Authenticated User Endpoint
	mux.Handle("POST /api/v1/help/messages", auth(http.HandlerFunc(h.submit)))

	// Super Admin Endpoints (ROLE_SUPER_ADMIN only)
	mux.Handle("GET /api/v1/super-admin/help/messages", superAdmin(h.list))
	mux.Handle("GET /api/v1/super-admin/help/messages/{messageId}", superAdmin(h.getByID))
	mux.Handle("DELETE /api/v1/super-admin/help/messages/{messageId}", superAdmin(h.delete))
}

// submit processes Help Center message submissions from authenticated users.
func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok || userID <= 0 {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	var req dto.CreateHelpMessageRequest
	if !httpx.Decode(w, r, &req) {
		return
	}

	res, err := h.svc.Submit(r.Context(), userID, req)
	if err != nil {
		fail(w, err)
		return
	}

	response.Created(
		w,
		"Your query has been submitted successfully. Our team will review your query and contact you as soon as possible.",
		"/api/v1/help/messages/"+res.ID,
		res,
	)
}

// list returns a paginated, searchable, and filterable list of Help Center messages for Super Admin.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)

	// Custom limit parameter support
	if lStr := strings.TrimSpace(r.URL.Query().Get("limit")); lStr != "" {
		l, err := strconv.Atoi(lStr)
		if err != nil || l <= 0 {
			response.Error(w, http.StatusBadRequest, "Invalid limit parameter", "VALIDATION_ERROR")
			return
		}
		if l > 100 {
			l = 100
		}
		size = l
	}

	if pStr := strings.TrimSpace(r.URL.Query().Get("page")); pStr != "" {
		p, err := strconv.Atoi(pStr)
		if err != nil || p < 0 {
			response.Error(w, http.StatusBadRequest, "Invalid page parameter", "VALIDATION_ERROR")
			return
		}
		page = p
	}

	filter := dto.HelpMessageFilter{
		Page:      page,
		Limit:     size,
		SortBy:    strings.TrimSpace(r.URL.Query().Get("sort")),
		SortOrder: strings.TrimSpace(r.URL.Query().Get("order")),
	}

	if filter.SortOrder != "" {
		orderLower := strings.ToLower(filter.SortOrder)
		if orderLower != "asc" && orderLower != "desc" {
			response.Error(w, http.StatusBadRequest, "Order must be asc or desc", "VALIDATION_ERROR")
			return
		}
	}

	if filter.SortBy != "" {
		switch strings.ToLower(filter.SortBy) {
		case "created_at", "createdat", "updated_at", "updatedat", "status",
			"user_name", "username", "user_email", "useremail", "user_phone", "userphone":
			// valid
		default:
			response.Error(w, http.StatusBadRequest, "Invalid sort parameter", "VALIDATION_ERROR")
			return
		}
	}

	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		filter.Search = &search
	}

	if statusVal := strings.TrimSpace(r.URL.Query().Get("status")); statusVal != "" {
		upper := strings.ToUpper(statusVal)
		if !helpdomain.HelpCenterStatus(upper).Valid() {
			response.Error(w, http.StatusBadRequest, "Status filter must be NEW or READ", "VALIDATION_ERROR")
			return
		}
		filter.Status = &upper
	}

	if uidStr := strings.TrimSpace(r.URL.Query().Get("user_id")); uidStr != "" {
		uid, err := strconv.ParseInt(uidStr, 10, 64)
		if err != nil || uid <= 0 {
			response.Error(w, http.StatusBadRequest, "Invalid user_id parameter", "VALIDATION_ERROR")
			return
		}
		filter.UserID = &uid
	}

	if fromStr := strings.TrimSpace(r.URL.Query().Get("from_date")); fromStr != "" {
		fromDate, err := parseDate(fromStr, false)
		if err != nil {
			response.Error(w, http.StatusBadRequest, "Invalid from_date parameter. Expected format YYYY-MM-DD or RFC3339", "VALIDATION_ERROR")
			return
		}
		filter.FromDate = fromDate
	}

	if toStr := strings.TrimSpace(r.URL.Query().Get("to_date")); toStr != "" {
		toDate, err := parseDate(toStr, true)
		if err != nil {
			response.Error(w, http.StatusBadRequest, "Invalid to_date parameter. Expected format YYYY-MM-DD or RFC3339", "VALIDATION_ERROR")
			return
		}
		filter.ToDate = toDate
	}

	if filter.FromDate != nil && filter.ToDate != nil && filter.FromDate.After(*filter.ToDate) {
		response.Error(w, http.StatusBadRequest, "from_date cannot be after to_date", "VALIDATION_ERROR")
		return
	}

	paged, err := h.svc.ListAll(r.Context(), filter)
	if err != nil {
		fail(w, err)
		return
	}

	response.OK(w, "Help center messages retrieved successfully", paged)
}

// getByID returns a single Help Center message and marks it as READ if currently NEW.
func (h *Handler) getByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("messageId")
	if id == "" {
		id = r.PathValue("id")
	}

	res, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}

	response.OK(w, "Help center message retrieved successfully", res)
}

// delete removes a Help Center message using soft deletion.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("messageId")
	if id == "" {
		id = r.PathValue("id")
	}

	if err := h.svc.Delete(r.Context(), id); err != nil {
		fail(w, err)
		return
	}

	response.OK(w, "Help center message deleted successfully", nil)
}

func parseDate(val string, endOfDay bool) (*time.Time, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, nil
	}

	// 1. RFC3339
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		return &t, nil
	}

	// 2. YYYY-MM-DD
	if t, err := time.Parse("2006-01-02", val); err == nil {
		if endOfDay {
			t = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.UTC)
		} else {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}
		return &t, nil
	}

	// 3. YYYY-MM-DD HH:MM:SS
	if t, err := time.Parse("2006-01-02 15:04:05", val); err == nil {
		return &t, nil
	}

	return nil, errors.New("unsupported date format")
}

func fail(w http.ResponseWriter, err error) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		response.Error(w, ae.Status, ae.Message, ae.Code)
		return
	}
	httpx.Fail(w, err)
}
