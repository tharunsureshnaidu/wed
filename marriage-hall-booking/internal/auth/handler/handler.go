package handler

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/validate"
)

type Handler struct {
	svc    *service.AuthService
	signer *jwt.Signer
}

func New(svc *service.AuthService, signer *jwt.Signer) *Handler {
	return &Handler{svc: svc, signer: signer}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/register/vendor", h.registerVendor)
	mux.HandleFunc("POST /api/v1/auth/register/verify-email", h.verifyOtp)
	mux.HandleFunc("POST /api/v1/auth/register/verify-phone", h.verifyOtp)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/otp/resend", h.resendOtp)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	mux.HandleFunc("POST /api/v1/auth/login/refresh", h.refresh) // alias from the spec
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", h.forgotPassword)
	mux.HandleFunc("POST /api/v1/auth/reset-password", h.resetPassword)

	authed := middleware.RequireAuth(h.signer)
	mux.Handle("POST /api/v1/auth/logout/all-devices", authed(http.HandlerFunc(h.logoutAll)))
	mux.Handle("GET /api/v1/auth/me", authed(http.HandlerFunc(h.me)))
}

// --- request bodies, field names identical to the Java DTOs ---

type registerReq struct {
	FullName    string  `json:"fullName"`
	Email       *string `json:"email"`
	PhoneNumber *string `json:"phoneNumber"`
	Password    string  `json:"password"`
}

type loginReq struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

type verifyOtpReq struct {
	Target  string `json:"target"`
	OtpCode string `json:"otpCode"`
}

type resendOtpReq struct {
	Target  string `json:"target"`
	OtpType string `json:"otpType"`
}

type refreshReq struct {
	RefreshToken string `json:"refreshToken"`
}

type forgotReq struct {
	Identifier string `json:"identifier"`
}

type resetReq struct {
	Identifier  string `json:"identifier"`
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	h.doRegister(w, r, false)
}

func (h *Handler) registerVendor(w http.ResponseWriter, r *http.Request) {
	h.doRegister(w, r, true)
}

func (h *Handler) doRegister(w http.ResponseWriter, r *http.Request, vendor bool) {
	var req registerReq
	if !decode(w, r, &req) {
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
	if empty(req.Email) && empty(req.PhoneNumber) {
		e = append(e, "Either email or phone number is required")
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	in := service.RegisterInput{
		FullName: req.FullName, Email: req.Email,
		PhoneNumber: req.PhoneNumber, Password: req.Password,
	}
	var err error
	if vendor {
		err = h.svc.RegisterVendor(r.Context(), in, ip(r))
	} else {
		err = h.svc.Register(r.Context(), in, ip(r))
	}
	if err != nil {
		fail(w, err)
		return
	}
	response.OK(w, "Registration successful. Please verify OTP.", nil)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Email or Phone number", req.Identifier)
	e.Required("Password", req.Password)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	res, err := h.svc.Login(r.Context(), req.Identifier, req.Password, ip(r), r.UserAgent())
	if err != nil {
		fail(w, err)
		return
	}
	response.OK(w, "Login successful", res)
}

func (h *Handler) verifyOtp(w http.ResponseWriter, r *http.Request) {
	var req verifyOtpReq
	if !decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Target (Email or Phone)", req.Target)
	e.Required("OTP", req.OtpCode)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}

	res, err := h.svc.VerifyOtp(r.Context(), req.Target, req.OtpCode, ip(r), r.UserAgent())
	if err != nil {
		fail(w, err)
		return
	}
	msg := "Phone number verified successfully"
	if strings.Contains(req.Target, "@") {
		msg = "Email verified successfully"
	}
	response.OK(w, msg, res)
}

func (h *Handler) resendOtp(w http.ResponseWriter, r *http.Request) {
	var req resendOtpReq
	if !decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Target (Email or Phone)", req.Target)
	e.Required("OTP type", req.OtpType)
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	otpType := domain.OtpType(req.OtpType)
	if !otpType.Valid() {
		response.Error(w, http.StatusBadRequest, "Invalid OTP type", "VALIDATION_ERROR")
		return
	}

	if err := h.svc.ResendOtp(r.Context(), req.Target, otpType, ip(r)); err != nil {
		fail(w, err)
		return
	}
	response.OK(w, "OTP sent if account exists", nil)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if !decode(w, r, &req) {
		return
	}
	if req.RefreshToken == "" {
		response.Error(w, http.StatusBadRequest, "Refresh token is required", "VALIDATION_ERROR")
		return
	}
	res, err := h.svc.Refresh(r.Context(), req.RefreshToken, ip(r), r.UserAgent())
	if err != nil {
		fail(w, err)
		return
	}
	response.OK(w, "Token refreshed successfully", res)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if !decode(w, r, &req) {
		return
	}
	if req.RefreshToken == "" {
		response.Error(w, http.StatusBadRequest, "Refresh token is required", "VALIDATION_ERROR")
		return
	}
	if err := h.svc.Logout(r.Context(), req.RefreshToken); err != nil {
		fail(w, err)
		return
	}
	response.OK(w, "Logged out successfully", nil)
}

func (h *Handler) logoutAll(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	if err := h.svc.LogoutAllDevices(r.Context(), userID); err != nil {
		fail(w, err)
		return
	}
	response.OK(w, "Logged out from all devices", nil)
}

func (h *Handler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotReq
	if !decode(w, r, &req) {
		return
	}
	if req.Identifier == "" {
		response.Error(w, http.StatusBadRequest, "Email or phone number is required", "VALIDATION_ERROR")
		return
	}
	if err := h.svc.ForgotPassword(r.Context(), req.Identifier, ip(r)); err != nil {
		fail(w, err)
		return
	}
	response.OK(w, "Password reset link sent if account exists", nil)
}

func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetReq
	if !decode(w, r, &req) {
		return
	}
	var e validate.Errors
	e.Required("Email or phone number", req.Identifier)
	e.Required("Reset token", req.Token)
	e.Required("New password", req.NewPassword)
	if req.NewPassword != "" {
		e.Password("Password", req.NewPassword)
	}
	if len(e) > 0 {
		response.Error(w, http.StatusBadRequest, e.Message(), "VALIDATION_ERROR")
		return
	}
	if err := h.svc.ResetPassword(r.Context(), req.Identifier, req.Token, req.NewPassword); err != nil {
		fail(w, err)
		return
	}
	response.OK(w, "Password reset successfully", nil)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserID(r.Context())
	v, err := h.svc.Me(r.Context(), userID)
	if err != nil {
		fail(w, err)
		return
	}
	response.OK(w, "User retrieved successfully", v)
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst); err != nil {
		response.Error(w, http.StatusBadRequest, "Malformed request body", "MALFORMED_JSON")
		return false
	}
	return true
}

// fail maps an apperr to its intended status; anything else is an unexpected
// bug and must not leak its message to the caller.
func fail(w http.ResponseWriter, err error) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		response.Error(w, ae.Status, ae.Message, ae.Code)
		return
	}
	logger.Error("unhandled error", logger.Err(err))
	response.Error(w, http.StatusInternalServerError, "Something went wrong", "INTERNAL_ERROR")
}

func ip(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		hops := strings.Split(xff, ",")
		return strings.TrimSpace(hops[len(hops)-1])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func empty(s *string) bool { return s == nil || strings.TrimSpace(*s) == "" }
