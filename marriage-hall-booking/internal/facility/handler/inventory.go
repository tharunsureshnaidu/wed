package handler

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
	"github.com/tripfcatory/marriage-hall-booking/pkg/validate"
)

// RegisterInventory mounts the sub-resources of a facility: room types, packages,
// add-ons, advance rules, policies and pricing rules.
//
// Reads are public (a customer browsing a hall needs to see its packages and
// prices); every write requires ownership of the parent facility.
func (h *Handler) RegisterInventory(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	owner := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn, auth,
			middleware.RequireRole(domain.RoleHallOwner, domain.RoleAdmin))
	}

	mux.HandleFunc("GET /api/v1/hotels/{id}/room-types", h.listRoomTypes)
	mux.Handle("POST /api/v1/hotels/{id}/room-types", owner(h.createRoomType))
	mux.Handle("DELETE /api/v1/hotels/{id}/room-types/{childId}", owner(h.deleteRoomType))

	mux.HandleFunc("GET /api/v1/halls/{id}/packages", h.listPackages)
	mux.Handle("POST /api/v1/halls/{id}/packages", owner(h.createPackage))
	mux.Handle("DELETE /api/v1/halls/{id}/packages/{childId}", owner(h.deletePackage))

	mux.HandleFunc("GET /api/v1/halls/{id}/addons", h.listAddons)
	mux.Handle("POST /api/v1/halls/{id}/addons", owner(h.createAddon))
	mux.Handle("DELETE /api/v1/halls/{id}/addons/{childId}", owner(h.deleteAddon))

	mux.HandleFunc("GET /api/v1/halls/{id}/advance-rules", h.getAdvanceRule)
	mux.Handle("POST /api/v1/halls/{id}/advance-rules", owner(h.setAdvanceRule))

	mux.HandleFunc("GET /api/v1/facilities/{id}/policies", h.listPolicies)
	mux.Handle("POST /api/v1/facilities/{id}/policies", owner(h.createPolicy))
	mux.Handle("PUT /api/v1/facilities/{id}/policies/{childId}", owner(h.updatePolicy))
	mux.Handle("DELETE /api/v1/facilities/{id}/policies/{childId}", owner(h.deletePolicy))

	mux.HandleFunc("GET /api/v1/facilities/{id}/pricing", h.listPricing)
	mux.Handle("POST /api/v1/facilities/{id}/pricing", owner(h.createPricing))
	mux.Handle("PUT /api/v1/facilities/{id}/pricing/{childId}", owner(h.updatePricing))
	mux.Handle("DELETE /api/v1/facilities/{id}/pricing/{childId}", owner(h.deletePricing))
}

// parent validates the {id} path value without an ownership check, for reads.
func parent(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return "", false
	}
	return id, true
}

// child validates the sub-resource id.
func child(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("childId")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid id", "VALIDATION_ERROR")
		return "", false
	}
	return id, true
}

// deleted soft-deletes a child row, scoped to its parent facility so an owner
// cannot delete a row belonging to someone else's facility by guessing its id.
func (h *Handler) softDeleteChild(w http.ResponseWriter, r *http.Request, table string) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	childID, ok := child(w, r)
	if !ok {
		return
	}
	tag, err := h.repo.Pool().Exec(r.Context(),
		`UPDATE `+table+` SET is_deleted = TRUE, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND facility_id = $2 AND is_deleted = FALSE`, childID, facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Not found", "NOT_FOUND")
		return
	}
	response.OK(w, "Deleted successfully", nil)
}

func (h *Handler) hardDeleteChild(w http.ResponseWriter, r *http.Request, table string) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	childID, ok := child(w, r)
	if !ok {
		return
	}
	tag, err := h.repo.Pool().Exec(r.Context(),
		`DELETE FROM `+table+` WHERE id = $1 AND facility_id = $2`, childID, facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Not found", "NOT_FOUND")
		return
	}
	response.OK(w, "Deleted successfully", nil)
}

// --- room types ---

