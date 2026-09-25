// Package notify delivers a message over one of three channels.
//
// Every sender is configured from the environment and degrades to logging when
// its credentials are absent, so the whole pipeline - enqueue, retry,
// acknowledge - is exercisable on a dev box with no vendor account. Supplying
// credentials for one channel switches only that channel to real delivery;
// there is no separate "enable" flag to forget.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
)

type Channel string

const (
	Email    Channel = "EMAIL"
	SMS      Channel = "SMS"
	WhatsApp Channel = "WHATSAPP"
	Push     Channel = "PUSH"
)

// Message is what a Sender delivers. Subject is ignored by SMS and WhatsApp.
type Message struct {
	To      string
	Subject string
	Body    string
	// Data is push-only: key/value hints the mobile app reads to open the
	// right screen when the notification is tapped. Ignored by every other
	// channel, and FCM requires the values to be strings.
	Data map[string]string
}

type Sender interface {
	Send(ctx context.Context, m Message) error
	// Live reports whether this sender has credentials. A false here is why a
	// channel logs instead of sending, and the worker says so once at startup
	// rather than per message.
	Live() bool
}

// Senders resolves a channel to its sender. Unknown channels are a programming
// error, not a delivery failure, so Get returns nil and the caller fails loudly.
type Senders map[Channel]Sender

// FromEnv builds all three senders. Never returns an error: a missing
// credential is a log-only channel, not a startup failure - the API must keep
// taking bookings even with no mail provider configured.
func FromEnv(httpc *http.Client) Senders {
	if httpc == nil {
		// Bounded: a hung vendor API must not wedge the retry loop, which is
		// single-threaded per tick.
		httpc = &http.Client{Timeout: 15 * time.Second}
	}
	return Senders{
		Email:    &emailSender{host: os.Getenv("SMTP_HOST"), port: envOr("SMTP_PORT", "587"), user: os.Getenv("SMTP_USER"), pass: os.Getenv("SMTP_PASS"), from: envOr("SMTP_FROM", os.Getenv("SMTP_USER"))},
		SMS:      &twilioSender{sid: os.Getenv("TWILIO_SID"), token: os.Getenv("TWILIO_TOKEN"), from: os.Getenv("TWILIO_FROM"), httpc: httpc},
		WhatsApp: &whatsappSender{phoneID: os.Getenv("WHATSAPP_PHONE_ID"), token: os.Getenv("WHATSAPP_TOKEN"), template: os.Getenv("WHATSAPP_TEMPLATE"), lang: envOr("WHATSAPP_LANG", "en_US"), httpc: httpc},
		Push:     &fcmSender{projectID: os.Getenv("FCM_PROJECT_ID"), credsFile: os.Getenv("FCM_CREDENTIALS_FILE"), httpc: httpc},
	}
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

// logOnly is the shared fallback: it records what would have been sent, at the
// same level as a real send, so a dev log reads the same as production.
func logOnly(ch Channel, m Message) error {
	logger.Info("notify: would send (no credentials)",
		"channel", string(ch), "to", m.To, "subject", m.Subject, "body", truncate(m.Body, 120))
	return nil
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- email ---

type emailSender struct{ host, port, user, pass, from string }

func (s *emailSender) Live() bool { return s.host != "" && s.from != "" }

func (s *emailSender) Send(ctx context.Context, m Message) error {
	if !s.Live() {
		return logOnly(Email, m)
	}
	// Plain text, no MIME multipart: these are short transactional notices.
	// Reach for a mail library if HTML or attachments are ever needed.
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		s.from, m.To, m.Subject, m.Body)
	addr := s.host + ":" + s.port
	var auth smtp.Auth
	if s.user != "" {
		auth = smtp.PlainAuth("", s.user, s.pass, s.host)
	}
	return smtp.SendMail(addr, auth, s.from, []string{m.To}, []byte(msg))
}

// --- sms (twilio) ---

type twilioSender struct {
	sid, token, from string
	httpc            *http.Client
}

func (s *twilioSender) Live() bool { return s.sid != "" && s.token != "" && s.from != "" }

func (s *twilioSender) Send(ctx context.Context, m Message) error {
	if !s.Live() {
		return logOnly(SMS, m)
	}
	form := url.Values{"To": {m.To}, "From": {s.from}, "Body": {m.Body}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.twilio.com/2010-04-01/Accounts/"+s.sid+"/Messages.json",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.sid, s.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(s.httpc, req, "twilio")
}

// --- whatsapp (meta cloud api) ---

type whatsappSender struct {
	phoneID, token, template, lang string
	httpc                          *http.Client
}

func (s *whatsappSender) Live() bool { return s.phoneID != "" && s.token != "" }

func (s *whatsappSender) Send(ctx context.Context, m Message) error {
	if !s.Live() {
		return logOnly(WhatsApp, m)
	}
	// Meta rejects free-form text outside a 24h customer-service window, so a
	// template is the only thing that reliably reaches an owner who has never
	// messaged the business. WHATSAPP_TEMPLATE unset falls back to plain text,
	// which works only inside that window - fine for testing, not for
	// production; the template must be approved in Meta's console first.
	var payload map[string]any
	if s.template != "" {
		payload = map[string]any{
			"messaging_product": "whatsapp", "to": m.To, "type": "template",
			"template": map[string]any{
				"name":     s.template,
				"language": map[string]any{"code": s.lang},
				"components": []any{map[string]any{
					"type":       "body",
					"parameters": []any{map[string]any{"type": "text", "text": m.Body}},
				}},
			},
		}
	} else {
		payload = map[string]any{
			"messaging_product": "whatsapp", "to": m.To, "type": "text",
			"text": map[string]any{"body": m.Body},
		}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://graph.facebook.com/v21.0/"+s.phoneID+"/messages", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	return do(s.httpc, req, "whatsapp")
}

// do sends the request and turns a non-2xx into an error carrying the vendor's
// own message - without it every failure reads "unexpected status 400" and the
// actual cause (bad template name, unverified number) is invisible.
func do(c *http.Client, req *http.Request, vendor string) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return fmt.Errorf("%s: %s: %s", vendor, resp.Status, truncate(buf.String(), 200))
}
