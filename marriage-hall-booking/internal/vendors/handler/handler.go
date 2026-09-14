package handler

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/jwt"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
	"github.com/tripfcatory/marriage-hall-booking/pkg/validate"
)

type Handler struct {
	db     *pgxpool.Pool
	signer *jwt.Signer
}

func New(db *pgxpool.Pool, signer *jwt.Signer) *Handler {
	return &Handler{db: db, signer: signer}
}

func (h *Handler) Register(mux *http.ServeMux) {
	vendor := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequireAuth(h.signer),
			middleware.RequireRole(domain.RoleHallOwner, domain.RoleAdmin))
	}
	mux.Handle("GET /api/v1/vendors/me", vendor(h.get))
	mux.Handle("PUT /api/v1/vendors/me", vendor(h.upsert))
	mux.Handle("PUT /api/v1/vendors/me/kyc", vendor(h.updateKyc))
	mux.Handle("PUT /api/v1/vendors/me/bank-account", vendor(h.updateBank))
	mux.Handle("GET /api/v1/vendors/me/properties", vendor(h.properties))
	mux.Handle("GET /api/v1/vendors/dashboard", vendor(h.dashboard))
}

// vendorView mirrors Java's VendorResponse.
type vendorView struct {
	ID        string     `json:"id"`
	UserID    int64      `json:"userId"`
	Status    string     `json:"status"`
	Kyc       *kycView   `json:"kyc"`
	AppliedAt *time.Time `json:"appliedAt"`

	BusinessName               string  `json:"businessName"`
	BusinessAddress            *string `json:"businessAddress"`
	BusinessDescription        *string `json:"businessDescription"`
	BusinessType               *string `json:"businessType"`
	BusinessTypeOther          *string `json:"businessTypeOther"`
	BusinessLogoURL            *string `json:"businessLogoUrl"`
	CoverImageURL              *string `json:"coverImageUrl"`
	BusinessPhone              *string `json:"businessPhone"`
	BusinessEmail              *string `json:"businessEmail"`
	Website                    *string `json:"website"`
	UpiID                      *string `json:"upiId"`
	BusinessRegistrationNumber *string `json:"businessRegistrationNumber"`
	SupportContact             *string `json:"supportContact"`
	BankAccountOnFile          bool    `json:"bankAccountOnFile"`

	// The account behind the vendor, joined from users.
	FullName    string  `json:"fullName"`
	Email       *string `json:"email"`
	PhoneNumber *string `json:"phoneNumber"`

	TotalHotels int64    `json:"totalHotels"`
	TotalHalls  int64    `json:"totalHalls"`
	FacilityIDs []string `json:"facilityIds"`

	// Kept from before this alignment - Java exposes the document and bank
	// details through separate admin endpoints, but these already shipped.
	KycStatus         string  `json:"kycStatus"`
	KycDocumentURL    *string `json:"kycDocumentUrl"`
	BankAccountMasked *string `json:"bankAccountMasked"`
	BankIFSC          *string `json:"bankIfsc"`
	BankHolderName    *string `json:"bankHolderName"`
}

// kycView is Java's VendorResponse.KycDetails.
type kycView struct {
	BusinessName    *string `json:"businessName"`
	BusinessAddress *string `json:"businessAddress"`
	GstNumber       *string `json:"gstNumber"`
	PanNumber       *string `json:"panNumber"`
	Status          string  `json:"status"`
	RejectionReason *string `json:"rejectionReason"`
}

