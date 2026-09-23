package notify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// A sender with no credentials must degrade to logging, not error - the whole
// pipeline has to run on a box with no vendor account.
func TestSendersFallBackToLogOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    Sender
	}{
		{"email", &emailSender{}},
		{"sms", &twilioSender{}},
		{"whatsapp", &whatsappSender{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.s.Live() {
				t.Fatal("Live() true with no credentials")
			}
			if err := tc.s.Send(context.Background(), Message{To: "x", Body: "y"}); err != nil {
				t.Fatalf("log-only send returned error: %v", err)
			}
		})
	}
}

func TestLiveRequiresAllCredentials(t *testing.T) {
	if (&twilioSender{sid: "a", token: "b"}).Live() {
		t.Error("twilio Live() without From")
	}
	if !(&twilioSender{sid: "a", token: "b", from: "+1"}).Live() {
		t.Error("twilio not Live() with full credentials")
	}
	if (&emailSender{host: "smtp.x"}).Live() {
		t.Error("email Live() without From")
	}
	if !(&whatsappSender{phoneID: "1", token: "t"}).Live() {
		t.Error("whatsapp not Live() with credentials")
	}
}

// A vendor's own error text must survive into the returned error, otherwise
// every failure reads "unexpected status 400" and the cause is invisible.
func TestDoSurfacesVendorError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"template not found"}}`))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	err := do(srv.Client(), req, "meta")
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if !strings.Contains(err.Error(), "template not found") {
		t.Fatalf("vendor message lost: %v", err)
	}
}

// FCM reports a dead device token as 404/UNREGISTERED. ErrTokenDead must be
// distinguishable from a transient failure, or an uninstalled app consumes
// every retry attempt forever - and a transient 500 must NOT retire a token
// that is actually fine.
func TestFCMDeadTokenIsDistinguishable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		dead   bool
	}{
		{"unregistered", 404, `{"error":{"status":"NOT_FOUND"}}`, true},
		{"invalid argument", 400, `{"error":{"status":"INVALID_ARGUMENT"}}`, true},
		{"server error", 500, `{"error":{"status":"INTERNAL"}}`, false},
		{"rate limited", 429, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			// Point the sender at the test server and skip the OAuth exchange
			// by pre-seeding a token source.
			s := &fcmSender{
				projectID: "p", credsFile: "unused",
				httpc:   srv.Client(),
				ts:      staticToken{},
				baseURL: srv.URL,
			}
			err := s.Send(context.Background(), Message{To: "tok", Body: "hi"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, ErrTokenDead); got != tc.dead {
				t.Fatalf("ErrTokenDead = %v, want %v (err: %v)", got, tc.dead, err)
			}
		})
	}
}

func TestPushSenderFallsBackToLogOnly(t *testing.T) {
	s := &fcmSender{}
	if s.Live() {
		t.Fatal("Live() with no credentials")
	}
	if err := s.Send(context.Background(), Message{To: "tok", Body: "hi"}); err != nil {
		t.Fatalf("log-only push errored: %v", err)
	}
}

// staticToken skips the service-account exchange in tests.
type staticToken struct{}

func (staticToken) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "test"}, nil
}