type roomTypeReq struct {
	Name              string  `json:"name"`
	Description       *string `json:"description"`
	CapacityAdults    int     `json:"capacityAdults"`
	CapacityChildren  int     `json:"capacityChildren"`
	BasePricePerNight float64 `json:"basePricePerNight"`
	TotalRooms        int     `json:"totalRooms"`
}

func (h *Handler) createRoomType(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req roomTypeReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Name", req.Name)
	if req.CapacityAdults <= 0 {
		e = append(e, "capacityAdults must be positive")
	}
	if req.BasePricePerNight < 0 {
		e = append(e, "basePricePerNight cannot be negative")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	// Default to a usable inventory count so a room type is bookable as soon as
	// it is created, rather than silently having zero rooms.
	if req.TotalRooms <= 0 {
		req.TotalRooms = 1
	}

	var id string
	if err := h.repo.Pool().QueryRow(r.Context(),
		`INSERT INTO room_types (facility_id, name, description, capacity_adults,
		    capacity_children, base_price_per_night, total_rooms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		facilityID, req.Name, req.Description, req.CapacityAdults,
		req.CapacityChildren, req.BasePricePerNight, req.TotalRooms).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	// Same shape as the list endpoint: a hand-built map here had dropped
	// description, so POST and GET disagreed about the same resource.
	response.OK(w, "Room type created successfully", map[string]any{
		"id": id, "facilityId": facilityID, "name": req.Name,
		"description":    req.Description,
		"capacityAdults": req.CapacityAdults, "capacityChildren": req.CapacityChildren,
		"basePricePerNight": req.BasePricePerNight, "totalRooms": req.TotalRooms,
	})
}

func (h *Handler) listRoomTypes(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := parent(w, r)
	if !ok {
		return
	}
	rows, err := h.repo.Pool().Query(r.Context(),
		`SELECT id, name, description, capacity_adults, capacity_children,
		        base_price_per_night, total_rooms
		 FROM room_types WHERE facility_id = $1 AND is_deleted = FALSE ORDER BY created_at`,
		facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID                string  `json:"id"`
		Name              string  `json:"name"`
		Description       *string `json:"description"`
		CapacityAdults    int     `json:"capacityAdults"`
		CapacityChildren  int     `json:"capacityChildren"`
		BasePricePerNight float64 `json:"basePricePerNight"`
		TotalRooms        int     `json:"totalRooms"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.Name, &x.Description, &x.CapacityAdults,
			&x.CapacityChildren, &x.BasePricePerNight, &x.TotalRooms); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	response.OK(w, "Room types retrieved successfully", out)
}

func (h *Handler) deleteRoomType(w http.ResponseWriter, r *http.Request) {
	h.softDeleteChild(w, r, "room_types")
}

// --- hall packages ---

type packageReq struct {
	Name             string  `json:"name"`
	Description      *string `json:"description"`
	Price            float64 `json:"price"`
	GuestCapacity    *int    `json:"guestCapacity"`
	IncludesCatering bool    `json:"includesCatering"`
	IncludedServices *string `json:"includedServices"`
	ExcludedServices *string `json:"excludedServices"`
}

func (h *Handler) createPackage(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req packageReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Name", req.Name)
	if req.Price < 0 {
		e = append(e, "price cannot be negative")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	var id string
	if err := h.repo.Pool().QueryRow(r.Context(),
		`INSERT INTO hall_packages (facility_id, name, description, price, guest_capacity,
		    includes_catering, included_services, excluded_services)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		facilityID, req.Name, req.Description, req.Price, req.GuestCapacity,
		req.IncludesCatering, req.IncludedServices, req.ExcludedServices).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Package created successfully", map[string]any{
		"id": id, "facilityId": facilityID, "name": req.Name, "price": req.Price,
		"guestCapacity": req.GuestCapacity, "includesCatering": req.IncludesCatering,
	})
}

func (h *Handler) listPackages(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := parent(w, r)
	if !ok {
		return
	}
	rows, err := h.repo.Pool().Query(r.Context(),
		`SELECT id, name, description, price, guest_capacity, includes_catering,
		        included_services, excluded_services
		 FROM hall_packages WHERE facility_id = $1 AND is_deleted = FALSE ORDER BY created_at`,
		facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID               string  `json:"id"`
		Name             string  `json:"name"`
		Description      *string `json:"description"`
		Price            float64 `json:"price"`
		GuestCapacity    *int    `json:"guestCapacity"`
		IncludesCatering bool    `json:"includesCatering"`
		IncludedServices *string `json:"includedServices"`
		ExcludedServices *string `json:"excludedServices"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.Name, &x.Description, &x.Price, &x.GuestCapacity,
			&x.IncludesCatering, &x.IncludedServices, &x.ExcludedServices); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	response.OK(w, "Packages retrieved successfully", out)
}

func (h *Handler) deletePackage(w http.ResponseWriter, r *http.Request) {
	h.softDeleteChild(w, r, "hall_packages")
}

// --- add-on services ---

type addonReq struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Price       float64 `json:"price"`
	ServiceType *string `json:"serviceType"`
	Unit        *string `json:"unit"`
}

func (h *Handler) createAddon(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req addonReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Name", req.Name)
	if req.Price < 0 {
		e = append(e, "price cannot be negative")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	var id string
	if err := h.repo.Pool().QueryRow(r.Context(),
		`INSERT INTO add_on_services (facility_id, name, description, price, service_type, unit)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		facilityID, req.Name, req.Description, req.Price, req.ServiceType, req.Unit).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Add-on created successfully", map[string]any{
		"id": id, "facilityId": facilityID, "name": req.Name,
		"price": req.Price, "serviceType": req.ServiceType,
	})
}

func (h *Handler) listAddons(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := parent(w, r)
	if !ok {
		return
	}
	rows, err := h.repo.Pool().Query(r.Context(),
		`SELECT id, name, description, price, service_type, unit
		 FROM add_on_services WHERE facility_id = $1 AND is_deleted = FALSE ORDER BY created_at`,
		facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Description *string `json:"description"`
		Price       float64 `json:"price"`
		ServiceType *string `json:"serviceType"`
		Unit        *string `json:"unit"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.Name, &x.Description, &x.Price, &x.ServiceType, &x.Unit); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	response.OK(w, "Add-ons retrieved successfully", out)
}

func (h *Handler) deleteAddon(w http.ResponseWriter, r *http.Request) {
	h.softDeleteChild(w, r, "add_on_services")
}

// --- token advance rules ---

type advanceRuleReq struct {
	AdvancePercentage    float64 `json:"advancePercentage"`
	MinAdvanceAmount     float64 `json:"minAdvanceAmount"`
	BalanceDueDaysBefore int     `json:"balanceDueDaysBefore"`
	AutoReminderEnabled  bool    `json:"autoReminderEnabled"`
}

// setAdvanceRule upserts: a hall has one advance rule, and POSTing again
// replaces it rather than stacking conflicting rules.
func (h *Handler) setAdvanceRule(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req advanceRuleReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	if req.AdvancePercentage <= 0 || req.AdvancePercentage > 100 {
		e = append(e, "advancePercentage must be between 0 and 100")
	}
	if req.MinAdvanceAmount < 0 {
		e = append(e, "minAdvanceAmount cannot be negative")
	}
	if req.BalanceDueDaysBefore < 0 {
		e = append(e, "balanceDueDaysBefore cannot be negative")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	var id string
	if err := h.repo.Pool().QueryRow(r.Context(),
		`INSERT INTO token_advance_rules (facility_id, advance_percentage, min_advance_amount,
		    balance_due_days_before, auto_reminder_enabled, min_percentage)
		 VALUES ($1,$2,$3,$4,$5,$2)
		 ON CONFLICT (facility_id) DO UPDATE SET
		    advance_percentage = excluded.advance_percentage,
		    min_advance_amount = excluded.min_advance_amount,
		    balance_due_days_before = excluded.balance_due_days_before,
		    auto_reminder_enabled = excluded.auto_reminder_enabled,
		    min_percentage = excluded.advance_percentage
		 RETURNING id`,
		facilityID, req.AdvancePercentage, req.MinAdvanceAmount,
		req.BalanceDueDaysBefore, req.AutoReminderEnabled).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Advance rule saved successfully", map[string]any{
		"id": id, "facilityId": facilityID, "advancePercentage": req.AdvancePercentage,
		"minAdvanceAmount":     req.MinAdvanceAmount,
		"balanceDueDaysBefore": req.BalanceDueDaysBefore,
		"autoReminderEnabled":  req.AutoReminderEnabled,
	})
}

func (h *Handler) getAdvanceRule(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := parent(w, r)
	if !ok {
		return
	}
	var out struct {
		ID                   string   `json:"id"`
		FacilityID           string   `json:"facilityId"`
		AdvancePercentage    *float64 `json:"advancePercentage"`
		MinAdvanceAmount     *float64 `json:"minAdvanceAmount"`
		BalanceDueDaysBefore *int     `json:"balanceDueDaysBefore"`
		AutoReminderEnabled  bool     `json:"autoReminderEnabled"`
	}
	err := h.repo.Pool().QueryRow(r.Context(),
		`SELECT id, facility_id, advance_percentage, min_advance_amount,
		        balance_due_days_before, COALESCE(auto_reminder_enabled, FALSE)
		 FROM token_advance_rules WHERE facility_id = $1`, facilityID).
		Scan(&out.ID, &out.FacilityID, &out.AdvancePercentage, &out.MinAdvanceAmount,
			&out.BalanceDueDaysBefore, &out.AutoReminderEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		// No rule configured is a valid state, not an error.
		response.OK(w, "No advance rule configured", nil)
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Advance rule retrieved successfully", out)
}

// --- policies ---

type policyReq struct {
	PolicyType  string `json:"policyType"`
	Description string `json:"description"`
}

func (h *Handler) createPolicy(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req policyReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("policyType", req.PolicyType)
	e.Required("description", req.Description)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	var id string
	if err := h.repo.Pool().QueryRow(r.Context(),
		`INSERT INTO facility_policies (facility_id, policy_type, description)
		 VALUES ($1,$2,$3) RETURNING id`,
		facilityID, req.PolicyType, req.Description).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Policy created successfully", map[string]any{
		"id": id, "facilityId": facilityID,
		"policyType": req.PolicyType, "description": req.Description,
	})
}

