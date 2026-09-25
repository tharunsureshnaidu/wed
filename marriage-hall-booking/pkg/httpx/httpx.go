// Package httpx holds the request/response helpers every module's handlers share.
package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/tripfcatory/marriage-hall-booking/pkg/apperr"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
)

func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst); err != nil {
		response.Error(w, http.StatusBadRequest, "Malformed request body", "MALFORMED_JSON")
		return false
	}
	return true
}

// Fail maps an apperr to its status; anything else is a bug whose detail must
// not reach the caller.
func Fail(w http.ResponseWriter, err error) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		response.Error(w, ae.Status, ae.Message, ae.Code)
		return
	}
	logger.Error("unhandled error", logger.Err(err))
	response.Error(w, http.StatusInternalServerError, "Something went wrong", "INTERNAL_ERROR")
}

// IP is the caller's address. Deprecated in favour of ClientIP, which it now
// delegates to: this version trusted X-Forwarded-For from anyone, and it keys
// the anonymous search-history bucket - a forged header let one visitor read
// another's history.
func IP(r *http.Request) string { return ClientIP(r) }

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func ValidUUID(s string) bool { return uuidRe.MatchString(s) }

// Page reads page/size query params, clamping size so a caller cannot ask for
// the entire table in one request.
func Page(r *http.Request) (page, size int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	size, _ = strconv.Atoi(r.URL.Query().Get("size"))
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

type Paged struct {
	Content       any   `json:"content"`
	Page          int   `json:"page"`
	Size          int   `json:"size"`
	TotalElements int64 `json:"totalElements"`
	TotalPages    int   `json:"totalPages"`
}

func NewPaged(content any, page, size int, total int64) Paged {
	pages := 0
	if size > 0 {
		pages = int((total + int64(size) - 1) / int64(size))
	}
	return Paged{Content: content, Page: page, Size: size, TotalElements: total, TotalPages: pages}
}
