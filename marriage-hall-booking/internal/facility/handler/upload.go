package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
	"github.com/tripfcatory/marriage-hall-booking/pkg/storage"
)

// Multipart facility creation: the venue's details and its photos in one
// request, which is what the listing form submits.
//
// ponytail: files are written to ./uploads and served by the API itself. No S3
// credentials to configure for local work, and the stored value is a URL either
// way - swapping in object storage later means changing saveUpload, not the
// database or any client.
const (
	uploadDir    = "uploads"
	maxImageSize = 10 << 20 // 10 MB per file
	maxImages    = 20
)

// imageTypes is an allowlist, not a blocklist: the file is served back over
// HTTP, so accepting anything would let a caller host arbitrary content (an
// .html with a script in it, say) on this origin.
var imageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// Java allows mp4/mov for video and pdf/jpg/jpeg/png for documents. These are
// the sniffed media types for those, since the extension is not trusted.
var videoTypes = map[string]string{
	"video/mp4":       ".mp4",
	"video/quicktime": ".mov",
}

var documentTypes = map[string]string{
	"application/pdf": ".pdf",
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
}

// isMultipart reports whether the request carries a multipart form body.
func isMultipart(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data")
}

// decodeMultipart reads the JSON part and the image files from a multipart
// form. The fields arrive either as one JSON blob under "data" (what a client
// sending a prepared object does) or as individual form fields (what a plain
// HTML form posts); both are accepted.
func decodeMultipart(r *http.Request, dst *facilityReq) ([]*multipart.FileHeader, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, fmt.Errorf("could not read the form: %w", err)
	}
	if blob := r.FormValue("data"); blob != "" {
		if err := json.Unmarshal([]byte(blob), dst); err != nil {
			return nil, fmt.Errorf("the data part is not valid JSON: %w", err)
		}
	} else {
		// Field-by-field. Only the values the listing form actually sends;
		// anything richer belongs in the "data" blob.
		obj := map[string]any{}
		for k, v := range r.MultipartForm.Value {
			if len(v) == 0 || k == "data" {
				continue
			}
			obj[k] = v[0]
		}
		b, _ := json.Marshal(obj)
		// Numbers arrive as strings from a form, so decode leniently: unknown
		// or mistyped fields are skipped rather than failing the whole request.
		_ = json.Unmarshal(b, dst)
		decodeFormNumbers(r, dst)
	}
	// Repeated field, one value per checkbox - JSON decoding above cannot see
	// these because the form sends them outside the "data" blob.
	if refs := amenityRefs(r.MultipartForm.Value["amenityIds"]); len(refs) > 0 {
		dst.AmenityIDs = refs
	}

	files := r.MultipartForm.File["images"]
	if len(files) > maxImages {
		return nil, fmt.Errorf("at most %d images per request", maxImages)
	}
	return files, nil
}