func (h *Handler) updatePolicy(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	policyID, ok := child(w, r)
	if !ok {
		return
	}
	var req policyReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("policyType", req.PolicyType)
	e.Required("description", req.Description)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	tag, err := h.repo.Pool().Exec(r.Context(),
		`UPDATE facility_policies SET policy_type = $3, description = $4,
		    updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND facility_id = $2`, policyID, facilityID, req.PolicyType, req.Description)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Policy not found", "NOT_FOUND")
		return
	}
	response.OK(w, "Policy updated successfully", map[string]any{
		"id": policyID, "facilityId": facilityID,
		"policyType": req.PolicyType, "description": req.Description,
	})
}

func (h *Handler) deletePolicy(w http.ResponseWriter, r *http.Request) {
	h.hardDeleteChild(w, r, "facility_policies")
}

func (h *Handler) listPolicies(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := parent(w, r)
	if !ok {
		return
	}
	rows, err := h.repo.Pool().Query(r.Context(),
		`SELECT id, policy_type, description FROM facility_policies
		 WHERE facility_id = $1 ORDER BY created_at`, facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID          string `json:"id"`
		PolicyType  string `json:"policyType"`
		Description string `json:"description"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.PolicyType, &x.Description); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	response.OK(w, "Policies retrieved successfully", out)
}

// --- pricing rules ---

type pricingReq struct {
	EventType string  `json:"eventType"`
	DayType   *string `json:"dayType"`
	Season    *string `json:"season"`
	MinGuests *int    `json:"minGuests"`
	MaxGuests *int    `json:"maxGuests"`
	Price     float64 `json:"price"`
	ValidFrom *string `json:"validFrom"`
	ValidTo   *string `json:"validTo"`
}

func (req pricingReq) validateInto(e *validate.Errors) {
	e.Required("eventType", req.EventType)
	if req.Price < 0 {
		*e = append(*e, "price cannot be negative")
	}
	if req.MinGuests != nil && req.MaxGuests != nil && *req.MinGuests > *req.MaxGuests {
		*e = append(*e, "minGuests cannot exceed maxGuests")
	}
}

func (h *Handler) createPricing(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req pricingReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	req.validateInto(&e)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	var id string
	if err := h.repo.Pool().QueryRow(r.Context(),
		`INSERT INTO facility_pricing_rules (facility_id, event_type, day_type, season,
		    min_guests, max_guests, price, valid_from, valid_to)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8::date,$9::date) RETURNING id`,
		facilityID, req.EventType, req.DayType, req.Season, req.MinGuests,
		req.MaxGuests, req.Price, req.ValidFrom, req.ValidTo).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Pricing rule created successfully", pricingView(id, facilityID, req))
}

func (h *Handler) updatePricing(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	ruleID, ok := child(w, r)
	if !ok {
		return
	}
	var req pricingReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	var e validate.Errors
	req.validateInto(&e)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	tag, err := h.repo.Pool().Exec(r.Context(),
		`UPDATE facility_pricing_rules SET event_type = $3, day_type = $4, season = $5,
		    min_guests = $6, max_guests = $7, price = $8, valid_from = $9::date,
		    valid_to = $10::date, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND facility_id = $2`,
		ruleID, facilityID, req.EventType, req.DayType, req.Season, req.MinGuests,
		req.MaxGuests, req.Price, req.ValidFrom, req.ValidTo)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, "Pricing rule not found", "NOT_FOUND")
		return
	}
	response.OK(w, "Pricing rule updated successfully", pricingView(ruleID, facilityID, req))
}

func (h *Handler) deletePricing(w http.ResponseWriter, r *http.Request) {
	h.hardDeleteChild(w, r, "facility_pricing_rules")
}

func (h *Handler) listPricing(w http.ResponseWriter, r *http.Request) {
	facilityID, ok := parent(w, r)
	if !ok {
		return
	}
	rows, err := h.repo.Pool().Query(r.Context(),
		`SELECT id, event_type, day_type, season, min_guests, max_guests, price,
		        to_char(valid_from,'YYYY-MM-DD'), to_char(valid_to,'YYYY-MM-DD')
		 FROM facility_pricing_rules WHERE facility_id = $1 ORDER BY created_at`, facilityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID        string  `json:"id"`
		EventType string  `json:"eventType"`
		DayType   *string `json:"dayType"`
		Season    *string `json:"season"`
		MinGuests *int    `json:"minGuests"`
		MaxGuests *int    `json:"maxGuests"`
		Price     float64 `json:"price"`
		ValidFrom *string `json:"validFrom"`
		ValidTo   *string `json:"validTo"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.EventType, &x.DayType, &x.Season, &x.MinGuests,
			&x.MaxGuests, &x.Price, &x.ValidFrom, &x.ValidTo); err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, x)
	}
	response.OK(w, "Pricing rules retrieved successfully", out)
}

func pricingView(id, facilityID string, req pricingReq) map[string]any {
	return map[string]any{
		"id": id, "facilityId": facilityID, "eventType": req.EventType,
		"dayType": req.DayType, "season": req.Season, "minGuests": req.MinGuests,
		"maxGuests": req.MaxGuests, "price": req.Price,
		"validFrom": req.ValidFrom, "validTo": req.ValidTo,
	}
}
