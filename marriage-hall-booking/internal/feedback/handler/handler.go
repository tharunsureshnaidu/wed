// Package handler serves app feedback: what users think of the service itself,
// as opposed to internal/review, which rates a venue.
//
// No service layer - a flat table with validation, the same shape
// internal/support and internal/vendors use.
package handler

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/jwt"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
	"github.com/tripfcatory/marriage-hall-booking/pkg/storage"
)

type Handler struct {
	db     *pgxpool.Pool
	signer *jwt.Signer
	store  storage.Store

	// OnSubmitted tells ops a report landed. A hook rather than an import, so
	// this package stays independent of notification - the same wiring the
	// facility and admin handlers use.
	OnSubmitted func(ctx context.Context, id, message string, rating *int)
}

func New(db *pgxpool.Pool, signer *jwt.Signer, store storage.Store) *Handler {
	return &Handler{db: db, signer: signer, store: store}
}

func (h *Handler) Register(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, auth, middleware.RequireRole(domain.RoleAdmin))
	}

	mux.Handle("POST /api/v1/feedback", auth(http.HandlerFunc(h.submit)))
	// A user's own history, so the app can show "you told us about this".
	mux.Handle("GET /api/v1/feedback/my-feedback", auth(http.HandlerFunc(h.listMine)))

	// The ops inbox.
	mux.Handle("GET /api/v1/admin/feedback", admin(h.listAll))
	mux.Handle("PUT /api/v1/admin/feedback/{id}", admin(h.update))
}

// maxAttachment caps a screenshot. Phone screenshots are well under this; the
// limit exists so one request cannot spool an arbitrary file to disk.
const maxAttachment = 10 << 20 // 10 MiB

var attachmentTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

type feedbackItem struct {
	ID            string     `json:"id"`
	UserID        *int64     `json:"userId,omitempty"`
	UserName      *string    `json:"userName,omitempty"`
	UserEmail     *string    `json:"userEmail,omitempty"`
	Rating        *int       `json:"rating"`
	Message       string     `json:"message"`
	AttachmentURL *string    `json:"attachmentUrl,omitempty"`
	AppVersion    *string    `json:"appVersion,omitempty"`
	Platform      *string    `json:"platform,omitempty"`
	Status        string     `json:"status"`
	AdminNote     *string    `json:"adminNote,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	ResolvedAt    *time.Time `json:"resolvedAt,omitempty"`
}

// submit accepts both JSON and multipart, because the screen has an optional
// screenshot: a text-only report should not force the client to build a
// multipart body.
func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())

	var (
		rating               *int
		message              string
		appVersion, platform *string
		attachment           *multipart.FileHeader
	)

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		// The whole request is capped, not just the file: a multipart body can
		// carry many parts, and limiting only the attachment leaves the rest
		// unbounded.
		r.Body = http.MaxBytesReader(w, r.Body, maxAttachment+1<<20)
		if err := r.ParseMultipartForm(maxAttachment); err != nil {
			response.Error(w, http.StatusBadRequest,
				"Attachment is too large or the form is malformed", "VALIDATION_ERROR")
			return
		}
		message = strings.TrimSpace(r.FormValue("message"))
		if v := strings.TrimSpace(r.FormValue("rating")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				rating = &n
			}
		}
		appVersion = optional(r.FormValue("appVersion"))
		platform = optional(r.FormValue("platform"))
		if r.MultipartForm != nil {
			for _, key := range []string{"attachment", "screenshot", "file", "image"} {
				if fhs := r.MultipartForm.File[key]; len(fhs) > 0 {
					attachment = fhs[0]
					break
				}
			}
		}
	} else {
		var req struct {
			Rating     *int    `json:"rating"`
			Message    string  `json:"message"`
			AppVersion *string `json:"appVersion"`
			Platform   *string `json:"platform"`
		}
		if !httpx.Decode(w, r, &req) {
			return
		}
		rating, message = req.Rating, strings.TrimSpace(req.Message)
		appVersion, platform = req.AppVersion, req.Platform
	}

	if msg, ok := validateFeedback(rating, message, platform); !ok {
		response.Error(w, http.StatusBadRequest, msg, "VALIDATION_ERROR")
		return
	}

	var attachmentURL *string
	if attachment != nil {
		url, err := h.saveAttachment(r.Context(), attachment)
		if err != nil {
			response.Error(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
			return
		}
		attachmentURL = &url
	}

	var id string
	var createdAt time.Time
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO app_feedback (user_id, rating, message, attachment_url, app_version, platform)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, created_at`,
		userID, rating, message, attachmentURL, appVersion, platform).Scan(&id, &createdAt)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	// After the insert, never before: ops must not be told about a report that
	// rolled back. A hook failure is logged, not returned - the user's feedback
	// was saved either way.
	if h.OnSubmitted != nil {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("feedback: notify hook panicked", "id", id)
				}
			}()
			h.OnSubmitted(r.Context(), id, message, rating)
		}()
	}

	response.OK(w, "Thank you for your feedback", map[string]any{
		"id": id, "status": "NEW", "createdAt": createdAt,
		"attachmentUrl": attachmentURL,
	})
}

