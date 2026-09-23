package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// ErrTokenDead means FCM rejected the device token itself: the app was
// uninstalled, or the token was replaced. Retrying cannot help, so the caller
// deactivates the token instead of spending its retry budget on it.
var ErrTokenDead = errors.New("device token no longer valid")

// fcmSender delivers over Firebase Cloud Messaging v1.
//
// Only the OAuth2 token exchange uses a library; the send itself is a plain
// POST, matching how the Twilio and WhatsApp senders call their vendors. The
// full Firebase SDK would pull in a large dependency tree for one endpoint.
type fcmSender struct {
	projectID string
	credsFile string
	httpc     *http.Client
	// baseURL overrides the FCM endpoint in tests. Empty means the real one.
	baseURL string

	// ts is built on first send rather than at construction: reading the
	// credentials file must not fail process startup, and a deployment with no
	// FCM configured never touches it at all. oauth2 caches and refreshes the
	// token internally, so this is created once and reused.
	ts oauth2.TokenSource
}

func (s *fcmSender) Live() bool { return s.projectID != "" && s.credsFile != "" }

func (s *fcmSender) Send(ctx context.Context, m Message) error {
	if !s.Live() {
		return logOnly(Push, m)
	}
	tok, err := s.accessToken(ctx)
	if err != nil {
		return fmt.Errorf("fcm: credentials: %w", err)
	}

	// Notification carries title/body for the OS banner; data carries the
	// routing hints the app uses to open the right screen when tapped.
	payload := map[string]any{
		"message": map[string]any{
			"token": m.To,
			"notification": map[string]any{
				"title": m.Subject,
				"body":  m.Body,
			},
			"data": m.Data,
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := s.baseURL
	if endpoint == "" {
		endpoint = "https://fcm.googleapis.com/v1/projects/" + s.projectID + "/messages:send"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	body := buf.String()

	// A dead token is reported as 404 UNREGISTERED or 400 INVALID_ARGUMENT.
	// Distinguished from a transient failure so the caller can retire it: an
	// uninstalled app would otherwise consume every retry, forever.
	if resp.StatusCode == http.StatusNotFound ||
		strings.Contains(body, "UNREGISTERED") ||
		strings.Contains(body, "INVALID_ARGUMENT") {
		return fmt.Errorf("%w: %s", ErrTokenDead, truncate(body, 160))
	}
	return fmt.Errorf("fcm: %s: %s", resp.Status, truncate(body, 200))
}

func (s *fcmSender) accessToken(ctx context.Context) (string, error) {
	if s.ts == nil {
		raw, err := os.ReadFile(s.credsFile)
		if err != nil {
			return "", err
		}
		// The FCM send scope is the only one this needs; a broader scope on a
		// long-lived service account is a liability, not a convenience.
		cfg, err := google.JWTConfigFromJSON(raw,
			"https://www.googleapis.com/auth/firebase.messaging")
		if err != nil {
			return "", err
		}
		s.ts = cfg.TokenSource(ctx)
	}
	t, err := s.ts.Token()
	if err != nil {
		return "", err
	}
	return t.AccessToken, nil
}
