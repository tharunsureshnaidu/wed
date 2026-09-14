package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/tripfcatory/marriage-hall-booking/internal/auth/domain"
	"github.com/tripfcatory/marriage-hall-booking/internal/payment/service"
	"github.com/tripfcatory/marriage-hall-booking/pkg/httpx"
	"github.com/tripfcatory/marriage-hall-booking/pkg/jwt"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
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

func (h *Handler) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Malformed request body", "MALFORMED_JSON")
		return
	}
	// Signature is checked against the raw bytes, before parsing - parsing and
	// re-serialising first would verify a different payload than the one sent.
	if !h.svc.VerifySignature(body, r.Header.Get("X-Signature")) {
		response.Error(w, http.StatusUnauthorized, "Invalid signature", "INVALID_SIGNATURE")
		return
	}
	var p service.WebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		response.Error(w, http.StatusBadRequest, "Malformed request body", "MALFORMED_JSON")
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
	if err != nil || amount <= 0 {
		response.Error(w, http.StatusBadRequest, "A positive amount is required", "VALIDATION_ERROR")
		return
	}
	id, err := h.svc.Refund(r.Context(), paymentID, amount, r.URL.Query().Get("reason"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "Refund processed successfully", map[string]string{"refundId": id})
}
