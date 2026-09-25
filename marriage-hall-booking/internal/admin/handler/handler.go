package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

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

	// OnStatusChange is called after an admin decision lands, so the affected
	// user can be told. One hook rather than four: KYC decisions, facility
	// approvals and blocks are all "an admin changed a status on something you
	// own", and differ only in the words.
	//
	// A func rather than a dependency on the notification package: this handler
	// must stay usable - and testable - without one.
	OnStatusChange func(ctx context.Context, ev StatusChange)
}

// StatusChange is one admin decision. UserID is who to tell; zero means the
// decision had no identifiable owner and nothing is sent.
type StatusChange struct {
	Entity   string // "vendor.kyc", "facility", "user"
	EntityID string
	UserID   int64
	Status   string // APPROVED, REJECTED, BLOCKED, ACTIVE, ...
	Reason   string
	Name     string // facility or vendor name, for the message
}

// notifyStatus fires the hook if one is wired. Failures are the hook's own
// problem: an admin decision that succeeded must not be reported as failed
// because a notification could not be queued.
func (h *Handler) notifyStatus(ctx context.Context, ev StatusChange) {
	if h.OnStatusChange != nil && ev.UserID != 0 {
		h.OnStatusChange(ctx, ev)
	}
}

func New(db *pgxpool.Pool, signer *jwt.Signer) *Handler {
	return &Handler{db: db, signer: signer}
}

// Register mounts every admin route behind RequireRole(ROLE_ADMIN). Nothing in
// this file is reachable without an admin token.
func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequireAuth(h.signer),
			middleware.RequireRole(domain.RoleAdmin))
	}

	mux.Handle("GET /api/v1/admin/dashboard", admin(h.dashboard))
	mux.Handle("GET /api/v1/admin/users", admin(h.listUsers))
	mux.Handle("POST /api/v1/admin/users", admin(h.createUser))
	mux.Handle("PUT /api/v1/admin/users/{id}", admin(h.updateUser))
	mux.Handle("DELETE /api/v1/admin/users/{id}", admin(h.deleteUser))
	mux.Handle("POST /api/v1/admin/block", admin(h.blockUser))
	mux.Handle("GET /api/v1/admin/vendors", admin(h.listVendors))
	mux.Handle("GET /api/v1/admin/vendors/{vendorId}", admin(h.getVendor))
	mux.Handle("POST /api/v1/admin/vendors/{vendorId}/kyc/approve", admin(h.approveKyc))
	mux.Handle("POST /api/v1/admin/vendors/{vendorId}/kyc/reject", admin(h.rejectKyc))
	mux.Handle("GET /api/v1/admin/facilities", admin(h.listFacilities))
	mux.Handle("POST /api/v1/admin/facilities/{id}/approve", admin(h.approveFacility))
	mux.Handle("POST /api/v1/admin/fraud-reports", admin(h.createFraudReport))
	mux.Handle("POST /api/v1/admin/fraud-reports/{reportId}/resolve", admin(h.resolveFraudReport))
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	// totalUsers/totalHotels/totalMarriageHalls/totalBookings/totalRevenue are
	// Java's DashboardSummaryResponse; the rest are extra admin metrics that
	// already shipped.
	var d struct {
		TotalUsers         int64   `json:"totalUsers"`
		TotalHotels        int64   `json:"totalHotels"`
		TotalMarriageHalls int64   `json:"totalMarriageHalls"`
		TotalBookings      int64   `json:"totalBookings"`
		TotalRevenue       float64 `json:"totalRevenue"`

		TotalVendors      int64   `json:"totalVendors"`
		TotalFacilities   int64   `json:"totalFacilities"`
		ConfirmedBookings int64   `json:"confirmedBookings"`
		GrossRevenue      float64 `json:"grossRevenue"`
		PendingKyc        int64   `json:"pendingKyc"`
		OpenFraudReports  int64   `json:"openFraudReports"`
	}
	err := h.db.QueryRow(r.Context(), `
		SELECT (SELECT count(*) FROM users WHERE is_deleted = FALSE),
		       (SELECT count(*) FROM facilities WHERE type = 'HOTEL' AND is_deleted = FALSE),
		       (SELECT count(*) FROM facilities WHERE type = 'MARRIAGE_HALL' AND is_deleted = FALSE),
		       (SELECT count(*) FROM vendors WHERE is_deleted = FALSE),
		       (SELECT count(*) FROM facilities WHERE is_deleted = FALSE),
		       (SELECT count(*) FROM bookings WHERE is_deleted = FALSE),
		       (SELECT count(*) FROM bookings WHERE status = 'CONFIRMED'),
		       (SELECT COALESCE(sum(amount),0) FROM payments WHERE status = 'SUCCESS'),
		       (SELECT count(*) FROM vendors WHERE kyc_status IN ('PENDING','SUBMITTED')),
		       (SELECT count(*) FROM fraud_reports WHERE status = 'OPEN')`).
		Scan(&d.TotalUsers, &d.TotalHotels, &d.TotalMarriageHalls, &d.TotalVendors,
			&d.TotalFacilities, &d.TotalBookings,
			&d.ConfirmedBookings, &d.GrossRevenue, &d.PendingKyc, &d.OpenFraudReports)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	d.TotalRevenue = d.GrossRevenue
	response.OK(w, "Dashboard retrieved successfully", d)
}

