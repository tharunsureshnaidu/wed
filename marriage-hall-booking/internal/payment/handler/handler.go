package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/payment/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

type Handler struct {
	svc    *service.Service
	signer *jwt.Signer
}

func New(svc *service.Service, signer *jwt.Signer) *Handler {
	return &Handler{svc: svc, signer: signer}
}

func (h *Handler) Register(mux *http.ServeMux) {
	auth := middleware.RequireAuth(h.signer)
	mux.Handle("POST /api/v1/payments/create", auth(http.HandlerFunc(h.create)))

	// The webhook is called by the gateway, not a logged-in user, so it is
	// authenticated by HMAC signature instead of a JWT.
	mux.HandleFunc("POST /api/v1/payments/webhook", h.webhook)

	mux.Handle("POST /api/v1/refunds/{paymentId}", middleware.Chain(
		http.HandlerFunc(h.refund), auth,
		middleware.RequireRole(domain.RoleAdmin, domain.RoleHallOwner)))

	mux.Handle("GET /api/v1/payments", auth(http.HandlerFunc(h.list)))
	mux.Handle("GET /api/v1/payments/{paymentId}", auth(http.HandlerFunc(h.get)))
	mux.Handle("POST /api/v1/payments/{paymentId}/sync", auth(http.HandlerFunc(h.sync)))
}

type createReq struct {
	BookingID string `json:"bookingId"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	if !httpx.ValidUUID(req.BookingID) {
		response.Error(w, http.StatusBadRequest, "A valid bookingId is required", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	p, err := h.svc.Create(r.Context(), userID, req.BookingID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Payment created successfully", p)
}

func normalizeWebhookPayload(body []byte) (service.WebhookPayload, error) {
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return service.WebhookPayload{}, err
	}

	var p service.WebhookPayload
	if err := json.Unmarshal(body, &p); err == nil && p.OrderID != "" {
		return p, nil
	}

	data, _ := envelope["data"].(map[string]any)
	order, _ := data["order"].(map[string]any)
	payment, _ := data["payment"].(map[string]any)
	if order == nil {
		order = map[string]any{}
	}
	if payment == nil {
		payment = map[string]any{}
	}

	p.OrderID, _ = order["order_id"].(string)
	p.PaymentID, _ = payment["cf_payment_id"].(string)
	if p.PaymentID == "" {
		p.PaymentID, _ = payment["payment_id"].(string)
	}
	p.Status = ""
	status, _ := payment["payment_status"].(string)
	switch status {
	case "SUCCESS":
		p.Status = "SUCCESS"
	case "FAILED", "USER_DROPPED", "CANCELLED", "PENDING":
		if status == "PENDING" {
			p.Status = "PENDING"
		} else {
			p.Status = "FAILED"
		}
	}
	if p.Status == "" {
		eventType, _ := envelope["type"].(string)
		if eventType == "" {
			eventType, _ = envelope["event_type"].(string)
		}
		switch {
		case strings.Contains(eventType, "SUCCESS"):
			p.Status = "SUCCESS"
		case strings.Contains(eventType, "FAILED"):
			p.Status = "FAILED"
		}
	}
	if amount, ok := payment["payment_amount"].(float64); ok {
		p.Amount = amount
	} else if amount, ok := order["order_amount"].(float64); ok {
		p.Amount = amount
	}
	p.EventID, _ = envelope["event_id"].(string)
	if p.EventID == "" {
		p.EventID, _ = envelope["id"].(string)
	}
	if p.EventID == "" {
		p.EventID = p.OrderID + ":" + p.PaymentID
	}
	p.Reason, _ = payment["payment_message"].(string)
	return p, nil
}

func (h *Handler) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Malformed request body", "MALFORMED_JSON")
		return
	}
	signature := r.Header.Get("x-webhook-signature")
	if signature == "" {
		signature = r.Header.Get("X-Signature")
	}
	timestamp := r.Header.Get("x-webhook-timestamp")
	if !h.svc.VerifySignatureWithTimestamp(body, signature, timestamp) {
		response.Error(w, http.StatusUnauthorized, "Invalid signature", "INVALID_SIGNATURE")
		return
	}
	p, err := normalizeWebhookPayload(body)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Malformed request body", "MALFORMED_JSON")
		return
	}
	if p.Status == "PENDING" {
		response.OK(w, "Webhook acknowledged", nil)
		return
	}
	if err := h.svc.HandleWebhook(r.Context(), body, p); err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Webhook processed", nil)
}

func (h *Handler) refund(w http.ResponseWriter, r *http.Request) {
	paymentID := r.PathValue("paymentId")
	if !httpx.ValidUUID(paymentID) {
		response.Error(w, http.StatusBadRequest, "Invalid payment id", "VALIDATION_ERROR")
		return
	}
	amount, err := strconv.ParseFloat(r.URL.Query().Get("amount"), 64)
	if err != nil || amount <= 0 { // NaN/Inf are refused in the service
		response.Error(w, http.StatusBadRequest, "A positive amount is required", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	id, err := h.svc.Refund(r.Context(), paymentID, amount, r.URL.Query().Get("reason"),
		userID, middleware.HasRole(r.Context(), domain.RoleAdmin))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Refund processed successfully", map[string]string{"refundId": id})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	payments, err := h.svc.ListForUser(r.Context(), userID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Payments retrieved successfully", payments)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	paymentID := r.PathValue("paymentId")
	if !httpx.ValidUUID(paymentID) {
		response.Error(w, http.StatusBadRequest, "Invalid payment id", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	isAdmin := middleware.HasRole(r.Context(), domain.RoleAdmin) || middleware.HasRole(r.Context(), domain.RoleSuperAdmin)
	payment, err := h.svc.GetForUser(r.Context(), paymentID, userID, isAdmin)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Payment retrieved successfully", payment)
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	paymentID := r.PathValue("paymentId")
	if !httpx.ValidUUID(paymentID) {
		response.Error(w, http.StatusBadRequest, "Invalid payment id", "VALIDATION_ERROR")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	isAdmin := middleware.HasRole(r.Context(), domain.RoleAdmin) || middleware.HasRole(r.Context(), domain.RoleSuperAdmin)
	payment, err := h.svc.Sync(r.Context(), paymentID, userID, isAdmin)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Payment status synchronized", payment)
}
