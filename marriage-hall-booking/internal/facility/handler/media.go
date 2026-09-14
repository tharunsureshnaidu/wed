package handler

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
	"github.com/tripfcatory/marriage-hall-booking/pkg/storage"
	"github.com/tripfcatory/marriage-hall-booking/pkg/validate"
)

// RegisterMedia mounts the image/video routes.
//
// These record URLs of files that are already hosted somewhere; there is no
// object store configured here, so the caller supplies the URL rather than
// uploading bytes. Swapping in a real uploader means changing these handlers
// only - the storage, ordering and cover-image rules stay as they are.
func (h *Handler) RegisterMedia(mux *http.ServeMux) {
	owner := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, middleware.RequireAuth(h.signer),
			middleware.RequireRole(domain.RoleHallOwner, domain.RoleAdmin))
	}
	mux.Handle("POST /api/v1/facilities/{id}/images", owner(h.addImage))
	mux.Handle("DELETE /api/v1/facilities/{id}/images/{childId}", owner(h.deleteImage))
	mux.Handle("PUT /api/v1/facilities/{id}/images/{childId}/cover", owner(h.setCover))
	mux.Handle("PUT /api/v1/facilities/{id}/images/reorder", owner(h.reorderImages))
	mux.Handle("POST /api/v1/facilities/{id}/videos", owner(h.addVideo))
	mux.Handle("DELETE /api/v1/facilities/{id}/videos/{childId}", owner(h.deleteVideo))
	mux.Handle("PUT /api/v1/facilities/{id}/videos/reorder", owner(h.reorderVideos))
	mux.Handle("POST /api/v1/facilities/{id}/block", owner(h.block))
	mux.Handle("POST /api/v1/facilities/{id}/unblock", owner(h.unblock))
}

type imageReq struct {
	URL       string `json:"url"`
	IsCover   bool   `json:"isCover"`
	SortOrder int    `json:"sortOrder"`
}

func (h *Handler) addImage(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	// Java's POST /{id}/images is multipart with an "image" file part. The JSON
	// {url} form is kept because it shipped and clients send it, but a real file
	// upload is the path that stores bytes anywhere.
	var req imageReq
	if isMultipart(r) {
		url, err := h.uploadPart(w, r, facilityID, storage.Image, "image")
		if err != nil {
			return // uploadPart has already written the response
		}
		req.URL = url
		req.IsCover = r.FormValue("isCover") == "true"
	} else {
		if !httpx.Decode(w, r, &req) {
			return
		}
		var e validate.Errors
		e.Required("url", req.URL)
		if len(e) > 0 {
			response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
			return
		}
	}

	tx, err := h.repo.Pool().Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	// Exactly one cover per facility.
	if req.IsCover {
		if _, err := tx.Exec(r.Context(),
			`UPDATE facility_images SET is_cover = FALSE WHERE facility_id = $1`,
			facilityID); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	var id string
	if err := tx.QueryRow(r.Context(),
		`INSERT INTO facility_images (facility_id, url, is_cover, sort_order)
		 VALUES ($1,$2,$3,$4) RETURNING id`,
		facilityID, req.URL, req.IsCover, req.SortOrder).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Image added successfully", map[string]any{
		"id": id, "facilityId": facilityID, "url": req.URL,
		"isCover": req.IsCover, "sortOrder": req.SortOrder,
	})
}

func (h *Handler) deleteImage(w http.ResponseWriter, r *http.Request) {
	h.deleteMedia(w, r, "facility_images")
}

// deleteMedia removes the row and the stored object behind it. hardDeleteChild
// deletes only the row, which for media leaves the file orphaned in the bucket
// forever - nothing else ever references it, so nothing else can clean it up.
//
// The row is deleted first and the object after: a failed object delete leaves
// a file nobody can reach, while the reverse order would leave a row pointing
// at a file that no longer exists, which is the one users actually see.
func (h *Handler) deleteMedia(w http.ResponseWriter, r *http.Request, table string) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	childID, ok := child(w, r)
	if !ok {
		return
	}
	var url string
	err := h.repo.Pool().QueryRow(r.Context(),
		`DELETE FROM `+table+` WHERE id = $1 AND facility_id = $2 RETURNING url`,
		childID, facilityID).Scan(&url)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Not found", "NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.media.Delete(r.Context(), url); err != nil {
		// The row is already gone, so the media is unreachable either way.
		// Report success and log, rather than telling the caller a delete they
		// can no longer retry has failed.
		logger.Warn("stored object could not be removed",
			logger.Component, "storage", "url", url, logger.Err(err))
	}
	response.OK(w, "Deleted successfully", nil)
}