type adminUser struct {
	ID          int64     `json:"id"`
	FullName    string    `json:"fullName"`
	Email       *string   `json:"email"`
	PhoneNumber *string   `json:"phoneNumber"`
	Status      string    `json:"status"`
	IsDeleted   bool      `json:"isDeleted"`
	CreatedAt   time.Time `json:"createdAt"`
	Roles       []string  `json:"roles"`
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)
	search := r.URL.Query().Get("search")

	var total int64
	if err := h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM users
		 WHERE ($1 = '' OR full_name ILIKE '%'||$1||'%' OR email ILIKE '%'||$1||'%'
		        OR phone_number ILIKE '%'||$1||'%')`, search).Scan(&total); err != nil {
		httpx.Fail(w, err)
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT u.id, u.full_name, u.email, u.phone_number, u.status, u.is_deleted, u.created_at,
		        COALESCE(array_agg(r.role_name) FILTER (WHERE r.role_name IS NOT NULL), '{}')
		 FROM users u
		 LEFT JOIN user_roles ur ON ur.user_id = u.id
		 LEFT JOIN roles r ON r.id = ur.role_id
		 WHERE ($1 = '' OR u.full_name ILIKE '%'||$1||'%' OR u.email ILIKE '%'||$1||'%'
		        OR u.phone_number ILIKE '%'||$1||'%')
		 GROUP BY u.id ORDER BY u.id LIMIT $2 OFFSET $3`, search, size, page*size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	out := []adminUser{}
	for rows.Next() {
		var u adminUser
		if err := rows.Scan(&u.ID, &u.FullName, &u.Email, &u.PhoneNumber, &u.Status,
			&u.IsDeleted, &u.CreatedAt, &u.Roles); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, u)
	}
	response.OK(w, "Users retrieved successfully", httpx.NewPaged(out, page, size, total))
}

type createUserReq struct {
	FullName    string  `json:"fullName"`
	Email       *string `json:"email"`
	PhoneNumber *string `json:"phoneNumber"`
	Password    string  `json:"password"`
	Role        string  `json:"role"`
}

