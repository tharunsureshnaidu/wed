// Package handler serves the quote request / negotiation flow: a customer asks
// for a price, the owner replies with line items, either side counters, and an
// accepted quote can be turned into a real booking.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	bookingservice "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
)

type Handler struct {
	db      *pgxpool.Pool
	signer  *jwt.Signer
	booking *bookingservice.Service
}

func New(db *pgxpool.Pool, signer *jwt.Signer, booking *bookingservice.Service) *Handler {
	return &Handler{db: db, signer: signer, booking: booking}
}

func (h *Handler) Register(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	a := func(fn http.HandlerFunc) http.Handler { return auth(fn) }

	mux.Handle("POST /api/v1/quotes/request", a(h.request))
	mux.Handle("GET /api/v1/quotes/my-requests", a(h.myRequests))
	mux.Handle("GET /api/v1/quotes/owner", a(h.ownerQuotes))
	mux.Handle("GET /api/v1/quotes/owner/stats", a(h.ownerStats))
	mux.Handle("GET /api/v1/quotes/{id}", a(h.get))
	mux.Handle("POST /api/v1/quotes/{id}/reply", a(h.reply))
	mux.Handle("POST /api/v1/quotes/{id}/counter", a(h.counter))
	mux.Handle("POST /api/v1/quotes/{id}/accept", a(h.accept))
	mux.Handle("POST /api/v1/quotes/{id}/reject", a(h.reject))
	mux.Handle("POST /api/v1/quotes/{id}/cancel", a(h.cancel))
	mux.Handle("POST /api/v1/quotes/{id}/messages", a(h.postMessage))
	mux.Handle("GET /api/v1/quotes/{id}/messages", a(h.listMessages))
	mux.Handle("POST /api/v1/quotes/{id}/attachments", a(h.addAttachment))
	mux.Handle("GET /api/v1/quotes/{id}/attachments", a(h.listAttachments))
	mux.Handle("POST /api/v1/quotes/{id}/convert-to-booking", a(h.convert))
}

type quoteParty struct {
	QuoteID    string
	CustomerID int64
	OwnerID    int64
	FacilityID string
	Status     string
	EventDate  time.Time
	EndDate    time.Time
	StartTime  *string
	EndTime    *string
	SlotType   *string
	IsCustomer bool
	IsOwner    bool
}

// load fetches a quote and establishes which side of the negotiation the caller
// is on. A quote is private to its customer, the facility's owner, and admins;
// anyone else gets 404 rather than 403, so quote ids cannot be probed.
func (h *Handler) load(w http.ResponseWriter, r *http.Request) (*quoteParty, bool) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid quote id", "VALIDATION_ERROR")
		return nil, false
	}
	var q quoteParty
	q.QuoteID = id
	err := h.db.QueryRow(r.Context(),
		`SELECT q.customer_id, f.owner_id, q.facility_id, q.status, q.event_date,
		        COALESCE(q.end_date, q.event_date), q.start_time, q.end_time, q.slot_type
		 FROM quotes q JOIN facilities f ON f.id = q.facility_id
		 WHERE q.id = $1`, id).
		Scan(&q.CustomerID, &q.OwnerID, &q.FacilityID, &q.Status, &q.EventDate,
			&q.EndDate, &q.StartTime, &q.EndTime, &q.SlotType)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Quote not found", "QUOTE_NOT_FOUND")
		return nil, false
	}
	if err != nil {
		httpx.Fail(w, err)
		return nil, false
	}

	userID, _ := middleware.UserID(r.Context())
	q.IsCustomer = userID == q.CustomerID
	q.IsOwner = userID == q.OwnerID
	if !q.IsCustomer && !q.IsOwner && !middleware.HasRole(r.Context(), domain.RoleAdmin) {
		response.Error(w, http.StatusNotFound, "Quote not found", "QUOTE_NOT_FOUND")
		return nil, false
	}
	return &q, true
}

