package handler

import (
	"net/http"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/payment/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

func (h *Handler) RegisterPolicyRoutes(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	mux.Handle("GET /api/v1/payment-policies", auth(http.HandlerFunc(h.getPolicy)))
	mux.Handle("PUT /api/v1/payment-policies", middleware.Chain(
		http.HandlerFunc(h.upsertPolicy), auth, middleware.RequireRole(domain.RoleAdmin)))
	mux.Handle("GET /api/v1/facilities/{facilityId}/payment-policy", auth(http.HandlerFunc(h.getFacilityPolicy)))
	mux.Handle("PUT /api/v1/facilities/{facilityId}/payment-policy", middleware.Chain(
		http.HandlerFunc(h.upsertFacilityPolicy), auth, middleware.RequireRole(domain.RoleAdmin, domain.RoleHallOwner)))
}

type policyRequest struct {
	AdvancePercent             float64 `json:"advancePercent"`
	MinAdvanceAmount           float64 `json:"minAdvanceAmount"`
	MaxAdvanceAmount           float64 `json:"maxAdvanceAmount"`
	BalanceDueHoursBeforeEvent int     `json:"balanceDueHoursBeforeEvent"`
	CancellationWindowHours    int     `json:"cancellationWindowHours"`
	CancellationPenaltyPercent float64 `json:"cancellationPenaltyPercent"`
}

func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := h.svc.LoadPolicyForBooking(r.Context(), r.URL.Query().Get("bookingId"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Payment policy retrieved", policy)
}

func (h *Handler) getFacilityPolicy(w http.ResponseWriter, r *http.Request) {
	facilityID := r.PathValue("facilityId")
	if !httpx.ValidUUID(facilityID) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	policy, err := h.svc.LoadPolicyForFacility(r.Context(), facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Facility payment policy retrieved", policy)
}

func (h *Handler) upsertPolicy(w http.ResponseWriter, r *http.Request) {
	var req policyRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	policy := service.Policy{
		AdvancePercent:             req.AdvancePercent,
		MinAdvanceAmount:           req.MinAdvanceAmount,
		MaxAdvanceAmount:           req.MaxAdvanceAmount,
		BalanceDueHoursBeforeEvent: req.BalanceDueHoursBeforeEvent,
		CancellationWindowHours:    req.CancellationWindowHours,
		CancellationPenaltyPercent: req.CancellationPenaltyPercent,
	}.Normalize()
	stored, err := h.svc.UpsertPolicy(r.Context(), "MARKETPLACE", "", policy)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Marketplace payment policy updated", stored)
}

func (h *Handler) upsertFacilityPolicy(w http.ResponseWriter, r *http.Request) {
	facilityID := r.PathValue("facilityId")
	if !httpx.ValidUUID(facilityID) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	if !middleware.HasRole(r.Context(), domain.RoleAdmin) {
		var ownerID int64
		if err := h.svc.Pool().QueryRow(r.Context(), `SELECT owner_id FROM facilities WHERE id = $1 AND is_deleted = FALSE`, facilityID).Scan(&ownerID); err != nil {
			response.Error(w, http.StatusNotFound, "Facility not found", "FACILITY_NOT_FOUND")
			return
		}
		if ownerID != userID {
			response.Error(w, http.StatusForbidden, "You can only manage your own facility policy", "FORBIDDEN")
			return
		}
	}
	var req policyRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	policy := service.Policy{
		AdvancePercent:             req.AdvancePercent,
		MinAdvanceAmount:           req.MinAdvanceAmount,
		MaxAdvanceAmount:           req.MaxAdvanceAmount,
		BalanceDueHoursBeforeEvent: req.BalanceDueHoursBeforeEvent,
		CancellationWindowHours:    req.CancellationWindowHours,
		CancellationPenaltyPercent: req.CancellationPenaltyPercent,
	}.Normalize()
	stored, err := h.svc.UpsertPolicy(r.Context(), "VENUE", facilityID, policy)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Facility payment policy updated", stored)
}
