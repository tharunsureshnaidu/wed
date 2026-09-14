// Package storage stores uploaded media and returns a public URL for it.
//
// Two implementations: S3 for real deployments and local disk for development,
// chosen by whether AWS_S3_BUCKET is set. Both satisfy Store, so callers never
// branch on which one is active.
package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Kind selects the key prefix a file is stored under, mirroring the layout
// Java's S3UploadService writes.
type Kind int

const (
	Image Kind = iota
	Video
	Document
)

// Upload is one file on its way to storage.
type Upload struct {
	Kind        Kind
	Body        io.Reader
	Size        int64
	ContentType string
	Ext         string // with the leading dot, e.g. ".jpg"

	// FacilityID and VendorID namespace the key. Both may be empty - a document
	// is not venue-owned, and a facility created by a non-vendor owner has no
	// vendor to namespace by.
	FacilityID string
	VendorID   string
}

// Result is what the caller records against the media row.
type Result struct {
	URL  string
	Key  string
	Size int64
}

// Store is the seam between the handlers and wherever bytes actually live.
type Store interface {
	Put(ctx context.Context, up Upload) (Result, error)
	Delete(ctx context.Context, url string) error
	// Name identifies the backend in logs and on the health endpoint, so a
	// deployment accidentally running on local disk is visible rather than
	// silently losing media on the next redeploy.
	Name() string
}

// Per-type limits, matching Java's S3UploadService.
const (
	MaxImageBytes    = 10 << 20  // 10MB
	MaxVideoBytes    = 200 << 20 // 200MB
	MaxDocumentBytes = 20 << 20  // 20MB
)

// Limit reports the maximum byte size allowed for a kind.
func Limit(k Kind) int64 {
	switch k {
	case Video:
		return MaxVideoBytes
	case Document:
		return MaxDocumentBytes
	default:
		return MaxImageBytes
	}
}

// keyFor builds the object key. Java's layout:
//
//	vendors/vendor-{vendorId}/venue-{facilityId}/gallery/{uuid}.{ext}
//	vendors/vendor-{vendorId}/venue-{facilityId}/videos/{uuid}.{ext}
//	venues/{facilityId}/...                     (facility with no vendor)
//	documents/{uuid}.{ext}                      (quote attachments)
func keyFor(up Upload, name string) string {
	if up.Kind == Document {
		return "documents/" + name
	}
	folder := "gallery/"
	if up.Kind == Video {
		folder = "videos/"
	}
	var prefix string
	switch {
	case up.VendorID != "" && up.FacilityID != "":
		prefix = "vendors/vendor-" + up.VendorID + "/venue-" + up.FacilityID + "/"
	case up.FacilityID != "":
		prefix = "venues/" + up.FacilityID + "/"
	default:
		// No venue context at all: keep it out of the venue namespaces rather
		// than writing to a path that looks like it belongs to one.
		prefix = "misc/"
	}
	return prefix + folder + name
}

// ErrTooLarge is returned when an upload exceeds its kind's limit.
type ErrTooLarge struct {
	Kind  Kind
	Limit int64
}

func (e ErrTooLarge) Error() string {
	return fmt.Sprintf("file exceeds the maximum size of %dMB", e.Limit/(1<<20))
}

// trimSlash keeps URL joins from doubling a separator.
func trimSlash(s string) string { return strings.TrimSuffix(s, "/") }