// saveUpload stores one uploaded file and returns the URL it is served at.
//
// The type is sniffed from the bytes rather than trusted from the request: the
// client-supplied Content-Type and the filename extension are both caller input.
func (h *Handler) saveUpload(ctx context.Context, fh *multipart.FileHeader, kind storage.Kind,
	facilityID, vendorID string) (string, error) {

	if limit := storage.Limit(kind); fh.Size > limit {
		return "", fmt.Errorf("%s exceeds the maximum size of %dMB", fh.Filename, limit/(1<<20))
	}
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	sniffed := http.DetectContentType(head[:n])
	ext, ok := allowedTypes(kind)[sniffed]
	if !ok {
		return "", fmt.Errorf("%s is not %s", fh.Filename, describeKind(kind))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	// Images are re-encoded before they leave this process, as Java does: a
	// phone photo arrives at 3-8MB and nothing displaying a gallery needs it
	// larger than 1920px. Formats with no stdlib encoder (WebP, GIF) fall
	// through and are stored as uploaded.
	body, size := io.Reader(f), fh.Size
	if kind == storage.Image {
		if small, ct, newExt, ok := compressImage(f); ok {
			body, size, sniffed, ext = bytes.NewReader(small), int64(len(small)), ct, newExt
			logger.Debug("image compressed", logger.Component, "storage",
				"from", fh.Size, "to", size)
		} else if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
	}

	res, err := h.media.Put(ctx, storage.Upload{
		Kind: kind, Body: body, Size: size, ContentType: sniffed, Ext: ext,
		FacilityID: facilityID, VendorID: vendorID,
	})
	if err != nil {
		return "", err
	}
	return res.URL, nil
}

// spoolUpload validates and compresses a file, then parks it in the spool
// directory for the worker instead of uploading inline.
//
// The expensive part of an upload is the network leg - 1.7-6s against S3 in
// another region - and the caller has no reason to wait for it. Validation and
// compression stay here so a bad file is still rejected with a 400 rather than
// being accepted and failing invisibly in the worker.
func (h *Handler) spoolUpload(fh *multipart.FileHeader, kind storage.Kind) (
	path, contentType, ext string, size int64, err error) {

	if limit := storage.Limit(kind); fh.Size > limit {
		return "", "", "", 0, fmt.Errorf("%s exceeds the maximum size of %dMB",
			fh.Filename, limit/(1<<20))
	}
	f, err := fh.Open()
	if err != nil {
		return "", "", "", 0, err
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	sniffed := http.DetectContentType(head[:n])
	ext, ok := allowedTypes(kind)[sniffed]
	if !ok {
		return "", "", "", 0, fmt.Errorf("%s is not %s", fh.Filename, describeKind(kind))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", "", 0, err
	}

	body, limit := io.Reader(f), fh.Size
	if kind == storage.Image {
		if small, ct, newExt, ok := compressImage(f); ok {
			body, limit, sniffed, ext = bytes.NewReader(small), int64(len(small)), ct, newExt
		} else if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", "", "", 0, err
		}
	}

	path, size, err = storage.Spool(body, ext, limit)
	if err != nil {
		return "", "", "", 0, err
	}
	return path, sniffed, ext, size, nil
}

// allowedTypes is an allowlist per kind, not a blocklist: the file is served
// back over HTTP, so accepting anything unlisted would let a caller host
// arbitrary content (an .html with a script in it, say) on this origin.
func allowedTypes(k storage.Kind) map[string]string {
	switch k {
	case storage.Video:
		return videoTypes
	case storage.Document:
		return documentTypes
	default:
		return imageTypes
	}
}

func describeKind(k storage.Kind) string {
	switch k {
	case storage.Video:
		return "an MP4 or MOV video"
	case storage.Document:
		return "a PDF or image"
	default:
		return "a JPEG, PNG, WebP or GIF"
	}
}

// baseURLOf reconstructs this server's public base URL for the stored link.
func baseURLOf(r *http.Request) string {
	if v := os.Getenv("PUBLIC_BASE_URL"); v != "" {
		return v
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ServeUploads registers the static handler for saved files.
func ServeUploads(mux *http.ServeMux) {
	fs := http.FileServer(http.Dir(uploadDir))
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", fs))
}

// decodeFormNumbers fills the numeric and boolean fields from a plain HTML
// form, where every value arrives as a string and so cannot be unmarshalled
// straight into an int or a float.
func decodeFormNumbers(r *http.Request, dst *facilityReq) {
	num := func(key string) *float64 {
		v := r.FormValue(key)
		if v == "" {
			return nil
		}
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err != nil {
			return nil
		}
		return &f
	}
	toInt := func(key string) *int {
		if f := num(key); f != nil {
			i := int(*f)
			return &i
		}
		return nil
	}
	dst.Lat, dst.Lng = num("lat"), num("lng")
	dst.BasePricePerDay = num("basePricePerDay")
	dst.StarRating = toInt("starRating")
	dst.CapacityPax = toInt("capacityPax")
	dst.AreaSqft = toInt("areaSqft")
	dst.SeatingCapacity = toInt("seatingCapacity")
	dst.FloatingCapacity = toInt("floatingCapacity")
	dst.MinBookingSize = toInt("minBookingSize")
}

// isUniqueViolation reports a Postgres unique-constraint breach (23505).
//
// Creating the same venue twice used to succeed, leaving the owner with two
// identical listings and search showing both. A unique index now refuses the
// second one, and this turns that into 409 FACILITY_EXISTS rather than a 500.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