// createUser provisions an account directly, already verified - an admin
// creating a staff or owner account should not have to go through OTP.
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Full name", req.FullName)
	e.Email("email", req.Email)
	e.Phone("phoneNumber", req.PhoneNumber)
	e.Required("Password", req.Password)
	if req.Password != "" {
		e.Password("Password", req.Password)
	}
	if (req.Email == nil || *req.Email == "") && (req.PhoneNumber == nil || *req.PhoneNumber == "") {
		e = append(e, "Either email or phone number is required")
	}
	switch req.Role {
	case domain.RoleCustomer, domain.RoleHallOwner, domain.RoleAdmin, domain.RoleStaff:
	case "":
		req.Role = domain.RoleCustomer
	default:
		e = append(e, "Invalid role")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
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

	var id int64
	err = tx.QueryRow(r.Context(),
		`INSERT INTO users (full_name, email, phone_number, password_hash, status,
		    is_email_verified, is_phone_verified)
		 VALUES ($1,$2,$3,$4,'ACTIVE',$5,$6) RETURNING id`,
		req.FullName, req.Email, req.PhoneNumber, string(hash),
		req.Email != nil, req.PhoneNumber != nil).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			response.Error(w, http.StatusConflict,
				"An account with that email or phone already exists", "USER_EXISTS")
			return
		}
		httpx.Fail(w, err)
		return
	}
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO user_roles (user_id, role_id) SELECT $1, id FROM roles WHERE role_name = $2`,
		id, req.Role); err != nil {
		httpx.Fail(w, err)
		return
	}
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO user_profiles (id, first_name) VALUES ($1, $2)
		 ON CONFLICT (id) DO NOTHING`, id, firstWord(req.FullName)); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}

	response.OK(w, "User created successfully", map[string]any{
		"id": id, "fullName": req.FullName, "email": req.Email,
		"phoneNumber": req.PhoneNumber, "status": "ACTIVE", "roles": []string{req.Role},
	})
}

func firstWord(s string) string {
	if i := strings.IndexByte(strings.TrimSpace(s), ' '); i > 0 {
		return strings.TrimSpace(s)[:i]
	}
	return strings.TrimSpace(s)
}

// getVendor returns one vendor with its owner's account details, for KYC review.
func (h *Handler) getVendor(w http.ResponseWriter, r *http.Request) {
	vendorID := r.PathValue("vendorId")
	if !httpx.ValidUUID(vendorID) {
		response.Error(w, http.StatusBadRequest, "Invalid vendor id", "VALIDATION_ERROR")
		return
	}
	var v struct {
		ID                  string  `json:"id"`
		UserID              int64   `json:"userId"`
		BusinessName        string  `json:"businessName"`
		BusinessAddress     *string `json:"businessAddress"`
		BusinessDescription *string `json:"businessDescription"`
		BusinessType        *string `json:"businessType"`
		KycStatus           string  `json:"kycStatus"`
		KycDocumentURL      *string `json:"kycDocumentUrl"`
		KycRejectionReason  *string `json:"kycRejectionReason"`
		BankAccountMasked   *string `json:"bankAccountMasked"`
		BankIFSC            *string `json:"bankIfsc"`
		OwnerName           string  `json:"ownerName"`
		OwnerEmail          *string `json:"ownerEmail"`
		OwnerPhone          *string `json:"ownerPhone"`
		FacilityCount       int64   `json:"facilityCount"`
	}
	err := h.db.QueryRow(r.Context(),
		`SELECT v.id, v.user_id, v.business_name, v.business_address, v.business_description,
		        v.business_type, v.kyc_status, v.kyc_document_url, v.kyc_rejection_reason,
		        v.bank_account_masked, v.bank_ifsc, u.full_name, u.email, u.phone_number,
		        (SELECT count(*) FROM facilities WHERE owner_id = v.user_id AND is_deleted = FALSE)
		 FROM vendors v JOIN users u ON u.id = v.user_id
		 WHERE v.id = $1 AND v.is_deleted = FALSE`, vendorID).
		Scan(&v.ID, &v.UserID, &v.BusinessName, &v.BusinessAddress, &v.BusinessDescription,
			&v.BusinessType, &v.KycStatus, &v.KycDocumentURL, &v.KycRejectionReason,
			&v.BankAccountMasked, &v.BankIFSC, &v.OwnerName, &v.OwnerEmail,
			&v.OwnerPhone, &v.FacilityCount)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Vendor not found", "VENDOR_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Vendor retrieved successfully", v)
}