// derefOr reads an optional string as the empty string the booking service
// expects when a time was not given.
func derefOr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// quoteHHMM normalises an optional HH:MM time, returning nil for absent so the
// column stays NULL rather than holding an empty string.
//
// ponytail: booking/handler has the same parse, unexported. Two small copies
// beat exporting a helper that would then have to keep both packages' shapes.
func quoteHHMM(v string) (*string, bool) {
	if v == "" {
		return nil, true
	}
	t, err := time.Parse("15:04", v)
	if err != nil {
		if t, err = time.Parse("15:04:05", v); err != nil {
			return nil, false
		}
	}
	out := t.Format("15:04")
	return &out, true
}

type requestReq struct {
	FacilityID string `json:"facilityId"`
	EventType  string `json:"eventType"`

	// A quote covers a date range and the hours within it, matching what a
	// booking has taken since 027 - an accepted quote converts into one, so a
	// quote that could only hold a single date was the narrower of the two.
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	StartTime string `json:"startTime"` // HH:MM, optional
	EndTime   string `json:"endTime"`   // HH:MM, optional

	// Legacy single-date form, still accepted: it is what existing clients
	// send, and rejecting it would break them for no gain.
	EventDate string `json:"eventDate"`

	SlotType            string   `json:"slotType"`
	GuestCount          *int     `json:"guestCount"`
	BudgetMin           *float64 `json:"budgetMin"`
	BudgetMax           *float64 `json:"budgetMax"`
	SpecialRequirements *string  `json:"specialRequirements"`
	PreferredContact    *string  `json:"preferredContact"`
	Message             *string  `json:"message"`
}