func (h *Handler) load(r *http.Request, userID int64) (*vendorView, error) {
	var v vendorView
	var kyc kycView
	err := h.db.QueryRow(r.Context(),
		`SELECT v.id, v.user_id, v.status, v.created_at,
		        v.business_name, v.business_address, v.business_description,
		        v.business_type, v.business_type_other, v.business_logo_url,
		        v.cover_image_url, v.business_phone, v.business_email, v.website,
		        v.upi_id, v.business_registration_number, v.support_contact,
		        v.kyc_status, v.kyc_document_url, v.bank_account_masked,
		        v.bank_ifsc, v.bank_holder_name,
		        v.kyc_business_name, v.kyc_business_address, v.kyc_gst_number,
		        v.kyc_pan_number, v.kyc_rejection_reason,
		        u.full_name, u.email, u.phone_number,
		        COALESCE((SELECT count(*) FROM facilities f
		                   WHERE f.vendor_id = v.id AND f.type = 'HOTEL' AND f.is_deleted = FALSE),0),
		        COALESCE((SELECT count(*) FROM facilities f
		                   WHERE f.vendor_id = v.id AND f.type = 'MARRIAGE_HALL' AND f.is_deleted = FALSE),0),
		        COALESCE((SELECT array_agg(f.id::text ORDER BY f.created_at)
		                    FROM facilities f
		                   WHERE f.vendor_id = v.id AND f.is_deleted = FALSE), '{}')
		 FROM vendors v JOIN users u ON u.id = v.user_id
		 WHERE v.user_id = $1 AND v.is_deleted = FALSE`, userID).
		Scan(&v.ID, &v.UserID, &v.Status, &v.AppliedAt,
			&v.BusinessName, &v.BusinessAddress, &v.BusinessDescription,
			&v.BusinessType, &v.BusinessTypeOther, &v.BusinessLogoURL,
			&v.CoverImageURL, &v.BusinessPhone, &v.BusinessEmail, &v.Website,
			&v.UpiID, &v.BusinessRegistrationNumber, &v.SupportContact,
			&v.KycStatus, &v.KycDocumentURL, &v.BankAccountMasked,
			&v.BankIFSC, &v.BankHolderName,
			&kyc.BusinessName, &kyc.BusinessAddress, &kyc.GstNumber,
			&kyc.PanNumber, &kyc.RejectionReason,
			&v.FullName, &v.Email, &v.PhoneNumber,
			&v.TotalHotels, &v.TotalHalls, &v.FacilityIDs)
	if err != nil {
		return &v, err
	}
	// Java omits the whole kyc block until KYC has actually been submitted.
	if kyc.BusinessName != nil || kyc.GstNumber != nil || kyc.PanNumber != nil {
		kyc.Status = v.KycStatus
		v.Kyc = &kyc
	}
	v.BankAccountOnFile = v.BankAccountMasked != nil
	if v.FacilityIDs == nil {
		v.FacilityIDs = []string{}
	}
	return &v, nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	v, err := h.load(r, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Vendor profile not found", "VENDOR_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Vendor retrieved successfully", v)
}

// vendorReq mirrors Java's UpdateVendorProfileRequest: every field is optional,
// so businessName is a pointer like the rest. Absent means "leave it alone",
// which is why it is not a plain string - a plain string cannot distinguish
// "not sent" from "sent as empty".
type vendorReq struct {
	BusinessName               *string `json:"businessName"`
	BusinessAddress            *string `json:"businessAddress"`
	BusinessDescription        *string `json:"businessDescription"`
	BusinessType               *string `json:"businessType"`
	BusinessTypeOther          *string `json:"businessTypeOther"`
	BusinessLogoURL            *string `json:"businessLogoUrl"`
	CoverImageURL              *string `json:"coverImageUrl"`
	BusinessPhone              *string `json:"businessPhone"`
	BusinessEmail              *string `json:"businessEmail"`
	Website                    *string `json:"website"`
	UpiID                      *string `json:"upiId"`
	BusinessRegistrationNumber *string `json:"businessRegistrationNumber"`
	SupportContact             *string `json:"supportContact"`
}

func (h *Handler) upsert(w http.ResponseWriter, r *http.Request) {
	var req vendorReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	// Java's field-level constraints, reproduced. Without these an invalid email
	// or an over-length name reaches the database and surfaces as a 500 rather
	// than a 400 naming the field.
	var e validate.Errors
	e.MaxLength("Business name", req.BusinessName, 255)
	e.MaxLength("Business address", req.BusinessAddress, 2000)
	e.MaxLength("Business description", req.BusinessDescription, 2000)
	e.MaxLength("Business logo URL", req.BusinessLogoURL, 500)
	e.MaxLength("Cover image URL", req.CoverImageURL, 500)
	e.MaxLength("Business registration number", req.BusinessRegistrationNumber, 50)
	e.MaxLength("Support contact", req.SupportContact, 255)
	e.Phone("Business phone", req.BusinessPhone)
	e.Email("Business email", req.BusinessEmail)
	e.URL("Website", req.Website)
	e.UpiID("UPI id", req.UpiID)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	userID, _ := middleware.UserID(r.Context())

	// business_name is NOT NULL, so the first save has to carry one; later saves
	// may omit it. Java draws the same line between CreateVendorBusinessRequest
	// (name required) and UpdateVendorProfileRequest (everything optional).
	//
	// The existing name is read into the INSERT's value slot rather than left
	// nil: NOT NULL is checked on the proposed row before ON CONFLICT can
	// redirect to the UPDATE, so a nil here fails with 23502 even though the
	// update path would never have written it.
	name := req.BusinessName
	if name == nil || *name == "" {
		var existing *string
		err := h.db.QueryRow(r.Context(),
			`SELECT business_name FROM vendors WHERE user_id = $1`, userID).Scan(&existing)
		if errors.Is(err, pgx.ErrNoRows) || existing == nil {
			response.Error(w, http.StatusBadRequest,
				"Business name is required", "VALIDATION_ERROR")
			return
		}
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		name = existing
	}
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO vendors (user_id, business_name, business_address, business_description,
		    business_type, business_type_other, business_logo_url, cover_image_url,
		    business_phone, business_email, website, upi_id,
		    business_registration_number, support_contact)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 ON CONFLICT (user_id) DO UPDATE SET
		    -- COALESCE, not excluded.*: this is a partial update, so a field the
		    -- client did not send keeps the value it already had. Assigning
		    -- excluded.* wiped address, phone and website whenever a vendor
		    -- edited only their business name.
		    business_name = COALESCE(excluded.business_name, vendors.business_name),
		    business_address = COALESCE(excluded.business_address, vendors.business_address),
		    business_description = COALESCE(excluded.business_description, vendors.business_description),
		    business_type = COALESCE(excluded.business_type, vendors.business_type),
		    business_type_other = COALESCE(excluded.business_type_other, vendors.business_type_other),
		    business_logo_url = COALESCE(excluded.business_logo_url, vendors.business_logo_url),
		    cover_image_url = COALESCE(excluded.cover_image_url, vendors.cover_image_url),
		    business_phone = COALESCE(excluded.business_phone, vendors.business_phone),
		    business_email = COALESCE(excluded.business_email, vendors.business_email),
		    website = COALESCE(excluded.website, vendors.website),
		    upi_id = COALESCE(excluded.upi_id, vendors.upi_id),
		    business_registration_number = COALESCE(excluded.business_registration_number,
		        vendors.business_registration_number),
		    support_contact = COALESCE(excluded.support_contact, vendors.support_contact),
		    updated_at = CURRENT_TIMESTAMP`,
		userID, name, req.BusinessAddress, req.BusinessDescription,
		req.BusinessType, req.BusinessTypeOther, req.BusinessLogoURL, req.CoverImageURL,
		req.BusinessPhone, req.BusinessEmail, req.Website, req.UpiID,
		req.BusinessRegistrationNumber, req.SupportContact); err != nil {
		httpx.Fail(w, err)
		return
	}
	v, err := h.load(r, userID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Vendor saved successfully", v)
}

type kycReq struct {
	DocumentURL string `json:"documentUrl"`
	// Java's SubmitVendorKycRequest. Both optional: KYC can be submitted in
	// stages, and a null value means "not submitted yet" (a blank one is an
	// error, which is why the patterns run on non-empty values only).
	GstNumber *string `json:"gstNumber"`
	PanNumber *string `json:"panNumber"`
	// KYC-filed business identity, which may differ from the display name.
	BusinessName    *string `json:"businessName"`
	BusinessAddress *string `json:"businessAddress"`
}

// Java validates these with @Pattern; same expressions.
var (
	gstRe = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$`)
	panRe = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]{1}$`)
)

func (h *Handler) updateKyc(w http.ResponseWriter, r *http.Request) {
	var req kycReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.DocumentURL == "" {
		response.Error(w, http.StatusBadRequest, "documentUrl is required", "VALIDATION_ERROR")
		return
	}
	var e validate.Errors
	if req.GstNumber != nil && !gstRe.MatchString(*req.GstNumber) {
		e = append(e, "Invalid GST number")
	}
	if req.PanNumber != nil && !panRe.MatchString(*req.PanNumber) {
		e = append(e, "Invalid PAN number")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	// Submitting a document moves KYC to SUBMITTED for admin review. A vendor can
	// never set APPROVED themselves - only the admin KYC endpoints do that.
	tag, err := h.db.Exec(r.Context(),
		`UPDATE vendors SET kyc_document_url = $2, kyc_status = 'SUBMITTED',
		    kyc_gst_number = COALESCE($3, kyc_gst_number),
		    kyc_pan_number = COALESCE($4, kyc_pan_number),
		    kyc_business_name = COALESCE($5, kyc_business_name, business_name),
		    kyc_business_address = COALESCE($6, kyc_business_address, business_address),
		    kyc_rejection_reason = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE user_id = $1 AND is_deleted = FALSE`, userID, req.DocumentURL,
		req.GstNumber, req.PanNumber, req.BusinessName, req.BusinessAddress)
	if err != nil {
		// The unique indexes on GST and PAN are what actually prevent two
		// vendors claiming one registration; this turns the breach into the
		// answer Java gives rather than a 500.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch {
			case strings.Contains(pgErr.ConstraintName, "gst"):
				response.Error(w, http.StatusConflict,
					"GST Number already registered", "DUPLICATE_GST")
			case strings.Contains(pgErr.ConstraintName, "pan"):
				response.Error(w, http.StatusConflict,
					"PAN Number already registered", "DUPLICATE_PAN")
			default:
				response.Error(w, http.StatusConflict,
					"Those KYC details are already registered", "DUPLICATE_VALUE")
			}
			return
		}
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Vendor profile not found", "VENDOR_NOT_FOUND")
		return
	}
	response.OK(w, "KYC document submitted for review", nil)
}

type bankReq struct {
	AccountNumber string `json:"accountNumber"`
	IFSC          string `json:"ifsc"`
	HolderName    string `json:"holderName"`
}

func (h *Handler) updateBank(w http.ResponseWriter, r *http.Request) {
	var req bankReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Account number", req.AccountNumber)
	e.Required("IFSC", req.IFSC)
	e.Required("Holder name", req.HolderName)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	userID, _ := middleware.UserID(r.Context())
	// Only the masked form is stored. Keeping a full account number would be
	// storing payout credentials this service never needs to read back.
	tag, err := h.db.Exec(r.Context(),
		`UPDATE vendors SET bank_account_masked = $2, bank_ifsc = $3, bank_holder_name = $4,
		    updated_at = CURRENT_TIMESTAMP
		 WHERE user_id = $1 AND is_deleted = FALSE`,
		userID, mask(req.AccountNumber), req.IFSC, req.HolderName)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Vendor profile not found", "VENDOR_NOT_FOUND")
		return
	}
	response.OK(w, "Bank account saved successfully", nil)
}

func (h *Handler) properties(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)
	userID, _ := middleware.UserID(r.Context())
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, type, status, COALESCE(city,'') FROM facilities
		 WHERE owner_id = $1 AND is_deleted = FALSE
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, size, page*size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Type   string `json:"type"`
		Status string `json:"status"`
		City   string `json:"city"`
	}
	out := []row{}
	for rows.Next() {
		var f row
		if err := rows.Scan(&f.ID, &f.Name, &f.Type, &f.Status, &f.City); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, f)
	}
	response.OK(w, "Properties retrieved successfully", httpx.NewPaged(out, page, size, int64(len(out))))
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	// The first four are Java's VendorDashboardResponse; the rest are extra
	// metrics this dashboard already returned.
	var d struct {
		TotalHalls    int64  `json:"totalHalls"`
		TotalHotels   int64  `json:"totalHotels"`
		KycStatus     string `json:"kycStatus"`
		AccountStatus string `json:"accountStatus"`

		TotalProperties   int64   `json:"totalProperties"`
		TotalBookings     int64   `json:"totalBookings"`
		ConfirmedBookings int64   `json:"confirmedBookings"`
		Revenue           float64 `json:"revenue"`
		PendingQuotes     int64   `json:"pendingQuotes"`
	}
	err := h.db.QueryRow(r.Context(), `
		SELECT (SELECT count(*) FROM facilities
		          WHERE owner_id = $1 AND type = 'MARRIAGE_HALL' AND is_deleted = FALSE),
		       (SELECT count(*) FROM facilities
		          WHERE owner_id = $1 AND type = 'HOTEL' AND is_deleted = FALSE),
		       (SELECT COALESCE(kyc_status,'NONE') FROM vendors WHERE user_id = $1),
		       (SELECT COALESCE(status,'PENDING') FROM vendors WHERE user_id = $1),
		       (SELECT count(*) FROM facilities WHERE owner_id = $1 AND is_deleted = FALSE),
		       (SELECT count(*) FROM bookings b JOIN facilities f ON f.id = b.target_id
		          WHERE f.owner_id = $1 AND b.is_deleted = FALSE),
		       (SELECT count(*) FROM bookings b JOIN facilities f ON f.id = b.target_id
		          WHERE f.owner_id = $1 AND b.status = 'CONFIRMED'),
		       (SELECT COALESCE(sum(p.amount),0) FROM payments p
		          JOIN bookings b ON b.id = p.booking_id
		          JOIN facilities f ON f.id = b.target_id
		          WHERE f.owner_id = $1 AND p.status = 'SUCCESS'),
		       (SELECT count(*) FROM quotes q JOIN facilities f ON f.id = q.facility_id
		          WHERE f.owner_id = $1 AND q.status IN ('REQUESTED','COUNTERED'))`,
		userID).Scan(&d.TotalHalls, &d.TotalHotels, &d.KycStatus, &d.AccountStatus,
		&d.TotalProperties, &d.TotalBookings, &d.ConfirmedBookings,
		&d.Revenue, &d.PendingQuotes)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Dashboard retrieved successfully", d)
}

// mask keeps only the last four digits, the most that should ever be displayed.
func mask(account string) string {
	account = strings.TrimSpace(account)
	if len(account) <= 4 {
		return strings.Repeat("*", len(account))
	}
	return strings.Repeat("*", len(account)-4) + account[len(account)-4:]
}
