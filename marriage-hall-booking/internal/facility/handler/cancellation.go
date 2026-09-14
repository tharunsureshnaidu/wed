package handler

import (
	"net/http"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"

	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
	"github.com/tripfcatory/marriage-hall-booking/pkg/validate"
)

// Cancellation tiers: how much is refunded, and how far ahead.
//
// The cancellation_policies table existed and the refund and quote paths read
// it, but nothing ever wrote a row - so every venue behaved as if it had no
// policy at all. These are the missing writes.
//
// Distinct from /policies, which is Java's FacilityPolicy: free text house
// rules ("no fireworks"). This one is structured money.

type cancellationReq struct {
	// PolicyType labels the tier for display ("FLEXIBLE", "MODERATE",
	// "STRICT"). The column is NOT NULL; the refund maths ignores it.
	PolicyType       string   `json:"policyType"`
	DaysBeforeEvent  *int     `json:"daysBeforeEvent"`
	RefundPercentage *float64 `json:"refundPercentage"`
	// Java spells the column days_before_checkin; accept that too.
	DaysBeforeCheckin *int `json:"daysBeforeCheckin"`
}

func (h *Handler) RegisterCancellation(mux *http.ServeMux) {
	owner := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, middleware.RequireAuth(h.signer),
			middleware.RequireRole(domain.RoleHallOwner, domain.RoleAdmin))
	}
	mux.HandleFunc("GET /api/v1/facilities/{id}/cancellation-policies", h.listCancellation)
	mux.Handle("POST /api/v1/facilities/{id}/cancellation-policies", owner(h.createCancellation))
	mux.Handle("DELETE /api/v1/facilities/{id}/cancellation-policies/{childId}", owner(h.deleteCancellation))
}

func (h *Handler) createCancellation(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req cancellationReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	days := req.DaysBeforeEvent
	if days == nil {
		days = req.DaysBeforeCheckin
	}

	var e validate.Errors
	if days == nil || *days < 0 {
		e = append(e, "daysBeforeEvent is required and cannot be negative")
	}
	if req.RefundPercentage == nil || *req.RefundPercentage < 0 || *req.RefundPercentage > 100 {
		e = append(e, "refundPercentage must be between 0 and 100")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	policyType := req.PolicyType
	if policyType == "" {
		policyType = "STANDARD"
	}

	// One tier per (facility, days): re-posting the same cut-off updates its
	// percentage rather than adding a second, contradictory rule.
	var id string
	if err := h.repo.Pool().QueryRow(r.Context(),
		`INSERT INTO cancellation_policies (facility_id, days_before_checkin,
		     refund_percentage, policy_type)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (facility_id, days_before_checkin) DO UPDATE
		    SET refund_percentage = excluded.refund_percentage,
		        policy_type = excluded.policy_type
		 RETURNING id`,
		facilityID, *days, *req.RefundPercentage, policyType).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Cancellation policy saved", map[string]any{
		"id": id, "facilityId": facilityID,
		"daysBeforeEvent": *days, "refundPercentage": *req.RefundPercentage,
		"policyType": policyType,
	})
}

func (h *Handler) listCancellation(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := parent(w, r)
	if !ok {
		return
	}
	rows, err := h.repo.Pool().Query(r.Context(),
		`SELECT id, days_before_checkin, refund_percentage, policy_type FROM cancellation_policies
		  WHERE facility_id = $1 ORDER BY days_before_checkin DESC`, facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID               string  `json:"id"`
		DaysBeforeEvent  int     `json:"daysBeforeEvent"`
		RefundPercentage float64 `json:"refundPercentage"`
		PolicyType       string  `json:"policyType"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.DaysBeforeEvent, &x.RefundPercentage, &x.PolicyType); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	response.OK(w, "Cancellation policies retrieved successfully", out)
}

func (h *Handler) deleteCancellation(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	childID := r.PathValue("childId")
	if !httpx.ValidUUID(childID) {
		response.Error(w, http.StatusBadRequest, "Invalid policy id", "VALIDATION_ERROR")
		return
	}
	tag, err := h.repo.Pool().Exec(r.Context(),
		`DELETE FROM cancellation_policies WHERE id = $1 AND facility_id = $2`,
		childID, facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Policy not found", "POLICY_NOT_FOUND")
		return
	}
	response.OK(w, "Cancellation policy deleted", nil)
}