func (h *Handler) request(w http.ResponseWriter, r *http.Request) {
	var req requestReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	if !httpx.ValidUUID(req.FacilityID) {
		e = append(e, "A valid facilityId is required")
	}
	// startDate is the field now; eventDate is the same value under the old
	// name. Taking either here means the rest of this function - and the
	// INSERT - has one date to reason about.
	start := req.StartDate
	if start == "" {
		start = req.EventDate
	}
	eventDate, err := time.Parse("2006-01-02", start)
	if err != nil {
		e = append(e, "startDate must be YYYY-MM-DD")
	} else if eventDate.Before(time.Now().Truncate(24 * time.Hour)) {
		e = append(e, "startDate cannot be in the past")
	}

	startTime, okStart := quoteHHMM(req.StartTime)
	endTime, okEnd := quoteHHMM(req.EndTime)
	if !okStart || !okEnd {
		e = append(e, "startTime and endTime must be HH:MM")
	}

	// An absent endDate means a single-day event, not an open-ended one.
	endDate := eventDate
	if req.EndDate != "" {
		endDate, err = time.Parse("2006-01-02", req.EndDate)
		if err != nil {
			e = append(e, "endDate must be YYYY-MM-DD")
		} else if endDate.Before(eventDate) {
			// Caught here as well as by the CHECK constraint, so the client
			// gets a field-level message rather than a 500 from the driver.
			e = append(e, "endDate cannot be before startDate")
		}
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	// Its own code, as in Java: a client can tell a bad budget range from any
	// other validation failure without parsing the message.
	if req.BudgetMin != nil && req.BudgetMax != nil && *req.BudgetMin > *req.BudgetMax {
		response.Error(w, http.StatusBadRequest,
			"budgetMin cannot exceed budgetMax", "INVALID_BUDGET_RANGE")
		return
	}
	slot := req.SlotType
	if slot == "" {
		slot = "FULL_DAY"
	}

	// Quotes are a marriage-hall negotiation: the versions carry hall packages
	// and a per-event price, and converting one produces a hall booking. A
	// quote against a hotel would build a booking the hotel path cannot price.
	var facilityType string
	if err := h.db.QueryRow(r.Context(),
		`SELECT type FROM facilities WHERE id = $1 AND is_deleted = FALSE`,
		req.FacilityID).Scan(&facilityType); err != nil {
		response.Error(w, http.StatusBadRequest, "Invalid facility", "INVALID_FACILITY")
		return
	}
	if facilityType != "MARRIAGE_HALL" {
		response.Error(w, http.StatusBadRequest,
			"Quotes are only supported for marriage halls", "QUOTES_HALLS_ONLY")
		return
	}

	userID, _ := middleware.UserID(r.Context())
	var id string
	if err := h.db.QueryRow(r.Context(),
		`INSERT INTO quotes (facility_id, customer_id, event_date, end_date, start_time, end_time,
		    slot_type, guest_count, event_type, message, budget_min, budget_max,
		    special_requirements, preferred_contact)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
		req.FacilityID, userID, eventDate, endDate,
		startTime, endTime,
		slot, req.GuestCount, req.EventType,
		req.Message, req.BudgetMin, req.BudgetMax, req.SpecialRequirements,
		req.PreferredContact).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	out, err := h.viewOf(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.Created(w, "Quote request submitted successfully", "/api/v1/quotes/"+id, out)
}

type lineItem struct {
	Name      string  `json:"name"`
	Quantity  float64 `json:"quantity"`
	UnitPrice float64 `json:"unitPrice"`
}

type versionReq struct {
	Items         []lineItem `json:"items"`
	Discount      float64    `json:"discount"`
	Tax           float64    `json:"tax"`
	ServiceCharge float64    `json:"serviceCharge"`
	Notes         *string    `json:"notes"`
	ValidUntil    *string    `json:"validUntil"`
}

// reply is the owner quoting a price. counter is the customer proposing a
// different one; both create a new immutable version.
func (h *Handler) reply(w http.ResponseWriter, r *http.Request) {
	h.addVersion(w, r, "OWNER", "REPLIED")
}

func (h *Handler) counter(w http.ResponseWriter, r *http.Request) {
	h.addVersion(w, r, "CUSTOMER", "COUNTERED")
}

func (h *Handler) addVersion(w http.ResponseWriter, r *http.Request, role, newStatus string) {
	q, ok := h.load(w, r)
	if !ok {
		return
	}
	// Only the owner may reply, only the customer may counter.
	if role == "OWNER" && !q.IsOwner {
		response.Error(w, http.StatusForbidden, "Only the venue owner can reply", "NOT_QUOTE_OWNER")
		return
	}
	if role == "CUSTOMER" && !q.IsCustomer {
		response.Error(w, http.StatusForbidden, "Only the customer can counter", "NOT_QUOTE_CUSTOMER")
		return
	}
	// A settled quote is not negotiable any more.
	switch q.Status {
	case "ACCEPTED", "REJECTED", "CANCELLED", "CONVERTED":
		response.Error(w, http.StatusConflict,
			"Quote is already "+q.Status, "INVALID_STATE")
		return
	}

	var req versionReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	if len(req.Items) == 0 {
		e = append(e, "At least one line item is required")
	}
	for _, it := range req.Items {
		if it.Name == "" {
			e = append(e, "Every item needs a name")
			break
		}
		if it.Quantity <= 0 || it.UnitPrice < 0 {
			e = append(e, "Item quantity must be positive and unitPrice non-negative")
			break
		}
	}
	if req.Discount < 0 || req.Tax < 0 || req.ServiceCharge < 0 {
		e = append(e, "discount, tax and serviceCharge cannot be negative")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	// Totals are computed here, never taken from the request - a client-supplied
	// total is a client-chosen price.
	subtotal := 0.0
	for _, it := range req.Items {
		subtotal += it.Quantity * it.UnitPrice
	}
	if req.Discount > subtotal {
		response.Error(w, http.StatusBadRequest,
			"Discount cannot exceed the subtotal", "VALIDATION_ERROR")
		return
	}
	total := subtotal - req.Discount + req.Tax + req.ServiceCharge

	itemsJSON, err := json.Marshal(req.Items)
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	var versionNo int
	if err := tx.QueryRow(r.Context(),
		`SELECT COALESCE(max(version_no), 0) + 1 FROM quote_versions WHERE quote_id = $1`,
		q.QuoteID).Scan(&versionNo); err != nil {
		httpx.Fail(w, err)
		return
	}

	userID, _ := middleware.UserID(r.Context())
	var versionID string
	if err := tx.QueryRow(r.Context(),
		`INSERT INTO quote_versions (quote_id, version_no, created_by_role, created_by,
		    items, subtotal, discount, tax, service_charge, total, notes, valid_until)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::date) RETURNING id`,
		q.QuoteID, versionNo, role, userID, itemsJSON, subtotal, req.Discount,
		req.Tax, req.ServiceCharge, total, req.Notes, req.ValidUntil).Scan(&versionID); err != nil {
		httpx.Fail(w, err)
		return
	}
	if _, err := tx.Exec(r.Context(),
		// valid_until is carried up from the version so QuoteRequestDTO.validUntil
		// reflects the offer currently on the table.
		`UPDATE quotes SET status = $2, quoted_amount = $3,
		    valid_until = COALESCE($4::date, valid_until), updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1`, q.QuoteID, newStatus, total, req.ValidUntil); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "Quote "+newStatus+" successfully", map[string]any{
		"quoteId": q.QuoteID, "versionId": versionID, "versionNo": versionNo,
		"status": newStatus, "subtotal": subtotal, "discount": req.Discount,
		"tax": req.Tax, "serviceCharge": req.ServiceCharge, "total": total,
	})
}

// messageView is Java's QuoteMessageDTO; attachmentView its QuoteAttachmentDTO.
// Both carry the pre-existing spellings alongside Java's (senderName/content
// and contentType/sizeBytes shipped before this alignment).
type messageView struct {
	ID            int64     `json:"id"`
	SenderID      int64     `json:"senderId"`
	SenderName    string    `json:"senderName"`
	MessageType   string    `json:"messageType"`
	Content       string    `json:"content"`
	AttachmentURL *string   `json:"attachmentUrl"`
	CreatedAt     time.Time `json:"createdAt"`
}

type attachmentView struct {
	ID          string    `json:"id"`
	FileName    string    `json:"fileName"`
	FileURL     string    `json:"fileUrl"`
	FileType    *string   `json:"fileType"`
	FileSize    *int64    `json:"fileSize"`
	ContentType *string   `json:"contentType"`
	SizeBytes   *int64    `json:"sizeBytes"`
	UploadedBy  int64     `json:"uploadedBy"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (h *Handler) messagesOf(ctx context.Context, quoteID string) ([]messageView, error) {
	rows, err := h.db.Query(ctx,
		`SELECT m.id, m.sender_id, u.full_name, COALESCE(m.message_type,'TEXT'), m.message,
		        m.created_at
		 FROM quote_messages m JOIN users u ON u.id = m.sender_id
		 WHERE m.quote_id = $1 ORDER BY m.created_at`, quoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []messageView{}
	for rows.Next() {
		var x messageView
		if err := rows.Scan(&x.ID, &x.SenderID, &x.SenderName, &x.MessageType,
			&x.Content, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (h *Handler) attachmentsOf(ctx context.Context, quoteID string) ([]attachmentView, error) {
	rows, err := h.db.Query(ctx,
		`SELECT id, file_name, file_url, content_type, size_bytes, uploaded_by, created_at
		 FROM quote_attachments WHERE quote_id = $1 ORDER BY created_at`, quoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []attachmentView{}
	for rows.Next() {
		var x attachmentView
		if err := rows.Scan(&x.ID, &x.FileName, &x.FileURL, &x.ContentType,
			&x.SizeBytes, &x.UploadedBy, &x.CreatedAt); err != nil {
			return nil, err
		}
		x.FileType, x.FileSize = x.ContentType, x.SizeBytes
		out = append(out, x)
	}
	return out, rows.Err()
}

// quoteView is Java's QuoteRequestDTO. Both the create and the get response
// are built from it, so POST and GET cannot describe the same quote differently.
type quoteView struct {
	ID           string  `json:"id"`
	FacilityID   string  `json:"facilityId"`
	FacilityName string  `json:"facilityName"`
	CustomerID   int64   `json:"customerId"`
	OwnerID      int64   `json:"ownerId"`
	Status       string  `json:"status"`
	EventType    *string `json:"eventType"`
	// startDate/endDate are the fields; eventDate repeats startDate under its
	// old name so existing clients keep reading the quote they always did.
	StartDate string  `json:"startDate"`
	EndDate   string  `json:"endDate"`
	StartTime *string `json:"startTime"`
	EndTime   *string `json:"endTime"`
	EventDate string  `json:"eventDate"`

	SlotType            *string    `json:"slotType"`
	GuestCount          *int       `json:"guestCount"`
	BudgetMin           *float64   `json:"budgetMin"`
	BudgetMax           *float64   `json:"budgetMax"`
	SpecialRequirements *string    `json:"specialRequirements"`
	PreferredContact    *string    `json:"preferredContact"`
	ValidUntil          *time.Time `json:"validUntil"`
	QuotedAmount        *float64   `json:"quotedAmount"`
	BookingID           *string    `json:"bookingId"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`

	Versions    []versionView    `json:"versions"`
	Messages    []messageView    `json:"messages"`
	Attachments []attachmentView `json:"attachments"`
}

func (h *Handler) viewOf(ctx context.Context, quoteID string) (*quoteView, error) {
	var out quoteView
	if err := h.db.QueryRow(ctx,
		`SELECT q.id, q.facility_id, f.name, q.customer_id, f.owner_id, q.status,
		        q.event_type, to_char(q.event_date,'YYYY-MM-DD'),
		        to_char(COALESCE(q.end_date, q.event_date),'YYYY-MM-DD'),
		        q.start_time, q.end_time, q.slot_type,
		        q.guest_count, q.budget_min, q.budget_max, q.special_requirements,
		        q.preferred_contact, q.valid_until, q.quoted_amount, q.booking_id,
		        q.created_at, q.updated_at
		 FROM quotes q JOIN facilities f ON f.id = q.facility_id WHERE q.id = $1`,
		quoteID).Scan(&out.ID, &out.FacilityID, &out.FacilityName, &out.CustomerID,
		&out.OwnerID, &out.Status, &out.EventType, &out.StartDate, &out.EndDate,
		&out.StartTime, &out.EndTime, &out.SlotType,
		&out.GuestCount, &out.BudgetMin, &out.BudgetMax, &out.SpecialRequirements,
		&out.PreferredContact, &out.ValidUntil, &out.QuotedAmount, &out.BookingID,
		&out.CreatedAt, &out.UpdatedAt); err != nil {
		return nil, err
	}
	out.EventDate = out.StartDate
	var err error
	if out.Versions, err = h.versionsOf(ctx, quoteID); err != nil {
		return nil, err
	}
	if out.Messages, err = h.messagesOf(ctx, quoteID); err != nil {
		return nil, err
	}
	if out.Attachments, err = h.attachmentsOf(ctx, quoteID); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	q, ok := h.load(w, r)
	if !ok {
		return
	}
	out, err := h.viewOf(r.Context(), q.QuoteID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// "quote"/"versions" kept alongside the flat fields: both shipped already.
	response.OK(w, "Quote retrieved successfully", map[string]any{
		"id": out.ID, "facilityId": out.FacilityID, "facilityName": out.FacilityName,
		"customerId": out.CustomerID, "ownerId": out.OwnerID, "status": out.Status,
		"eventType": out.EventType, "eventDate": out.EventDate, "slotType": out.SlotType,
		"guestCount": out.GuestCount, "budgetMin": out.BudgetMin, "budgetMax": out.BudgetMax,
		"specialRequirements": out.SpecialRequirements, "preferredContact": out.PreferredContact,
		"validUntil": out.ValidUntil, "quotedAmount": out.QuotedAmount,
		"bookingId": out.BookingID, "createdAt": out.CreatedAt, "updatedAt": out.UpdatedAt,
		"versions": out.Versions, "messages": out.Messages, "attachments": out.Attachments,
		"quote": out,
	})
}

type versionView struct {
	ID            string     `json:"id"`
	VersionNo     int        `json:"versionNo"`
	CreatedByRole string     `json:"createdByRole"`
	Items         []lineItem `json:"items"`
	Subtotal      float64    `json:"subtotal"`
	Discount      float64    `json:"discount"`
	Tax           float64    `json:"tax"`
	ServiceCharge float64    `json:"serviceCharge"`
	Total         float64    `json:"total"`
	Notes         *string    `json:"notes"`
	CreatedAt     time.Time  `json:"createdAt"`
}

func (h *Handler) versionsOf(ctx context.Context, quoteID string) ([]versionView, error) {
	rows, err := h.db.Query(ctx,
		`SELECT id, version_no, created_by_role, items, subtotal, discount, tax,
		        service_charge, total, notes, created_at
		 FROM quote_versions WHERE quote_id = $1 ORDER BY version_no`, quoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []versionView{}
	for rows.Next() {
		var v versionView
		var raw []byte
		if err := rows.Scan(&v.ID, &v.VersionNo, &v.CreatedByRole, &raw, &v.Subtotal,
			&v.Discount, &v.Tax, &v.ServiceCharge, &v.Total, &v.Notes, &v.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal(raw, &v.Items)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (h *Handler) myRequests(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	h.listQuotes(w, r, `q.customer_id = $1`, userID)
}

func (h *Handler) ownerQuotes(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	h.listQuotes(w, r, `f.owner_id = $1`, userID)
}

func (h *Handler) listQuotes(w http.ResponseWriter, r *http.Request, where string, userID int64) {
	page, size := httpx.Page(r)
	status := r.URL.Query().Get("status")

	var total int64
	if err := h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM quotes q JOIN facilities f ON f.id = q.facility_id
		 WHERE `+where+` AND ($2 = '' OR q.status = $2)`, userID, status).Scan(&total); err != nil {
		httpx.Fail(w, err)
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT q.id, q.facility_id, f.name, q.event_type,
		        to_char(q.event_date,'YYYY-MM-DD'),
		        to_char(COALESCE(q.end_date, q.event_date),'YYYY-MM-DD'),
		        q.start_time, q.end_time, q.guest_count, q.status, q.quoted_amount
		 FROM quotes q JOIN facilities f ON f.id = q.facility_id
		 WHERE `+where+` AND ($2 = '' OR q.status = $2)
		 ORDER BY q.created_at DESC LIMIT $3 OFFSET $4`, userID, status, size, page*size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID           string   `json:"id"`
		FacilityID   string   `json:"facilityId"`
		FacilityName string   `json:"facilityName"`
		EventType    *string  `json:"eventType"`
		StartDate    string   `json:"startDate"`
		EndDate      string   `json:"endDate"`
		StartTime    *string  `json:"startTime"`
		EndTime      *string  `json:"endTime"`
		EventDate    string   `json:"eventDate"` // mirrors startDate, for old clients
		GuestCount   *int     `json:"guestCount"`
		Status       string   `json:"status"`
		QuotedAmount *float64 `json:"quotedAmount"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.FacilityID, &x.FacilityName, &x.EventType,
			&x.StartDate, &x.EndDate, &x.StartTime, &x.EndTime,
			&x.GuestCount, &x.Status, &x.QuotedAmount); err != nil {
			httpx.Fail(w, err)
			return
		}
		x.EventDate = x.StartDate
		out = append(out, x)
	}
	response.OK(w, "Quotes retrieved successfully", httpx.NewPaged(out, page, size, total))
}

func (h *Handler) ownerStats(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	// Java's QuoteStatsDTO is totalRequests/pending/accepted/rejected/booked/
	// conversionRate; pending is anything still awaiting a decision and booked
	// is a converted quote. The finer per-status counts shipped already.
	var s struct {
		TotalRequests  int64   `json:"totalRequests"`
		Pending        int64   `json:"pending"`
		Accepted       int64   `json:"accepted"`
		Rejected       int64   `json:"rejected"`
		Booked         int64   `json:"booked"`
		ConversionRate float64 `json:"conversionRate"`

		Total     int64    `json:"total"`
		Requested int64    `json:"requested"`
		Replied   int64    `json:"replied"`
		Countered int64    `json:"countered"`
		Converted int64    `json:"converted"`
		AvgQuoted *float64 `json:"avgQuotedAmount"`
	}
	err := h.db.QueryRow(r.Context(),
		`SELECT count(*),
		        count(*) FILTER (WHERE q.status = 'REQUESTED'),
		        count(*) FILTER (WHERE q.status = 'REPLIED'),
		        count(*) FILTER (WHERE q.status = 'COUNTERED'),
		        count(*) FILTER (WHERE q.status = 'ACCEPTED'),
		        count(*) FILTER (WHERE q.status = 'REJECTED'),
		        count(*) FILTER (WHERE q.status = 'CONVERTED'),
		        avg(q.quoted_amount)
		 FROM quotes q JOIN facilities f ON f.id = q.facility_id
		 WHERE f.owner_id = $1`, userID).
		Scan(&s.Total, &s.Requested, &s.Replied, &s.Countered, &s.Accepted,
			&s.Rejected, &s.Converted, &s.AvgQuoted)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.TotalRequests = s.Total
	s.Pending = s.Requested + s.Replied + s.Countered
	s.Booked = s.Converted
	if s.Total > 0 {
		s.ConversionRate = float64(s.Converted) / float64(s.Total)
	}
	response.OK(w, "Quote stats retrieved successfully", s)
}

func (h *Handler) accept(w http.ResponseWriter, r *http.Request) {
	q, ok := h.load(w, r)
	if !ok {
		return
	}
	// Either side can accept, but only a quote that actually carries a price.
	if q.Status != "REPLIED" && q.Status != "COUNTERED" {
		response.Error(w, http.StatusConflict,
			"Only a quoted or countered quote can be accepted", "INVALID_STATUS_TRANSITION")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`UPDATE quotes SET status = 'ACCEPTED', updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		q.QuoteID); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Quote accepted successfully", map[string]any{
		"id": q.QuoteID, "status": "ACCEPTED",
	})
}

type reasonReq struct {
	Reason string `json:"reason"`
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	h.settle(w, r, "REJECTED", "rejected")
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	h.settle(w, r, "CANCELLED", "cancelled")
}

func (h *Handler) settle(w http.ResponseWriter, r *http.Request, status, verb string) {
	q, ok := h.load(w, r)
	if !ok {
		return
	}
	switch q.Status {
	case "CONVERTED", "ACCEPTED":
		response.Error(w, http.StatusConflict,
			"Quote is already "+q.Status, "INVALID_STATE")
		return
	}
	var req reasonReq
	if r.ContentLength > 0 && !httpx.Decode(w, r, &req) {
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`UPDATE quotes SET status = $2, rejection_reason = NULLIF($3,''),
		    updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		q.QuoteID, status, req.Reason); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Quote "+verb+" successfully", map[string]any{
		"id": q.QuoteID, "status": status,
	})
}

type messageReq struct {
	MessageType string `json:"messageType"`
	Content     string `json:"content"`
	Message     string `json:"message"`
}

func (h *Handler) postMessage(w http.ResponseWriter, r *http.Request) {
	q, ok := h.load(w, r)
	if !ok {
		return
	}
	var req messageReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	content := req.Content
	if content == "" {
		content = req.Message
	}
	if content == "" {
		response.Error(w, http.StatusBadRequest, "content is required", "VALIDATION_ERROR")
		return
	}
	msgType := req.MessageType
	if msgType == "" {
		msgType = "TEXT"
	}

	userID, _ := middleware.UserID(r.Context())
	var id int64
	if err := h.db.QueryRow(r.Context(),
		`INSERT INTO quote_messages (quote_id, sender_id, message, message_type)
		 VALUES ($1,$2,$3,$4) RETURNING id`,
		q.QuoteID, userID, content, msgType).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Message sent successfully", map[string]any{
		"id": id, "quoteId": q.QuoteID, "content": content, "messageType": msgType,
	})
}

func (h *Handler) listMessages(w http.ResponseWriter, r *http.Request) {
	q, ok := h.load(w, r)
	if !ok {
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT m.id, m.sender_id, u.full_name, m.message,
		        COALESCE(m.message_type,'TEXT'), m.created_at
		 FROM quote_messages m JOIN users u ON u.id = m.sender_id
		 WHERE m.quote_id = $1 ORDER BY m.created_at`, q.QuoteID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID          int64     `json:"id"`
		SenderID    int64     `json:"senderId"`
		SenderName  string    `json:"senderName"`
		Content     string    `json:"content"`
		MessageType string    `json:"messageType"`
		CreatedAt   time.Time `json:"createdAt"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.SenderID, &x.SenderName, &x.Content,
			&x.MessageType, &x.CreatedAt); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	response.OK(w, "Messages retrieved successfully", out)
}

type attachmentReq struct {
	FileName    string  `json:"fileName"`
	FileURL     string  `json:"fileUrl"`
	ContentType *string `json:"contentType"`
	SizeBytes   *int64  `json:"sizeBytes"`
}

// addAttachment records a reference to an already-uploaded file. There is no
// object store wired up here, so the URL is supplied by the caller rather than
// produced by an upload.
func (h *Handler) addAttachment(w http.ResponseWriter, r *http.Request) {
	q, ok := h.load(w, r)
	if !ok {
		return
	}
	var req attachmentReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("fileName", req.FileName)
	e.Required("fileUrl", req.FileURL)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var id string
	if err := h.db.QueryRow(r.Context(),
		`INSERT INTO quote_attachments (quote_id, uploaded_by, file_name, file_url,
		    content_type, size_bytes)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		q.QuoteID, userID, req.FileName, req.FileURL, req.ContentType, req.SizeBytes).
		Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Attachment added successfully", map[string]any{
		"id": id, "quoteId": q.QuoteID, "fileName": req.FileName, "fileUrl": req.FileURL,
	})
}

func (h *Handler) listAttachments(w http.ResponseWriter, r *http.Request) {
	q, ok := h.load(w, r)
	if !ok {
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT id, file_name, file_url, content_type, size_bytes, uploaded_by, created_at
		 FROM quote_attachments WHERE quote_id = $1 ORDER BY created_at`, q.QuoteID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID          string    `json:"id"`
		FileName    string    `json:"fileName"`
		FileURL     string    `json:"fileUrl"`
		ContentType *string   `json:"contentType"`
		SizeBytes   *int64    `json:"sizeBytes"`
		UploadedBy  int64     `json:"uploadedBy"`
		CreatedAt   time.Time `json:"createdAt"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.FileName, &x.FileURL, &x.ContentType,
			&x.SizeBytes, &x.UploadedBy, &x.CreatedAt); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	response.OK(w, "Attachments retrieved successfully", out)
}

type convertReq struct {
	GuestName     *string `json:"guestName"`
	GuestEmail    *string `json:"guestEmail"`
	GuestPhone    *string `json:"guestPhone"`
	IdempotentKey string  `json:"idempotentKey"`
}

// convert turns an accepted quote into a real booking, reusing the booking
// service so the slot claim, idempotency and pricing rules are identical to a
// direct booking. The agreed quote total overrides the hall's list price.
func (h *Handler) convert(w http.ResponseWriter, r *http.Request) {
	q, ok := h.load(w, r)
	if !ok {
		return
	}
	if !q.IsCustomer && !middleware.HasRole(r.Context(), domain.RoleAdmin) {
		response.Error(w, http.StatusForbidden,
			"Only the customer can convert their quote", "NOT_QUOTE_CUSTOMER")
		return
	}
	if q.Status != "ACCEPTED" {
		response.Error(w, http.StatusConflict,
			"Only an ACCEPTED quote can be converted to a booking", "INVALID_STATUS_TRANSITION")
		return
	}

	var req convertReq
	if r.ContentLength > 0 && !httpx.Decode(w, r, &req) {
		return
	}
	if req.IdempotentKey == "" {
		req.IdempotentKey = "quote-" + q.QuoteID
	}

	var agreed *float64
	if err := h.db.QueryRow(r.Context(),
		`SELECT quoted_amount FROM quotes WHERE id = $1`, q.QuoteID).Scan(&agreed); err != nil {
		httpx.Fail(w, err)
		return
	}
	if agreed == nil {
		// Java: a quote with no version has nothing to convert.
		response.Error(w, http.StatusConflict,
			"Quote has no version to convert", "NO_QUOTE_VERSION")
		return
	}

	slot := "FULL_DAY"
	if q.SlotType != nil && *q.SlotType != "" {
		slot = *q.SlotType
	}

	b, err := h.booking.CreateHallBooking(r.Context(), q.CustomerID, bookingservice.HallBookingRequest{
		FacilityID: q.FacilityID, EventDate: q.EventDate, EndDate: q.EndDate,
		StartTime: derefOr(q.StartTime), EndTime: derefOr(q.EndTime), SlotType: slot,
		IdempotentKey: req.IdempotentKey, GuestName: req.GuestName,
		GuestEmail: req.GuestEmail, GuestPhone: req.GuestPhone,
		OverrideAmount: agreed,
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	if _, err := h.db.Exec(r.Context(),
		`UPDATE quotes SET status = 'CONVERTED', booking_id = $2,
		    updated_at = CURRENT_TIMESTAMP WHERE id = $1`, q.QuoteID, b.ID); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Quote converted to booking successfully", b)
}
