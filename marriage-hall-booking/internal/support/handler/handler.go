// Package handler serves the support helpline: a public read for the app and
// an admin-only write for the back office.
//
// No service layer - this is a key/value table with validation, the same shape
// internal/vendors and internal/quote use.
package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
)

type Handler struct {
	db     *pgxpool.Pool
	signer *jwt.Signer
}

func New(db *pgxpool.Pool, signer *jwt.Signer) *Handler {
	return &Handler{db: db, signer: signer}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequireAuth(h.signer),
			middleware.RequireRole(domain.RoleAdmin))
	}

	// Public and unauthenticated: a customer who cannot sign in is exactly the
	// person who needs the helpline.
	mux.HandleFunc("GET /api/v1/support", h.get)

	// Admin edits. GET here too, because the edit form needs the audit fields
	// the public read deliberately omits.
	mux.Handle("GET /api/v1/admin/support", admin(h.getAdmin))
	mux.Handle("PUT /api/v1/admin/support", admin(h.update))
}

// settingKeys maps the JSON field the UI posts to its settings row. Adding a
// support field means one entry here plus a seeded row - no schema change.
var settingKeys = map[string]string{
	"supportEmail":    "support.email",
	"supportPhone":    "support.phone",
	"supportWhatsapp": "support.whatsapp",
	"supportHours":    "support.hours",
	"supportAddress":  "support.address",
}

type supportReq struct {
	SupportEmail    *string `json:"supportEmail"`
	SupportPhone    *string `json:"supportPhone"`
	SupportWhatsapp *string `json:"supportWhatsapp"`
	SupportHours    *string `json:"supportHours"`
	SupportAddress  *string `json:"supportAddress"`
}

// fields returns only what the caller actually sent. Pointers throughout, so
// editing the phone number cannot blank the email by omission.
func (r supportReq) fields() map[string]string {
	out := map[string]string{}
	for jsonName, v := range map[string]*string{
		"supportEmail":    r.SupportEmail,
		"supportPhone":    r.SupportPhone,
		"supportWhatsapp": r.SupportWhatsapp,
		"supportHours":    r.SupportHours,
		"supportAddress":  r.SupportAddress,
	} {
		if v != nil {
			out[settingKeys[jsonName]] = strings.TrimSpace(*v)
		}
	}
	return out
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	out, _, _, err := h.load(r, false)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Support details retrieved successfully", out)
}

func (h *Handler) getAdmin(w http.ResponseWriter, r *http.Request) {
	out, updatedBy, updatedAt, err := h.load(r, true)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// Who last changed the helpline, so a wrong number has an owner.
	out["updatedBy"] = updatedBy
	out["updatedAt"] = updatedAt
	response.OK(w, "Support details retrieved successfully", out)
}

// load reads the support settings. withPrivate is false for the public route,
// which must never return a key someone marked internal.
func (h *Handler) load(r *http.Request, withPrivate bool) (map[string]any, any, any, error) {
	rows, err := h.db.Query(r.Context(), `
		SELECT s.key, COALESCE(s.value,''), u.full_name, s.updated_at
		  FROM app_settings s
		  LEFT JOIN users u ON u.id = s.updated_by
		 WHERE s.key LIKE 'support.%'
		   AND ($1 OR s.is_public)`, withPrivate)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	// Reverse of settingKeys, so the response uses the same names the PUT takes.
	jsonName := map[string]string{}
	for j, k := range settingKeys {
		jsonName[k] = j
	}

	out := map[string]any{}
	var lastBy any
	var lastAt any
	var newest time.Time
	for rows.Next() {
		var key, value string
		var by *string
		var at *time.Time
		if err := rows.Scan(&key, &value, &by, &at); err != nil {
			return nil, nil, nil, err
		}
		name := jsonName[key]
		if name == "" {
			// A support.* key with no mapping yet: return it under its raw key
			// rather than dropping it silently.
			name = key
		}
		out[name] = value
		// The most recently touched row stands for "when was support last
		// edited" - the fields are edited together from one form.
		if at != nil && at.After(newest) {
			newest, lastAt = *at, *at
			if by != nil {
				lastBy = *by
			}
		}
	}
	return out, lastBy, lastAt, rows.Err()
}

// validPhoneDisplay accepts a helpline number as an admin would type it, while
// still rejecting anything that is not a phone number. The stored value keeps
// the admin's formatting; only the digit count is checked.
func validPhoneDisplay(v string) bool {
	digits := 0
	for _, c := range v {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == '+' || c == ' ' || c == '-' || c == '(' || c == ')':
			// Formatting an admin is free to use.
		default:
			return false
		}
	}
	return digits >= 7 && digits <= 15
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var req supportReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	fields := req.fields()
	if len(fields) == 0 {
		response.Error(w, http.StatusBadRequest, "No fields to update", "VALIDATION_ERROR")
		return
	}

	var e validate.Errors
	// Blank is allowed and means "hide this in the app" - clearing the
	// WhatsApp number is a real edit. Only a non-blank value is checked.
	if v, ok := fields["support.email"]; ok && v != "" {
		e.Email("Support email", &v)
	}
	// Validated on digits only, not with validate.Phone: that enforces E.164
	// for a user's login number, while a helpline is a string humans read -
	// "+91 98765 43210" is the useful form and must survive as typed.
	if v, ok := fields["support.phone"]; ok && v != "" && !validPhoneDisplay(v) {
		e = append(e, "Support phone must be 7-15 digits, optionally with +, spaces or dashes")
	}
	if v, ok := fields["support.whatsapp"]; ok && v != "" && !validPhoneDisplay(v) {
		e = append(e, "Support WhatsApp number must be 7-15 digits, optionally with +, spaces or dashes")
	}
	// The helpline email and phone are what a stuck customer falls back on, so
	// they cannot both be blanked at once.
	if v, ok := fields["support.email"]; ok && v == "" {
		var phone string
		if p, sent := fields["support.phone"]; sent {
			phone = p
		} else {
			_ = h.db.QueryRow(r.Context(),
				`SELECT COALESCE(value,'') FROM app_settings WHERE key = 'support.phone'`).Scan(&phone)
		}
		if strings.TrimSpace(phone) == "" {
			e = append(e, "Support email and phone cannot both be empty")
		}
	}
	for _, v := range fields {
		if len(v) > 255 {
			e = append(e, "Support values must be 255 characters or fewer")
			break
		}
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	userID, _ := middleware.UserID(r.Context())
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	for key, value := range fields {
		// Upsert rather than update: a key added to settingKeys but never
		// seeded would otherwise silently accept a write that changes nothing.
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO app_settings (key, value, is_public, updated_by, updated_at)
			VALUES ($1, $2, TRUE, $3, CURRENT_TIMESTAMP)
			ON CONFLICT (key) DO UPDATE
			   SET value = EXCLUDED.value,
			       updated_by = EXCLUDED.updated_by,
			       updated_at = CURRENT_TIMESTAMP`, key, value, userID); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}

	out, updatedBy, updatedAt, err := h.load(r, true)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			response.OK(w, "Support details updated successfully", nil)
			return
		}
		httpx.Fail(w, err)
		return
	}
	out["updatedBy"] = updatedBy
	out["updatedAt"] = updatedAt
	response.OK(w, "Support details updated successfully", out)
}
