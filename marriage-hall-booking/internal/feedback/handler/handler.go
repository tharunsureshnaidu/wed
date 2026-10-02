package handler

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/dto"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/storage"
)

const maxAttachment = 10 << 20 // 10 MiB

var attachmentTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// Handler handles HTTP requests for user feedback and admin triage.
type Handler struct {
	svc    service.Service
	signer *jwt.Signer
	store  storage.Store

	// OnSubmitted hook exposed for main.go event wiring
	OnSubmitted func(ctx context.Context, id, message string, rating *int)
}

// New creates a new feedback HTTP handler with standard dependencies.
func New(db *pgxpool.Pool, signer *jwt.Signer, store storage.Store) *Handler {
	repo := repository.New(db)
	svc := service.New(repo)
	return NewWithService(svc, signer, store)
}

// NewWithService creates a new feedback HTTP handler injecting the service interface.
func NewWithService(svc service.Service, signer *jwt.Signer, store storage.Store) *Handler {
	h := &Handler{
		svc:    svc,
		signer: signer,
		store:  store,
	}
	return h
}

// Register registers all user and admin routes on the given ServeMux.
func (h *Handler) Register(mux *http.ServeMux) {
	if h.OnSubmitted != nil {
		h.svc.SetOnSubmitted(h.OnSubmitted)
	}

	auth := middleware.RequireAuth(h.signer)
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, auth, middleware.RequireRole(domain.RoleAdmin))
	}

	// User Feedback Submission (Authenticated)
	mux.Handle("POST /api/feedback", auth(http.HandlerFunc(h.submit)))
	mux.Handle("POST /api/v1/feedback", auth(http.HandlerFunc(h.submit)))
	mux.Handle("GET /api/v1/feedback/my-feedback", auth(http.HandlerFunc(h.listMine)))

	// Admin Feedback Management (ROLE_ADMIN only)
	mux.Handle("GET /api/admin/feedback", admin(h.listAll))
	mux.Handle("GET /api/v1/admin/feedback", admin(h.listAll))

	mux.Handle("GET /api/admin/feedback/summary", admin(h.summary))
	mux.Handle("GET /api/v1/admin/feedback/summary", admin(h.summary))

	mux.Handle("GET /api/admin/feedback/{id}", admin(h.getByID))
	mux.Handle("GET /api/v1/admin/feedback/{id}", admin(h.getByID))

	mux.Handle("PUT /api/admin/feedback/{id}", admin(h.update))
	mux.Handle("PUT /api/v1/admin/feedback/{id}", admin(h.update))
}

// submit processes user feedback submissions (supports both JSON and multipart form-data).
func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok || userID <= 0 {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	var (
		req        dto.CreateFeedbackRequest
		attachment *multipart.FileHeader
	)

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, maxAttachment+1<<20)
		if err := r.ParseMultipartForm(maxAttachment); err != nil {
			response.Error(w, http.StatusBadRequest, "Attachment is too large or the form is malformed", "VALIDATION_ERROR")
			return
		}

		if v := strings.TrimSpace(r.FormValue("rating")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				req.Rating = &n
			}
		}

		feedbackText := strings.TrimSpace(r.FormValue("feedback"))
		if feedbackText == "" {
			feedbackText = strings.TrimSpace(r.FormValue("message"))
		}
		if feedbackText != "" {
			req.Feedback = &feedbackText
		}

		if v := strings.TrimSpace(r.FormValue("appVersion")); v != "" {
			req.AppVersion = &v
		}
		if v := strings.TrimSpace(r.FormValue("platform")); v != "" {
			req.Platform = &v
		}

		if r.MultipartForm != nil {
			for _, key := range []string{"attachment", "screenshot", "file", "image"} {
				if fhs := r.MultipartForm.File[key]; len(fhs) > 0 {
					attachment = fhs[0]
					break
				}
			}
		}
	} else {
		if !httpx.Decode(w, r, &req) {
			return
		}
	}

	var attachmentURL *string
	if attachment != nil && h.store != nil {
		url, err := h.saveAttachment(r.Context(), attachment)
		if err != nil {
			response.Error(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
			return
		}
		attachmentURL = &url
	}

	res, err := h.svc.Submit(r.Context(), userID, req, attachmentURL)
	if err != nil {
		fail(w, err)
		return
	}

	response.Created(w, "Thank you for your feedback.", "/api/v1/feedback/"+res.ID, res)
}

func (h *Handler) listMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok || userID <= 0 {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}

	page, size := httpx.Page(r)
	paged, err := h.svc.ListMine(r.Context(), userID, page, size)
	if err != nil {
		fail(w, err)
		return
	}

	response.OK(w, "Feedback retrieved successfully", paged)
}

func (h *Handler) listAll(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)

	filter := dto.FeedbackFilter{
		Page: page,
		Size: size,
		Sort: strings.TrimSpace(r.URL.Query().Get("sort")),
	}

	if rVal := strings.TrimSpace(r.URL.Query().Get("rating")); rVal != "" {
		if n, err := strconv.Atoi(rVal); err == nil {
			filter.Rating = &n
		}
	}

	if sVal := strings.TrimSpace(r.URL.Query().Get("status")); sVal != "" {
		filter.Status = &sVal
	}

	// Support both ?search= and ?q=
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if search == "" {
		search = strings.TrimSpace(r.URL.Query().Get("q"))
	}
	if search != "" {
		filter.Search = &search
	}

	paged, err := h.svc.ListAll(r.Context(), filter)
	if err != nil {
		fail(w, err)
		return
	}

	response.OK(w, "Feedback retrieved successfully", paged)
}

func (h *Handler) getByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}

	response.OK(w, "Feedback retrieved successfully", res)
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.GetSummary(r.Context())
	if err != nil {
		fail(w, err)
		return
	}

	response.OK(w, "Feedback summary retrieved successfully", res)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	adminID, _ := middleware.UserID(r.Context())

	var req dto.UpdateFeedbackRequest
	if !httpx.Decode(w, r, &req) {
		return
	}

	res, err := h.svc.UpdateStatus(r.Context(), id, adminID, req)
	if err != nil {
		fail(w, err)
		return
	}

	response.OK(w, "Feedback updated successfully", res)
}

func (h *Handler) saveAttachment(ctx context.Context, fh *multipart.FileHeader) (string, error) {
	if fh.Size > maxAttachment {
		return "", fmt.Errorf("attachment must be %d MB or smaller", maxAttachment>>20)
	}
	f, err := fh.Open()
	if err != nil {
		return "", errors.New("could not read the attachment")
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := f.Read(head)
	ct := http.DetectContentType(head[:n])
	ext, ok := attachmentTypes[ct]
	if !ok {
		return "", errors.New("attachment must be a JPEG, PNG or WebP image")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", errors.New("could not read the attachment")
	}

	res, err := h.store.Put(ctx, storage.Upload{
		Kind: storage.Image, Body: f, Size: fh.Size,
		ContentType: ct, Ext: ext,
	})
	if err != nil {
		return "", errors.New("could not store the attachment")
	}
	return res.URL, nil
}

func fail(w http.ResponseWriter, err error) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		response.Error(w, ae.Status, ae.Message, ae.Code)
		return
	}
	httpx.Fail(w, err)
}