type updateUserReq struct {
	FullName *string `json:"fullName"`
	Status   *string `json:"status"`
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Invalid user id", "VALIDATION_ERROR")
		return
	}
	var req updateUserReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Status != nil {
		switch *req.Status {
		case "ACTIVE", "INACTIVE", "SUSPENDED", "PENDING_VERIFICATION":
		default:
			response.Error(w, http.StatusBadRequest, "Invalid status", "VALIDATION_ERROR")
			return
		}
	}
	tag, err := h.db.Exec(r.Context(),
		`UPDATE users SET full_name = COALESCE($2, full_name),
		    status = COALESCE($3, status), updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1`, id, req.FullName, req.Status)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "User not found", "USER_NOT_FOUND")
		return
	}
	response.OK(w, "User updated successfully", nil)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Invalid user id", "VALIDATION_ERROR")
		return
	}
	// An admin must not be able to delete their own account out from under themselves.
	if actor, _ := middleware.UserID(r.Context()); actor == id {
		response.Error(w, http.StatusBadRequest, "You cannot delete your own account here",
			"SELF_DELETE_FORBIDDEN")
		return
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(),
		`UPDATE users SET is_deleted = TRUE, status = 'INACTIVE' WHERE id = $1`, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE refresh_tokens SET revoked = TRUE WHERE user_id = $1`, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "User deleted successfully", nil)
}

// blockReq mirrors Java's BlockRequest: {id, type} where type is "user",
// "HOTEL" or "MARRIAGE_HALL". userId is kept because that is what this endpoint
// accepted before, and existing callers send it.
type blockReq struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	UserID int64  `json:"userId"`
	Reason string `json:"reason"`
}

func (h *Handler) blockUser(w http.ResponseWriter, r *http.Request) {
	var req blockReq
	if !httpx.Decode(w, r, &req) {
		return
	}

	// A facility target: block and unblock the listing, as Java's toggleBlock
	// does. Without this branch an admin could only ever block, never reverse
	// it - the endpoint had no way back.
	if t := strings.ToUpper(req.Type); t == "HOTEL" || t == "MARRIAGE_HALL" {
		if !httpx.ValidUUID(req.ID) {
			response.Error(w, http.StatusBadRequest,
				"id must be a facility id for this type", "VALIDATION_ERROR")
			return
		}
		var status string
		if err := h.db.QueryRow(r.Context(),
			`UPDATE facilities
			    SET status = CASE WHEN status = 'BLOCKED' THEN 'APPROVED' ELSE 'BLOCKED' END,
			        updated_at = CURRENT_TIMESTAMP
			  WHERE id = $1 AND is_deleted = FALSE
			  RETURNING status`, req.ID).Scan(&status); err != nil {
			response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
			return
		}
		if status == "BLOCKED" {
			response.OK(w, "Facility blocked successfully", nil)
		} else {
			response.OK(w, "Facility unblocked successfully", nil)
		}
		return
	}
	if req.Type != "" && !strings.EqualFold(req.Type, "user") {
		response.Error(w, http.StatusBadRequest,
			"type must be one of: user, HOTEL, MARRIAGE_HALL", "INVALID_TYPE")
		return
	}

	userID := req.UserID
	if userID == 0 && req.ID != "" {
		parsed, err := strconv.ParseInt(req.ID, 10, 64)
		if err != nil {
			response.Error(w, http.StatusBadRequest, "id must be a user id", "INVALID_ID")
			return
		}
		userID = parsed
	}
	if userID == 0 {
		response.Error(w, http.StatusBadRequest, "userId is required", "VALIDATION_ERROR")
		return
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	// Toggle in one statement: reading the status and then writing it would let
	// two admins both read ACTIVE and both "block", the second silently undoing
	// the first.
	var status string
	if err := tx.QueryRow(r.Context(),
		`UPDATE users
		    SET status = CASE WHEN status = 'SUSPENDED' THEN 'ACTIVE' ELSE 'SUSPENDED' END,
		        updated_at = CURRENT_TIMESTAMP
		  WHERE id = $1 AND is_deleted = FALSE
		  RETURNING status`, userID).Scan(&status); err != nil {
		response.Error(w, http.StatusNotFound, "User not found", "USER_NOT_FOUND")
		return
	}

	action, msg := "UNBLOCK_USER", "User unblocked successfully"
	if status == "SUSPENDED" {
		action, msg = "BLOCK_USER", "User blocked successfully"
		// Suspension must end current sessions, or the user keeps their access
		// token until it expires.
		if _, err := tx.Exec(r.Context(),
			`UPDATE refresh_tokens SET revoked = TRUE WHERE user_id = $1`, userID); err != nil {
			httpx.Fail(w, err)
			return
		}
	}

	actor, _ := middleware.UserID(r.Context())
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO audit_logs (user_id, action, entity_name, entity_id, new_values, ip_address)
		 VALUES ($1, $2, 'users', $3, $4, $5)`,
		actor, action, strconv.FormatInt(userID, 10),
		`{"status":`+strconv.Quote(status)+`,"reason":`+strconv.Quote(req.Reason)+`}`,
		httpx.IP(r)); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	// Blocking revokes the access tokens too, not just the refresh token.
	if status == "SUSPENDED" {
		middleware.RevokeAccessTokens(r.Context(), userID)
	}
	// After the commit, so a user is never told about a block that rolled back.
	h.notifyStatus(r.Context(), StatusChange{
		Entity: "user", EntityID: strconv.FormatInt(userID, 10), UserID: userID,
		Status: status, Reason: req.Reason,
	})
	response.OK(w, msg, nil)
}