func (h *Handler) setCover(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	imageID, ok := child(w, r)
	if !ok {
		return
	}
	tx, err := h.repo.Pool().Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := tx.Exec(r.Context(),
		`UPDATE facility_images SET is_cover = FALSE WHERE facility_id = $1`,
		facilityID); err != nil {
		httpx.Fail(w, err)
		return
	}
	tag, err := tx.Exec(r.Context(),
		`UPDATE facility_images SET is_cover = TRUE WHERE id = $1 AND facility_id = $2`,
		imageID, facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Image not found", "NOT_FOUND")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Cover image updated successfully", nil)
}

type reorderReq struct {
	// Ids in their new display order.
	Order []string `json:"order"`
	IDs   []string `json:"ids"`
}

func (h *Handler) reorderImages(w http.ResponseWriter, r *http.Request) {
	h.reorder(w, r, "facility_images")
}

func (h *Handler) reorderVideos(w http.ResponseWriter, r *http.Request) {
	h.reorder(w, r, "facility_videos")
}

func (h *Handler) reorder(w http.ResponseWriter, r *http.Request, table string) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req reorderReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	order := req.Order
	if len(order) == 0 {
		order = req.IDs
	}
	if len(order) == 0 {
		response.Error(w, http.StatusBadRequest,
			"order must list the ids in their new order", "VALIDATION_ERROR")
		return
	}
	for _, id := range order {
		if !httpx.ValidUUID(id) {
			response.Error(w, http.StatusBadRequest, "Invalid id in order", "VALIDATION_ERROR")
			return
		}
	}

	tx, err := h.repo.Pool().Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	// Scoped to the facility, so an id from another facility silently affects
	// nothing rather than being reordered into this one.
	for i, id := range order {
		if _, err := tx.Exec(r.Context(),
			`UPDATE `+table+` SET sort_order = $3 WHERE id = $1 AND facility_id = $2`,
			id, facilityID, i); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Order updated successfully", nil)
}

// uploadPart reads one named file part, stores it, and returns its URL. On any
// failure it writes the error response and returns a non-nil error, so callers
// just return.
func (h *Handler) uploadPart(w http.ResponseWriter, r *http.Request, facilityID string,
	kind storage.Kind, field string) (string, error) {

	if err := r.ParseMultipartForm(storage.Limit(kind)); err != nil {
		response.Error(w, http.StatusBadRequest, "Malformed multipart body", "VALIDATION_ERROR")
		return "", err
	}
	files := r.MultipartForm.File[field]
	if len(files) == 0 {
		response.Error(w, http.StatusBadRequest, field+" file is required", "INVALID_FILE")
		return "", errNoFile
	}

	// The key is namespaced by vendor, so look up which one owns this facility.
	var vendorID *string
	if err := h.repo.Pool().QueryRow(r.Context(),
		`SELECT vendor_id FROM facilities WHERE id = $1`, facilityID).Scan(&vendorID); err != nil {
		httpx.Fail(w, err)
		return "", err
	}
	var vid string
	if vendorID != nil {
		vid = *vendorID
	}

	url, err := h.saveUpload(r.Context(), files[0], kind, facilityID, vid)
	if err != nil {
		response.Error(w, http.StatusBadRequest, err.Error(), "INVALID_FILE")
		return "", err
	}
	return url, nil
}

var errNoFile = errors.New("no file part")

type videoReq struct {
	URL             string  `json:"url"`
	ThumbnailURL    *string `json:"thumbnailUrl"`
	DurationSeconds *int    `json:"durationSeconds"`
	SortOrder       int     `json:"sortOrder"`
}

func (h *Handler) addVideo(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	// Java's POST /{id}/videos is multipart with a "video" file part, capped at
	// 200MB. The JSON {url} form is kept for the same reason as images.
	var req videoReq
	if isMultipart(r) {
		url, err := h.uploadPart(w, r, facilityID, storage.Video, "video")
		if err != nil {
			return
		}
		req.URL = url
	} else {
		if !httpx.Decode(w, r, &req) {
			return
		}
		var e validate.Errors
		e.Required("url", req.URL)
		if len(e) > 0 {
			response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
			return
		}
	}
	var id string
	if err := h.repo.Pool().QueryRow(r.Context(),
		`INSERT INTO facility_videos (facility_id, url, thumbnail_url, duration_seconds, sort_order)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		facilityID, req.URL, req.ThumbnailURL, req.DurationSeconds, req.SortOrder).
		Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Video added successfully", map[string]any{
		"id": id, "facilityId": facilityID, "url": req.URL,
	})
}

func (h *Handler) deleteVideo(w http.ResponseWriter, r *http.Request) {
	h.deleteMedia(w, r, "facility_videos")
}

func (h *Handler) block(w http.ResponseWriter, r *http.Request)   { h.setStatus(w, r, "BLOCKED") }
func (h *Handler) unblock(w http.ResponseWriter, r *http.Request) { h.setStatus(w, r, "APPROVED") }

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	if _, err := h.repo.Pool().Exec(r.Context(),
		`UPDATE facilities SET status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		facilityID, status); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Facility "+status, map[string]any{"id": facilityID, "status": status})
}
