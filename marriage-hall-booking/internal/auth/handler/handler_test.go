package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/migrations"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
)

const testSecret = "K3p9vL7xT2sQf6bR8mZ0nY5cJ1hW4eD2xPqRsNmLkJhGfEdCbA9Z8Y7W6V5U4T3"

func setupHandler(t *testing.T) (*http.ServeMux, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx := context.Background()
	pool, err := database.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}

	repo := repository.New(pool)
	signer, err := jwt.NewSigner(testSecret, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	otp := service.NewOtpService(repo, false)
	tokens := service.NewTokenService(repo, signer, 7*24*time.Hour, nil)
	svc := service.NewAuthService(repo, otp, tokens, "http://localhost:3000/reset-password")

	h := New(svc, signer)
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, pool
}

func uniqueEmail(t *testing.T, pool *pgxpool.Pool, phone string) string {
	t.Helper()
	email := strings.ToLower(t.Name()) + "@handler.local"
	del := func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE email = $1 OR phone_number = $2`, email, phone)
		pool.Exec(context.Background(), `DELETE FROM otp_verification WHERE target = $1 OR target = $2`, email, phone)
		pool.Exec(context.Background(), `DELETE FROM login_attempts WHERE identifier = $1 OR identifier = $2`, email, phone)
	}
	del()
	t.Cleanup(del)
	return email
}

type apiResponse struct {
	Success   bool            `json:"success"`
	Message   string          `json:"message"`
	ErrorCode string          `json:"errorCode,omitempty"`
	Data      json.RawMessage `json:"data"`
}

type userData struct {
	ID          int64   `json:"id"`
	FullName    string  `json:"fullName"`
	Email       *string `json:"email"`
	PhoneNumber *string `json:"phoneNumber"`
	Address     *string `json:"address"`
}

func TestRegisterHandlerWithAddress(t *testing.T) {
	mux, pool := setupHandler(t)
	const phone = "9876543210"
	email := uniqueEmail(t, pool, phone)

	body := map[string]any{
		"fullName":    "John Doe",
		"email":       email,
		"phoneNumber": phone,
		"password":    "Password@123",
		"address":     "Bangalore, Karnataka",
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success true, got false: %s", resp.Message)
	}

	if strings.Contains(string(resp.Data), `"phone":`) {
		t.Fatalf("expected response not to contain duplicate 'phone' field, got %s", string(resp.Data))
	}
	if strings.Contains(string(resp.Data), `"name":`) {
		t.Fatalf("expected response not to contain duplicate 'name' field, got %s", string(resp.Data))
	}

	var u userData
	if err := json.Unmarshal(resp.Data, &u); err != nil {
		t.Fatalf("failed to parse data: %v", err)
	}
	if u.Address == nil || *u.Address != "Bangalore, Karnataka" {
		t.Fatalf("expected address 'Bangalore, Karnataka', got %v", u.Address)
	}
	if u.PhoneNumber == nil || *u.PhoneNumber != phone {
		t.Fatalf("expected phoneNumber %q, got %v", phone, u.PhoneNumber)
	}
	if u.FullName != "John Doe" {
		t.Fatalf("expected fullName 'John Doe', got %q", u.FullName)
	}
}

func TestRegisterHandlerWithoutAddress(t *testing.T) {
	mux, pool := setupHandler(t)
	const phone = "9876543211"
	email := uniqueEmail(t, pool, phone)

	body := map[string]any{
		"name":     "John Doe",
		"email":    email,
		"phone":    phone,
		"password": "Password@123",
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success true, got false")
	}

	if strings.Contains(string(resp.Data), `"phone":`) {
		t.Fatalf("expected response not to contain duplicate 'phone' field, got %s", string(resp.Data))
	}
	if strings.Contains(string(resp.Data), `"name":`) {
		t.Fatalf("expected response not to contain duplicate 'name' field, got %s", string(resp.Data))
	}

	var u userData
	if err := json.Unmarshal(resp.Data, &u); err != nil {
		t.Fatalf("failed to parse data: %v", err)
	}
	if u.Address != nil {
		t.Fatalf("expected address nil, got %q", *u.Address)
	}
}

func TestRegisterHandlerWithEmptyAddress(t *testing.T) {
	mux, pool := setupHandler(t)
	const phone = "9876543212"
	email := uniqueEmail(t, pool, phone)

	body := map[string]any{
		"fullName":    "John Doe",
		"email":       email,
		"phoneNumber": phone,
		"password":    "Password@123",
		"address":     "",
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success true, got false")
	}

	if strings.Contains(string(resp.Data), `"phone":`) {
		t.Fatalf("expected response not to contain duplicate 'phone' field, got %s", string(resp.Data))
	}
	if strings.Contains(string(resp.Data), `"name":`) {
		t.Fatalf("expected response not to contain duplicate 'name' field, got %s", string(resp.Data))
	}

	var u userData
	if err := json.Unmarshal(resp.Data, &u); err != nil {
		t.Fatalf("failed to parse data: %v", err)
	}
	if u.Address != nil {
		t.Fatalf("expected address nil, got %q", *u.Address)
	}
}

func TestRegisterHandlerWithNullAddress(t *testing.T) {
	mux, pool := setupHandler(t)
	const phone = "9876543213"
	email := uniqueEmail(t, pool, phone)

	rawJSON := `{"name":"John Doe","email":"` + email + `","phone":"` + phone + `","password":"Password@123","address":null}`

	req := httptest.NewRequest("POST", "/api/v1/auth/register", strings.NewReader(rawJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success true, got false")
	}

	if strings.Contains(string(resp.Data), `"phone":`) {
		t.Fatalf("expected response not to contain duplicate 'phone' field, got %s", string(resp.Data))
	}
	if strings.Contains(string(resp.Data), `"name":`) {
		t.Fatalf("expected response not to contain duplicate 'name' field, got %s", string(resp.Data))
	}

	var u userData
	if err := json.Unmarshal(resp.Data, &u); err != nil {
		t.Fatalf("failed to parse data: %v", err)
	}
	if u.Address != nil {
		t.Fatalf("expected address nil, got %q", *u.Address)
	}
}

func TestRegisterHandlerAddressTooLong(t *testing.T) {
	mux, pool := setupHandler(t)
	const phone = "9876543214"
	email := uniqueEmail(t, pool, phone)

	longAddress := strings.Repeat("A", 501)
	body := map[string]any{
		"fullName":    "John Doe",
		"email":       email,
		"phoneNumber": "9876543214",
		"password":    "Password@123",
		"address":     longAddress,
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d: %s", w.Code, w.Body.String())
	}

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.ErrorCode != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %q", resp.ErrorCode)
	}
	if !strings.Contains(resp.Message, "Address must be at most 500 characters") {
		t.Fatalf("expected message to mention Address length, got %q", resp.Message)
	}
}

func TestExistingUserUnaffected(t *testing.T) {
	mux, pool := setupHandler(t)
	email := uniqueEmail(t, pool, "")
	ctx := context.Background()

	// Insert user directly without address, simulating existing users from before migration
	var userID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (full_name, email, password_hash, status, is_email_verified)
		 VALUES ('Old User', $1, '$2a$10$w0...fake', 'ACTIVE', TRUE) RETURNING id`, email).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to insert existing user: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO user_roles (user_id, role_id)
		 SELECT $1, id FROM roles WHERE role_name = 'ROLE_CUSTOMER'`, userID)
	if err != nil {
		t.Fatalf("failed to insert user role: %v", err)
	}

	signer, err := jwt.NewSigner(testSecret, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Generate(email, strconv.FormatInt(userID, 10), "ROLE_CUSTOMER")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	var u userData
	if err := json.Unmarshal(resp.Data, &u); err != nil {
		t.Fatalf("failed to parse data: %v", err)
	}
	if u.Address != nil {
		t.Fatalf("expected existing user address to be nil/null, got %q", *u.Address)
	}
	if u.FullName != "Old User" {
		t.Fatalf("expected fullName 'Old User', got %q", u.FullName)
	}
}

func TestUserWithAddressLoginAndMe(t *testing.T) {
	mux, pool := setupHandler(t)
	const phone = "9876543219"
	email := uniqueEmail(t, pool, phone)

	// 1. Register with address
	regBody := map[string]any{
		"fullName":    "Bangalore User",
		"email":       email,
		"phoneNumber": phone,
		"password":    "Password@123",
		"address":     "123 MG Road, Bangalore",
	}
	payload, _ := json.Marshal(regBody)
	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("registration failed: %s", w.Body.String())
	}

	// 2. Mark verified
	_, err := pool.Exec(context.Background(),
		`UPDATE users SET is_email_verified = TRUE, status = 'ACTIVE' WHERE email = $1`, email)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Login
	loginBody := map[string]any{
		"identifier": email,
		"password":   "Password@123",
	}
	lPayload, _ := json.Marshal(loginBody)
	req = httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(lPayload))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %s", w.Body.String())
	}

	var loginResp struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken string   `json:"accessToken"`
			User        userData `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("failed to parse login response: %v", err)
	}
	if loginResp.Data.User.Address == nil || *loginResp.Data.User.Address != "123 MG Road, Bangalore" {
		t.Fatalf("expected login user address '123 MG Road, Bangalore', got %v", loginResp.Data.User.Address)
	}

	// 4. Call /me with accessToken
	req = httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Data.AccessToken)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/me failed: %s", w.Body.String())
	}

	var meResp struct {
		Success bool     `json:"success"`
		Data    userData `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &meResp); err != nil {
		t.Fatalf("failed to parse /me response: %v", err)
	}
	if meResp.Data.Address == nil || *meResp.Data.Address != "123 MG Road, Bangalore" {
		t.Fatalf("expected /me address '123 MG Road, Bangalore', got %v", meResp.Data.Address)
	}
}


// address arrives as one line or as an object; neither shape may break the other.
func TestRegisterReqAddressShapes(t *testing.T) {
	var line registerReq
	if err := json.Unmarshal([]byte(`{"name":"A","address":"Bangalore, Karnataka"}`), &line); err != nil {
		t.Fatal(err)
	}
	if line.FullName != "A" || line.Address != "Bangalore, Karnataka" || line.AddressParts != nil {
		t.Fatalf("string shape: %+v", line)
	}
	var obj registerReq
	if err := json.Unmarshal([]byte(`{"fullName":"B","address":{"city":"Bengaluru"}}`), &obj); err != nil {
		t.Fatal(err)
	}
	if obj.Address != "" || obj.AddressParts == nil || *obj.AddressParts.City != "Bengaluru" {
		t.Fatalf("object shape: %+v", obj)
	}
	var none registerReq
	if err := json.Unmarshal([]byte(`{"address":null}`), &none); err != nil || none.Address != "" || none.AddressParts != nil {
		t.Fatalf("null: %+v %v", none, err)
	}
}