// validateFeedback keeps the rules in one place so the JSON and multipart paths
// cannot drift apart.
func validateFeedback(rating *int, message string, platform *string) (string, bool) {
	if message == "" {
		return "A message is required", false
	}
	if len([]rune(message)) > 5000 {
		return "Message must be 5000 characters or fewer", false
	}
	if rating != nil && (*rating < 1 || *rating > 5) {
		return "Rating must be between 1 and 5", false
	}
	if platform != nil && *platform != "" {
		switch strings.ToUpper(*platform) {
		case "ANDROID", "IOS", "WEB":
		default:
			return "Platform must be ANDROID, IOS or WEB", false
		}
	}
	return "", true
}

// saveAttachment sniffs the real content type rather than trusting the
// filename: an executable renamed to .png must not be stored and served back.
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

func optional(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

func (h *Handler) listMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Authentication required", "UNAUTHORIZED")
		return
	}
	page, size := httpx.Page(r)
	// The reporter sees their own words and the status, never admin_note.
	h.respondList(w, r, `
		SELECT f.id::text, NULL::bigint, NULL, NULL,
		       f.rating, f.message, f.attachment_url, f.app_version, f.platform,
		       f.status, NULL, f.created_at, f.resolved_at
		  FROM app_feedback f
		 WHERE f.user_id = $1 AND f.is_deleted = FALSE
		 ORDER BY f.created_at DESC LIMIT $2 OFFSET $3`,
		`SELECT COUNT(*) FROM app_feedback WHERE user_id = $1 AND is_deleted = FALSE`,
		[]any{userID}, page, size)
}

func (h *Handler) listAll(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)
	status := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	switch status {
	case "", "NEW", "REVIEWING", "RESOLVED", "CLOSED":
	default:
		response.Error(w, http.StatusBadRequest,
			"status must be NEW, REVIEWING, RESOLVED or CLOSED", "VALIDATION_ERROR")
		return
	}
	// $1 empty means "every status" - one query rather than two that can drift.
	h.respondList(w, r, `
		SELECT f.id::text, f.user_id, u.full_name, u.email,
		       f.rating, f.message, f.attachment_url, f.app_version, f.platform,
		       f.status, f.admin_note, f.created_at, f.resolved_at
		  FROM app_feedback f
		  LEFT JOIN users u ON u.id = f.user_id
		 WHERE f.is_deleted = FALSE AND ($1 = '' OR f.status = $1)
		 ORDER BY f.created_at DESC LIMIT $2 OFFSET $3`,
		`SELECT COUNT(*) FROM app_feedback WHERE is_deleted = FALSE AND ($1 = '' OR status = $1)`,
		[]any{status}, page, size)
}

func (h *Handler) respondList(w http.ResponseWriter, r *http.Request,
	listSQL, countSQL string, args []any, page, size int) {

	var total int64
	if err := h.db.QueryRow(r.Context(), countSQL, args...).Scan(&total); err != nil {
		httpx.Fail(w, err)
		return
	}
	rows, err := h.db.Query(r.Context(), listSQL, append(args, size, page*size)...)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	out := []feedbackItem{}
	for rows.Next() {
		var it feedbackItem
		if err := rows.Scan(&it.ID, &it.UserID, &it.UserName, &it.UserEmail,
			&it.Rating, &it.Message, &it.AttachmentURL, &it.AppVersion, &it.Platform,
			&it.Status, &it.AdminNote, &it.CreatedAt, &it.ResolvedAt); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Feedback retrieved successfully", map[string]any{
		"content": out, "page": page, "size": size, "totalElements": total,
	})
}

// update is the ops triage action: change status, leave a note.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid feedback id", "VALIDATION_ERROR")
		return
	}
	var req struct {
		Status    *string `json:"status"`
		AdminNote *string `json:"adminNote"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Status == nil && req.AdminNote == nil {
		response.Error(w, http.StatusBadRequest, "No fields to update", "VALIDATION_ERROR")
		return
	}
	if req.Status != nil {
		switch strings.ToUpper(*req.Status) {
		case "NEW", "REVIEWING", "RESOLVED", "CLOSED":
			s := strings.ToUpper(*req.Status)
			req.Status = &s
		default:
			response.Error(w, http.StatusBadRequest,
				"status must be NEW, REVIEWING, RESOLVED or CLOSED", "VALIDATION_ERROR")
			return
		}
	}
	adminID, _ := middleware.UserID(r.Context())

	// COALESCE keeps an omitted field untouched, so setting a note cannot blank
	// the status. resolved_at is stamped only on the transition into a closed
	// state, and cleared on the way back out.
	var it feedbackItem
	err := h.db.QueryRow(r.Context(), `
		UPDATE app_feedback SET
		    status      = COALESCE($2, status),
		    admin_note  = COALESCE($3, admin_note),
		    resolved_by = CASE WHEN $2 IN ('RESOLVED','CLOSED') THEN $4 ELSE resolved_by END,
		    resolved_at = CASE
		        WHEN $2 IN ('RESOLVED','CLOSED') THEN CURRENT_TIMESTAMP
		        WHEN $2 IS NOT NULL THEN NULL
		        ELSE resolved_at END,
		    updated_at  = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE
		 RETURNING id::text, user_id, rating, message, attachment_url,
		           app_version, platform, status, admin_note, created_at, resolved_at`,
		id, req.Status, req.AdminNote, adminID).Scan(
		&it.ID, &it.UserID, &it.Rating, &it.Message, &it.AttachmentURL,
		&it.AppVersion, &it.Platform, &it.Status, &it.AdminNote, &it.CreatedAt, &it.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Feedback not found", "NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Feedback updated successfully", it)
}