func (h *Handler) listVendors(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)
	status := r.URL.Query().Get("status")

	var total int64
	if err := h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM vendors WHERE is_deleted = FALSE
		   AND ($1 = '' OR kyc_status = $1)`, status).Scan(&total); err != nil {
		httpx.Fail(w, err)
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT v.id, v.user_id, v.business_name, v.kyc_status, u.email
		 FROM vendors v JOIN users u ON u.id = v.user_id
		 WHERE v.is_deleted = FALSE AND ($1 = '' OR v.kyc_status = $1)
		 ORDER BY v.created_at DESC LIMIT $2 OFFSET $3`, status, size, page*size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	type vendorRow struct {
		ID           string  `json:"id"`
		UserID       int64   `json:"userId"`
		BusinessName string  `json:"businessName"`
		KycStatus    string  `json:"kycStatus"`
		Email        *string `json:"email"`
	}
	out := []vendorRow{}
	for rows.Next() {
		var v vendorRow
		if err := rows.Scan(&v.ID, &v.UserID, &v.BusinessName, &v.KycStatus, &v.Email); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, v)
	}
	response.OK(w, "Vendors retrieved successfully", httpx.NewPaged(out, page, size, total))
}

func (h *Handler) approveKyc(w http.ResponseWriter, r *http.Request) {
	h.setKyc(w, r, "APPROVED", "")
}

func (h *Handler) rejectKyc(w http.ResponseWriter, r *http.Request) {
	h.setKyc(w, r, "REJECTED", r.URL.Query().Get("reason"))
}

func (h *Handler) setKyc(w http.ResponseWriter, r *http.Request, status, reason string) {
	vendorID := r.PathValue("vendorId")
	if !httpx.ValidUUID(vendorID) {
		response.Error(w, http.StatusBadRequest, "Invalid vendor id", "VALIDATION_ERROR")
		return
	}
	// Only a submitted application can be decided. Approving one that was never
	// submitted marks a vendor verified on the strength of nothing, and the
	// vendor sees APPROVED without having sent any document.
	//
	// The guard is in the WHERE clause rather than a separate read, so two
	// admins clicking at once cannot both pass a check and then both write.
	// RETURNING the owning user so the decision can be told to them: without
	// it the vendor learns their KYC outcome only by looking.
	var ownerUserID int64
	var vendorName string
	err := h.db.QueryRow(r.Context(),
		`UPDATE vendors SET kyc_status = $2, kyc_rejection_reason = NULLIF($3,''),
		    updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE
		   AND kyc_status IN ('SUBMITTED','PENDING_REVIEW')
		 RETURNING user_id, COALESCE(business_name,'your account')`,
		vendorID, status, reason).Scan(&ownerUserID, &vendorName)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		httpx.Fail(w, err)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Distinguish "no such vendor" from "nothing to decide on": an admin
		// told only "not found" would go looking for the wrong problem.
		var current string
		err := h.db.QueryRow(r.Context(),
			`SELECT COALESCE(kyc_status,'NONE') FROM vendors
			  WHERE id = $1 AND is_deleted = FALSE`, vendorID).Scan(&current)
		if err != nil {
			response.Error(w, http.StatusNotFound, "Vendor not found", "VENDOR_NOT_FOUND")
			return
		}
		response.Error(w, http.StatusBadRequest,
			"KYC has not been submitted for review (current status: "+current+")",
			"KYC_NOT_SUBMITTED")
		return
	}
	h.notifyStatus(r.Context(), StatusChange{
		Entity: "vendor.kyc", EntityID: vendorID, UserID: ownerUserID,
		Status: status, Reason: reason, Name: vendorName,
	})
	response.OK(w, "KYC "+status, nil)
}

