package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/domain"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

func TestCouponBannerImageUploadSizeLimit(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	vendorToken, _ := signer.Generate("vendor@example.com", "20", domain.RoleHallOwner)

	// Create a multipart form with an oversized file (> 5 MB)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("code", "OVERSIZED")
	_ = writer.WriteField("discountType", "PERCENT")
	_ = writer.WriteField("discountValue", "10")
	_ = writer.WriteField("facilityId", "a0000000-0000-0000-0000-000000000001")

	// 6 MB of dummy data
	part, err := writer.CreateFormFile("banner_image", "large.png")
	if err != nil {
		t.Fatal(err)
	}
	largeData := make([]byte, 6<<20)
	_, _ = part.Write(largeData)
	writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/coupons", body)
	req.Header.Set("Authorization", "Bearer "+vendorToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for >5MB banner image, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := rr.Body.String()
	if !strings.Contains(strings.ToLower(resp), "5 mb") && !strings.Contains(strings.ToLower(resp), "too large") {
		t.Errorf("expected 5 MB limit error message, got: %s", resp)
	}
}

func TestCouponBannerImageInvalidType(t *testing.T) {
	signer, err := jwt.NewSigner("Zzzzz7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{signer: signer}
	mux := http.NewServeMux()
	h.Register(mux)

	vendorToken, _ := signer.Generate("vendor@example.com", "20", domain.RoleHallOwner)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("code", "INVALIDTYPE")
	_ = writer.WriteField("discountType", "PERCENT")
	_ = writer.WriteField("discountValue", "10")
	_ = writer.WriteField("facilityId", "a0000000-0000-0000-0000-000000000001")

	part, err := writer.CreateFormFile("banner_image", "test.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("this is plain text, not an image"))
	writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/coupons", body)
	req.Header.Set("Authorization", "Bearer "+vendorToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for non-image banner, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := rr.Body.String()
	if !strings.Contains(strings.ToLower(resp), "jpeg, png, webp") && !strings.Contains(strings.ToLower(resp), "image") {
		t.Errorf("expected image format error, got: %s", resp)
	}
}

func TestCouponBannerDecodeMultipartDataJSON(t *testing.T) {
	h := &Handler{}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("data", `{"code":"DATATEST","discountType":"FLAT","discountValue":100,"facilityId":"a0000000-0000-0000-0000-000000000001"}`)
	writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/coupons", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	decoded, _, ok := h.decodeCouponReq(rr, req)
	if !ok {
		t.Fatalf("expected decode to succeed, got error: %s", rr.Body.String())
	}
	if decoded.Code != "DATATEST" {
		t.Errorf("expected code DATATEST, got %s", decoded.Code)
	}
	if decoded.DiscountType != "FLAT" {
		t.Errorf("expected discountType FLAT, got %s", decoded.DiscountType)
	}
	if decoded.DiscountValue != 100 {
		t.Errorf("expected discountValue 100, got %f", decoded.DiscountValue)
	}
}