func (h *Handler) listFacilities(w http.ResponseWriter, r *http.Request) {
	page, size := httpx.Page(r)
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, type, status, owner_id, COALESCE(city,'')
		 FROM facilities WHERE is_deleted = FALSE
		   AND ($1 = '' OR type = $1)
		   AND ($2 = '' OR name ILIKE '%'||$2||'%')
		 ORDER BY created_at DESC LIMIT $3 OFFSET $4`,
		r.URL.Query().Get("type"), r.URL.Query().Get("search"), size, page*size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	type row struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Type    string `json:"type"`
		Status  string `json:"status"`
		OwnerID int64  `json:"ownerId"`
		City    string `json:"city"`
	}
	out := []row{}
	for rows.Next() {
		var f row
		if err := rows.Scan(&f.ID, &f.Name, &f.Type, &f.Status, &f.OwnerID, &f.City); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, f)
	}
	response.OK(w, "Facilities retrieved successfully", httpx.NewPaged(out, page, size, int64(len(out))))
}

func (h *Handler) approveFacility(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "APPROVED"
	}
	switch status {
	case "APPROVED", "REJECTED", "BLOCKED", "PENDING":
	default:
		response.Error(w, http.StatusBadRequest, "Invalid status", "VALIDATION_ERROR")
		return
	}
	// RETURNING the owner so they learn the outcome. A listing sitting at
	// PENDING is the vendor's whole business waiting on this decision.
	var ownerID int64
	var name string
	err := h.db.QueryRow(r.Context(),
		`UPDATE facilities SET status = $2, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND is_deleted = FALSE
		 RETURNING owner_id, name`, id, status).Scan(&ownerID, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.notifyStatus(r.Context(), StatusChange{
		Entity: "facility", EntityID: id, UserID: ownerID,
		Status: status, Name: name,
	})
	response.OK(w, "Facility "+status, nil)
}

func (h *Handler) createFraudReport(w http.ResponseWriter, r *http.Request) {
	targetType := r.URL.Query().Get("targetType")
	targetID := r.URL.Query().Get("targetId")
	reason := r.URL.Query().Get("reason")

	var e validate.Errors
	switch targetType {
	case "VENDOR", "USER", "FACILITY":
	default:
		e = append(e, "targetType must be VENDOR, USER or FACILITY")
	}
	e.Required("targetId", targetID)
	e.Required("reason", reason)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	actor, _ := middleware.UserID(r.Context())
	var id string
	if err := h.db.QueryRow(r.Context(),
		`INSERT INTO fraud_reports (target_type, target_id, reason, reported_by)
		 VALUES ($1,$2,$3,$4) RETURNING id`,
		targetType, targetID, reason, actor).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Fraud report created", map[string]string{"id": id})
}

func (h *Handler) resolveFraudReport(w http.ResponseWriter, r *http.Request) {
	reportID := r.PathValue("reportId")
	if !httpx.ValidUUID(reportID) {
		response.Error(w, http.StatusBadRequest, "Invalid report id", "VALIDATION_ERROR")
		return
	}
	status := r.URL.Query().Get("status")
	switch status {
	case "RESOLVED_CLEARED", "RESOLVED_ACTIONED":
	default:
		response.Error(w, http.StatusBadRequest,
			"status must be RESOLVED_CLEARED or RESOLVED_ACTIONED", "VALIDATION_ERROR")
		return
	}
	tag, err := h.db.Exec(r.Context(),
		`UPDATE fraud_reports SET status = $2, resolution_note = $3,
		    updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		reportID, status, r.URL.Query().Get("note"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Report not found", "REPORT_NOT_FOUND")
		return
	}
	response.OK(w, "Fraud report resolved", nil)
}
