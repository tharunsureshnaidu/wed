// Package service turns a booking event into outbox rows and delivers them.
//
// Enqueue and dispatch are deliberately split: the Kafka consumer only writes
// rows (fast, transactional, idempotent) while a ticker does the slow vendor
// calls. A hung SMS provider therefore delays retries, never event consumption.
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/notify"
)

type Service struct {
	repo    *repository.Repo
	senders notify.Senders

	// RetryEvery is how long until a still-unacknowledged owner is chased
	// again, and MaxAttempts caps a permanently failing destination.
	RetryEvery  time.Duration
	MaxAttempts int
	BatchSize   int

	// AdminEmail/AdminPhone are the ops destinations. Empty means admin copies
	// are not sent at all - deliberately opt-in, so a deployment that has not
	// set them does not silently drop messages into an unread mailbox.
	AdminEmail string
	AdminPhone string

	// PublicBaseURL is where the one-tap link points. Empty means no link is
	// included - the message still sends, it just asks the recipient to use
	// the app instead.
	PublicBaseURL string
	// AckTokenTTL bounds how long a link in an old message keeps working.
	AckTokenTTL time.Duration
}

func New(repo *repository.Repo, senders notify.Senders) *Service {
	return &Service{
		repo: repo, senders: senders,
		RetryEvery:  30 * time.Minute,
		MaxAttempts: 10,
		BatchSize:   50,
		AdminEmail:  os.Getenv("ADMIN_NOTIFY_EMAIL"),
		AdminPhone:  os.Getenv("ADMIN_NOTIFY_PHONE"),

		PublicBaseURL: strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),
		AckTokenTTL:   14 * 24 * time.Hour,
	}
}

// newAckToken returns a URL-safe random token. crypto/rand, not math/rand:
// this token is the only thing standing between a stranger and silencing an
// owner's reminders, so it must not be predictable from another one.
func newAckToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// chased reports whether a role keeps its retry schedule after a successful
// send. OWNER must act on the booking and ADMIN oversees it; the customer's
// copy is informational and done once delivered.
func chased(role string) bool { return role == "OWNER" || role == "ADMIN" }

// EnqueueBookingCreated fans a booking out to every reachable
// recipient/channel. Missing contact details are skipped, not errors: an owner
// with no WhatsApp number still gets email.
func (s *Service) EnqueueBookingCreated(ctx context.Context, bookingID string) error {
	c, err := s.repo.LoadBookingContext(ctx, bookingID)
	if err != nil {
		return err
	}

	ownerSubject := fmt.Sprintf("New booking for %s", c.Facility)
	ownerBody := ownerText(c)
	custSubject := fmt.Sprintf("Booking confirmed: %s", c.Facility)
	custBody := customerText(c)
	adminSubject := fmt.Sprintf("[ops] New booking: %s", c.Facility)
	adminBody := adminText(c)

	targets := []struct {
		role          string
		userID        int64
		email, phone  *string
		subject, body string
	}{
		{"OWNER", c.OwnerUserID, c.OwnerEmail, c.OwnerPhone, ownerSubject, ownerBody},
		{"CUSTOMER", c.CustomerUserID, c.CustomerEmail, c.CustomerPhone, custSubject, custBody},
		{"ADMIN", 0, strPtr(s.AdminEmail), strPtr(s.AdminPhone), adminSubject, adminBody},
	}

	var queued int
	for _, t := range targets {
		uid := t.userID
		for _, ch := range []struct {
			channel notify.Channel
			dest    *string
		}{
			{notify.Email, t.email},
			{notify.SMS, t.phone},
			{notify.WhatsApp, t.phone},
			// Push addresses the person, so the destination is their user id;
			// devices are resolved at send time. Nil for the ops address,
			// which is not a user.
			{notify.Push, pushDest(t.role, t.userID)},
		} {
			if ch.dest == nil || strings.TrimSpace(*ch.dest) == "" {
				continue
			}
			subject := t.subject
			// No single user behind the ops address, so the FK stays NULL.
			recipient := &uid
			if t.role == "ADMIN" {
				recipient = nil
			}
			// A chased role gets its own link: per row, so a forwarded message
			// acknowledges only what its recipient was actually sent.
			body := t.body
			var token string
			if chased(t.role) {
				tok, err := newAckToken()
				if err != nil {
					return err
				}
				token = tok
				// A push notification is tapped, not read as text: the app
				// opens the booking and confirms in-place, so a pasted URL in
				// the banner would only be noise.
				if s.PublicBaseURL != "" && ch.channel != notify.Push {
					body += fmt.Sprintf("\n\nConfirm you have seen this booking:\n%s/ack/%s", s.PublicBaseURL, token)
					body += fmt.Sprintf("\nDecline: %s/decline/%s", s.PublicBaseURL, token)
				}
			}
			if err := s.repo.Enqueue(ctx, repository.Notification{
				BookingID: bookingID, RecipientRole: t.role, RecipientID: recipient,
				Channel: string(ch.channel), Destination: strings.TrimSpace(*ch.dest),
				Subject: &subject, Body: body,
				AckToken: token, AckTokenTTL: s.AckTokenTTL,
			}); err != nil {
				return err
			}
			queued++
		}
	}
	logger.Info("notify: queued", "bookingId", bookingID, "rows", queued, "facility", c.Facility)
	return nil
}

// Event is a notification that is not about a booking: an approval, a
// rejection, a block, a new review. These are told once and never chased -
// there is nothing for the recipient to acknowledge.
type Event struct {
	Type      string // "facility.approved", "vendor.kyc.rejected", ...
	SubjectID string // the facility, vendor or user the event is about
	UserID    int64  // who to tell
	Subject   string
	Body      string
	// Channels restricts delivery. Empty means every channel the recipient is
	// reachable on. A block, for instance, must not rely on push or in-app:
	// blocking revokes the user's sessions, so they cannot read either.
	Channels []notify.Channel
}

// NotifyUser enqueues an event notification to one user, resolving their
// contact details from their account.
//
// Unreachable users are skipped rather than erroring: a user with no phone
// still gets email, and an event nobody can be told about must not fail the
// admin action that produced it.
func (s *Service) NotifyUser(ctx context.Context, e Event) error {
	c, err := s.repo.LoadUserContact(ctx, e.UserID)
	if err != nil {
		return err
	}
	channels := e.Channels
	if len(channels) == 0 {
		channels = []notify.Channel{notify.Email, notify.SMS, notify.WhatsApp, notify.Push}
	}
	uid := e.UserID
	for _, ch := range channels {
		var dest string
		switch ch {
		case notify.Email:
			if c.Email != nil {
				dest = *c.Email
			}
		case notify.SMS, notify.WhatsApp:
			if c.Phone != nil {
				dest = *c.Phone
			}
		case notify.Push:
			// A push row addresses the person; the device list is resolved at
			// send time, so the destination is the user id.
			dest = strconv.FormatInt(e.UserID, 10)
		}
		if strings.TrimSpace(dest) == "" {
			continue
		}
		subject := e.Subject
		if err := s.repo.Enqueue(ctx, repository.Notification{
			RecipientRole: "USER", RecipientID: &uid,
			Channel: string(ch), Destination: strings.TrimSpace(dest),
			Subject: &subject, Body: e.Body,
			EventType: e.Type, SubjectID: e.SubjectID,
		}); err != nil {
			return err
		}
	}
	logger.Info("notify: event queued", "event", e.Type, "userId", e.UserID, "subjectId", e.SubjectID)
	return nil
}

// NotifyAdmins enqueues an event to the ops address. Used when something needs
// platform attention rather than one user's: a new facility awaiting approval,
// an owner declining a booking.
func (s *Service) NotifyAdmins(ctx context.Context, e Event) error {
	for _, t := range []struct {
		ch   notify.Channel
		dest string
	}{
		{notify.Email, s.AdminEmail},
		{notify.SMS, s.AdminPhone},
		{notify.WhatsApp, s.AdminPhone},
	} {
		if strings.TrimSpace(t.dest) == "" {
			continue
		}
		subject := e.Subject
		if err := s.repo.Enqueue(ctx, repository.Notification{
			RecipientRole: "ADMIN", RecipientID: nil,
			Channel: string(t.ch), Destination: strings.TrimSpace(t.dest),
			Subject: &subject, Body: e.Body,
			EventType: e.Type, SubjectID: e.SubjectID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// SaveUserLocation stores where a user is, for radius targeting.
func (s *Service) SaveUserLocation(ctx context.Context, userID int64, lat, lng float64, source string) error {
	return s.repo.SaveUserLocation(ctx, userID, lat, lng, source)
}

// SetGeoNotifications is the user's opt-out of nearby-venue announcements.
func (s *Service) SetGeoNotifications(ctx context.Context, userID int64, enabled bool) error {
	return s.repo.SetGeoNotifications(ctx, userID, enabled)
}

// BackfillLocations gives users without a location one inferred from the
// venues they have booked.
func (s *Service) BackfillLocations(ctx context.Context) (int64, error) {
	return s.repo.BackfillLocationsFromBookings(ctx)
}

// RegisterDevice records a device token for push. Idempotent: the app calls
// this on every launch, and re-registering refreshes rather than duplicates.
func (s *Service) RegisterDevice(ctx context.Context, userID int64, token, platform string) error {
	return s.repo.SaveDevice(ctx, userID, repository.Device{Token: token, Platform: platform})
}

// DeleteDevice deregisters a token, typically on logout.
func (s *Service) DeleteDevice(ctx context.Context, userID int64, token string) (int64, error) {
	return s.repo.DeleteDevice(ctx, userID, token)
}

// Cancel stops chasing a booking that no longer needs an answer.
func (s *Service) Cancel(ctx context.Context, bookingID string) error {
	return s.repo.CancelForBooking(ctx, bookingID)
}

// Ack is the owner confirming they have seen the booking; retries stop.
func (s *Service) Ack(ctx context.Context, bookingID string, ownerID int64) (int64, error) {
	return s.repo.Ack(ctx, bookingID, ownerID)
}

// AckAdmin is ops confirming the same, for the admin copy only. Caller must
// already have checked ROLE_ADMIN.
func (s *Service) AckAdmin(ctx context.Context, bookingID string) (int64, error) {
	return s.repo.AckAdmin(ctx, bookingID)
}

// AckByToken redeems a one-tap link from a message.
func (s *Service) AckByToken(ctx context.Context, token string) (bookingID, role string, err error) {
	return s.repo.AckByToken(ctx, token)
}

// DeclineByToken is the owner refusing the booking. Their reminders stop and
// ops is told, because a declined booking needs a human to find the customer
// another venue - silence would leave the customer holding a booking nobody
// intends to honour.
func (s *Service) DeclineByToken(ctx context.Context, token string) (bookingID, role string, err error) {
	bookingID, role, err = s.repo.DeclineByToken(ctx, token)
	if err != nil {
		return bookingID, role, err
	}
	c, lerr := s.repo.LoadBookingContext(ctx, bookingID)
	if lerr != nil {
		// The decline itself stands; ops just gets a terser message.
		logger.Error("notify: decline context", "bookingId", bookingID, logger.Err(lerr))
		return bookingID, role, nil
	}
	body := fmt.Sprintf(
		"The owner has DECLINED this booking.\n\n%s\nDates: %s to %s\nCustomer: %s (%s)\n\nBooking ref: %s\n\nThe customer has not been told - someone needs to contact them.",
		c.Facility, c.StartDate.Format("02 Jan 2006"), c.EndDate.Format("02 Jan 2006"),
		c.CustomerName, contact(c.CustomerEmail, c.CustomerPhone), bookingID)
	if nerr := s.NotifyAdmins(ctx, Event{
		Type:      "booking.declined",
		SubjectID: bookingID,
		Subject:   fmt.Sprintf("[ops] Booking DECLINED: %s", c.Facility),
		Body:      body,
	}); nerr != nil {
		logger.Error("notify: decline admin alert", "bookingId", bookingID, logger.Err(nerr))
	}
	return bookingID, role, nil
}

// Dispatch sends one batch of due notifications. Called on a ticker; returns
// how many it attempted so the caller can log only when there was work.
func (s *Service) Dispatch(ctx context.Context) (int, error) {
	due, err := s.repo.ClaimDue(ctx, s.BatchSize, s.RetryEvery)
	if err != nil || len(due) == 0 {
		return 0, err
	}
	for _, n := range due {
		sender := s.senders[notify.Channel(n.Channel)]
		if sender == nil {
			// Unknown channel is a bug, not a transient failure - retrying
			// cannot fix it, so it goes terminal immediately.
			_ = s.repo.MarkFailed(ctx, n.ID, s.MaxAttempts, s.MaxAttempts, "unknown channel "+n.Channel)
			continue
		}
		subject := ""
		if n.Subject != nil {
			subject = *n.Subject
		}
		var err error
		if notify.Channel(n.Channel) == notify.Push {
			err = s.sendPush(ctx, sender, n, subject)
		} else {
			err = sender.Send(ctx, notify.Message{To: n.Destination, Subject: subject, Body: n.Body})
		}
		if err != nil {
			logger.Warn("notify: send failed", "channel", n.Channel, "to", n.Destination,
				"attempt", n.Attempts, "bookingId", n.BookingID, logger.Err(err))
			if err := s.repo.MarkFailed(ctx, n.ID, n.Attempts, s.MaxAttempts, err.Error()); err != nil {
				logger.Error("notify: mark failed", logger.Err(err))
			}
			continue
		}
		// A customer notice is done once delivered; owner and admin keep their
		// retry schedule until acknowledged - that is the "chase" behaviour.
		done := !chased(n.RecipientRole)
		if err := s.repo.MarkSent(ctx, n.ID, done); err != nil {
			logger.Error("notify: mark sent", logger.Err(err))
		}
		logger.Info("notify: sent", "channel", n.Channel, "to", n.Destination,
			"role", n.RecipientRole, "bookingId", n.BookingID, "attempt", n.Attempts)
	}
	return len(due), nil
}

// sendPush fans one outbox row out to every live device the recipient has.
//
// A PUSH row addresses a person, not a handset: the user's device list is only
// known at send time, and it changes as they install, reinstall and log out.
// The row succeeds if any device accepted it - one dead phone must not keep
// re-notifying the working one.
//
// Tokens FCM rejects as dead are retired here rather than retried: an
// uninstalled app would otherwise consume the whole retry budget and never
// succeed.
func (s *Service) sendPush(ctx context.Context, sender notify.Sender, n repository.Notification, subject string) error {
	if n.RecipientID == nil {
		// No user behind the row (an ops address), so no devices to push to.
		// Not an error: the other channels carry that copy.
		return nil
	}
	tokens, err := s.repo.ActiveTokens(ctx, *n.RecipientID)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		// Nobody has registered a device yet. Reported as success so the row
		// goes terminal instead of retrying against a user who may never
		// install the app.
		logger.Debug("notify: no devices registered", "userId", *n.RecipientID)
		return nil
	}
	data := map[string]string{"eventType": n.EventType, "subjectId": n.SubjectID}
	if n.BookingID != "" {
		data["bookingId"] = n.BookingID
	}

	var delivered int
	var lastErr error
	for _, tok := range tokens {
		err := sender.Send(ctx, notify.Message{
			To: tok, Subject: subject, Body: n.Body, Data: data,
		})
		switch {
		case err == nil:
			delivered++
		case errors.Is(err, notify.ErrTokenDead):
			if derr := s.repo.DeactivateToken(ctx, tok); derr != nil {
				logger.Error("notify: deactivate token", logger.Err(derr))
			}
			logger.Info("notify: retired dead device token", "userId", *n.RecipientID)
		default:
			lastErr = err
		}
	}
	if delivered > 0 {
		return nil
	}
	return lastErr
}

// Run drives Dispatch on a ticker until ctx is cancelled.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.Dispatch(ctx); err != nil {
				logger.Error("notify: dispatch", logger.Err(err))
			}
		}
	}
}

func ownerText(c *repository.BookingContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "New booking for %s\n\n", c.Facility)
	fmt.Fprintf(&b, "Dates:   %s to %s\n", c.StartDate.Format("02 Jan 2006"), c.EndDate.Format("02 Jan 2006"))
	writeTime(&b, c)
	if c.GuestCount != nil {
		fmt.Fprintf(&b, "Guests:  %d\n", *c.GuestCount)
	}
	// Only when asked for: an absent room count is not "0 rooms", and printing
	// a line the customer never filled in invites the owner to act on it.
	if c.RoomCount != nil {
		fmt.Fprintf(&b, "Rooms:   %d\n", *c.RoomCount)
	}
	if c.EventType != nil && *c.EventType != "" {
		fmt.Fprintf(&b, "Event:   %s\n", *c.EventType)
	}
	fmt.Fprintf(&b, "Amount:  %.2f\n", c.TotalAmount)
	fmt.Fprintf(&b, "Booked by: %s\n\n", c.CustomerName)
	fmt.Fprintf(&b, "Booking ref: %s\n", c.BookingID)
	b.WriteString("\nYou will keep receiving this reminder until you confirm the booking.")
	return b.String()
}

func customerText(c *repository.BookingContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s, your booking at %s is confirmed.\n\n", c.CustomerName, c.Facility)
	fmt.Fprintf(&b, "Dates:   %s to %s\n", c.StartDate.Format("02 Jan 2006"), c.EndDate.Format("02 Jan 2006"))
	writeTime(&b, c)
	if c.GuestCount != nil {
		fmt.Fprintf(&b, "Guests:  %d\n", *c.GuestCount)
	}
	fmt.Fprintf(&b, "Amount:  %.2f\n", c.TotalAmount)
	if c.City != nil && *c.City != "" {
		fmt.Fprintf(&b, "City:    %s\n", *c.City)
	}
	fmt.Fprintf(&b, "\nBooking ref: %s\n", c.BookingID)
	return b.String()
}

// adminText is the ops view: who booked what from whom, with both sides'
// contacts so support can reach either without a lookup.
func adminText(c *repository.BookingContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "New booking: %s\n\n", c.Facility)
	fmt.Fprintf(&b, "Dates:    %s to %s\n", c.StartDate.Format("02 Jan 2006"), c.EndDate.Format("02 Jan 2006"))
	writeTime(&b, c)
	if c.GuestCount != nil {
		fmt.Fprintf(&b, "Guests:   %d\n", *c.GuestCount)
	}
	if c.RoomCount != nil {
		fmt.Fprintf(&b, "Rooms:    %d\n", *c.RoomCount)
	}
	fmt.Fprintf(&b, "Amount:   %.2f\n\n", c.TotalAmount)
	fmt.Fprintf(&b, "Owner:    %s (%s)\n", c.OwnerName, contact(c.OwnerEmail, c.OwnerPhone))
	fmt.Fprintf(&b, "Customer: %s (%s)\n\n", c.CustomerName, contact(c.CustomerEmail, c.CustomerPhone))
	fmt.Fprintf(&b, "Booking ref: %s\n", c.BookingID)
	return b.String()
}

func contact(email, phone *string) string {
	var parts []string
	if email != nil && *email != "" {
		parts = append(parts, *email)
	}
	if phone != nil && *phone != "" {
		parts = append(parts, *phone)
	}
	if len(parts) == 0 {
		return "no contact on file"
	}
	return strings.Join(parts, ", ")
}

// pushDest is the user id a push row is addressed to, or nil for the ops
// address - an email and phone number with no account and so no devices.
func pushDest(role string, userID int64) *string {
	if role == "ADMIN" || userID == 0 {
		return nil
	}
	s := strconv.FormatInt(userID, 10)
	return &s
}

// strPtr turns an empty config value into a nil destination, which the
// enqueue loop already skips.
func strPtr(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

func writeTime(b *strings.Builder, c *repository.BookingContext) {
	if c.StartTime != nil && c.EndTime != nil {
		fmt.Fprintf(b, "Time:    %s - %s\n", hhmm(*c.StartTime), hhmm(*c.EndTime))
	}
}

// hhmm trims Postgres's "18:00:00.000000" to "18:00". The column is a bare
// time, so there is no zone to lose.
func hhmm(t string) string {
	if len(t) >= 5 {
		return t[:5]
	}
	return t
}

// --- geo-targeted announcements ---

// GeoRadiusMetres is how far a "near you" announcement reaches. Overridable
// with GEO_RADIUS_KM.
const GeoRadiusMetres = 50000

// GeoMaxRecipients caps one announcement.
//
// A radius blast is the one notification path where a single admin action can
// address an unbounded number of people. Without a cap, one coupon in a dense
// city enqueues a row per nearby user per channel, and the retry loop then
// works through all of them. The cap makes the worst case knowable.
const GeoMaxRecipients = 2000

// GeoAnnounce tells nearby customers about something new.
//
// PUSH only, deliberately: this is marketing, not a transaction. SMS and
// WhatsApp bill per message, so a coupon launch to 2000 people would be a real
// invoice and a plausible spam complaint; push costs nothing and is what an app
// notification is for.
type GeoAnnounce struct {
	Type      string // "facility.nearby", "coupon.nearby", ...
	SubjectID string
	Lat, Lng  float64
	Subject   string
	// Body is formatted per recipient with their distance in km.
	Body func(distanceKm float64) string
	// ExcludeUserID is the actor - the vendor who added the venue does not
	// need telling it is near them.
	ExcludeUserID int64
}

// AnnounceNearby enqueues one push per nearby customer. Returns how many.
//
// Never returns an error to its caller's request path: a failed announcement
// must not fail the facility creation or coupon that triggered it.
func (s *Service) AnnounceNearby(ctx context.Context, a GeoAnnounce) int {
	radius := float64(GeoRadiusMetres)
	if v := os.Getenv("GEO_RADIUS_KM"); v != "" {
		if km, err := strconv.ParseFloat(v, 64); err == nil && km > 0 {
			radius = km * 1000
		}
	}
	targets, err := s.repo.UsersNear(ctx, a.Lat, a.Lng, radius, a.ExcludeUserID, GeoMaxRecipients)
	if err != nil {
		logger.Error("geo: find nearby users", "event", a.Type, logger.Err(err))
		return 0
	}
	if len(targets) == 0 {
		logger.Debug("geo: no users in radius", "event", a.Type, "subjectId", a.SubjectID)
		return 0
	}

	var queued int
	for _, t := range targets {
		uid := t.UserID
		subject := a.Subject
		km := t.Distance / 1000
		if err := s.repo.Enqueue(ctx, repository.Notification{
			RecipientRole: "USER", RecipientID: &uid,
			Channel:     string(notify.Push),
			Destination: strconv.FormatInt(uid, 10),
			Subject:     &subject,
			Body:        a.Body(km),
			EventType:   a.Type,
			// Per recipient, so one user's copy cannot collide with another's
			// on the (event, subject, role, channel) unique key.
			SubjectID: a.SubjectID + ":" + strconv.FormatInt(uid, 10),
		}); err != nil {
			logger.Error("geo: enqueue", "event", a.Type, "userId", uid, logger.Err(err))
			continue
		}
		queued++
	}
	if queued == GeoMaxRecipients {
		logger.Warn("geo: recipient cap reached, some nearby users not notified",
			"event", a.Type, "subjectId", a.SubjectID, "cap", GeoMaxRecipients)
	}
	logger.Info("geo: announced to nearby users",
		"event", a.Type, "subjectId", a.SubjectID, "recipients", queued,
		"radiusKm", radius/1000)
	return queued
}

// AnnounceFacilityNearby announces something about a venue to nearby users.
//
// dedupeKey distinguishes one announcement from another about the same venue:
// a new coupon and a new amenity are both "facility.nearby" for the same
// facility, and without it the second silently collides with the first on the
// outbox's idempotency key and is never sent.
//
// A facility with no coordinates is skipped and logged rather than guessed at -
// a wrong-city blast is worse than no blast.
func (s *Service) AnnounceFacilityNearby(ctx context.Context, facilityID, what, dedupeKey string) int {
	lat, lng, name, ownerID, ok, err := s.repo.FacilityPoint(ctx, facilityID)
	if err != nil {
		logger.Error("geo: facility lookup", "facilityId", facilityID, logger.Err(err))
		return 0
	}
	if !ok {
		logger.Warn("geo: facility has no coordinates, radius notification skipped",
			"facilityId", facilityID, "name", name,
			"fix", "PATCH /api/v1/admin/facilities/{id} with lat and lng")
		return 0
	}
	subject := name + " — " + what
	return s.AnnounceNearby(ctx, GeoAnnounce{
		Type: "facility.nearby", SubjectID: facilityID + ":" + dedupeKey,
		Lat: lat, Lng: lng, Subject: subject,
		Body: func(km float64) string {
			return fmt.Sprintf("%s is %.0f km from you.\n\n%s", name, km, what)
		},
		ExcludeUserID: ownerID,
	})
}

// Feed, UnreadCount, MarkRead and MarkAllRead are thin passthroughs: the feed
// is a read of rows this package already owns, and there is nothing to decide
// between the handler and the query.
func (s *Service) Feed(ctx context.Context, userID int64, unreadOnly bool, before *time.Time, limit int) ([]repository.FeedItem, error) {
	return s.repo.Feed(ctx, userID, unreadOnly, before, limit)
}

func (s *Service) UnreadCount(ctx context.Context, userID int64) (int, error) {
	return s.repo.UnreadCount(ctx, userID)
}

func (s *Service) MarkRead(ctx context.Context, userID int64, id string) (int64, error) {
	return s.repo.MarkRead(ctx, userID, id)
}

func (s *Service) MarkAllRead(ctx context.Context, userID int64) (int64, error) {
	return s.repo.MarkAllRead(ctx, userID)
}

// NotifyPaymentReceived tells the customer their payment landed. Fired from the
// payment.completed consumer, which previously only logged.
//
// Customer-side and terminal: a receipt is informational, so it is told once
// and never chased. The amount comes from the event payload rather than the
// booking total - a partial or advance payment is not the full amount.
func (s *Service) NotifyPaymentReceived(ctx context.Context, bookingID, paymentID string, amount float64) error {
	c, err := s.repo.LoadBookingContext(ctx, bookingID)
	if err != nil {
		return err
	}
	body := fmt.Sprintf("Payment of %.2f for your booking at %s has been received.\n\nBooking ref: %s",
		amount, c.Facility, shortRef(bookingID))
	return s.NotifyUser(ctx, Event{
		Type:      "payment.received",
		SubjectID: paymentID,
		UserID:    c.CustomerUserID,
		Subject:   "Payment Received",
		Body:      body,
	})
}

// NotifyUpcoming is the T-minus reminder before a stay or event. Enqueued by
// the reminder sweep, not by an event: nothing happens at T-3 days for a hook
// to hang off.
//
// SubjectID is the booking plus the day count, so the 3-day and 1-day reminders
// are separate rows. Keyed on the booking alone the second would collide with
// the first on the outbox unique index and never send - the same bug the geo
// announcements hit.
func (s *Service) NotifyUpcoming(ctx context.Context, bookingID string, daysOut int) error {
	c, err := s.repo.LoadBookingContext(ctx, bookingID)
	if err != nil {
		return err
	}
	when := fmt.Sprintf("in %d days", daysOut)
	if daysOut == 1 {
		when = "tomorrow"
	}
	body := fmt.Sprintf("Reminder: your booking at %s starts %s (%s).\n\nBooking ref: %s",
		c.Facility, when, c.StartDate.Format("02 Jan 2006"), shortRef(bookingID))
	return s.NotifyUser(ctx, Event{
		Type:      "booking.upcoming",
		SubjectID: fmt.Sprintf("%s:%d", bookingID, daysOut),
		UserID:    c.CustomerUserID,
		Subject:   "Upcoming Event Reminder",
		Body:      body,
	})
}

// NotifyReviewRequest asks for a review after the stay. Enqueued by the same
// sweep, looking the other way down the calendar.
//
// PUSH and EMAIL only: this is a nudge, not something worth an SMS bill per
// completed booking.
func (s *Service) NotifyReviewRequest(ctx context.Context, bookingID string) error {
	c, err := s.repo.LoadBookingContext(ctx, bookingID)
	if err != nil {
		return err
	}
	body := fmt.Sprintf("How was your experience? Please share your review for your recent stay at %s.",
		c.Facility)
	return s.NotifyUser(ctx, Event{
		Type:      "review.request",
		SubjectID: bookingID,
		UserID:    c.CustomerUserID,
		Subject:   "How was your experience?",
		Body:      body,
		Channels:  []notify.Channel{notify.Push, notify.Email},
	})
}

// shortRef is the booking id as a human quotes it over the phone. The full UUID
// is unreadable in an SMS and nobody types it back.
func shortRef(id string) string {
	if len(id) >= 8 {
		return strings.ToUpper(id[:8])
	}
	return id
}

// SweepReminders enqueues the date-driven notifications: upcoming bookings and
// review requests for finished ones.
//
// Idempotency is the outbox unique index, not a "reminded" column: running this
// sweep every hour must not send an hourly reminder, and ON CONFLICT DO NOTHING
// already gives that for free.
func (s *Service) SweepReminders(ctx context.Context) (int, error) {
	sent := 0
	for _, days := range []int{3, 1} {
		ids, err := s.repo.BookingsStartingIn(ctx, days)
		if err != nil {
			return sent, err
		}
		for _, id := range ids {
			if err := s.NotifyUpcoming(ctx, id, days); err != nil {
				// One unreachable booking must not stop the sweep: the next
				// booking in the list is someone else's reminder.
				logger.Error("notify: upcoming", "bookingId", id, logger.Err(err))
				continue
			}
			sent++
		}
	}
	ids, err := s.repo.BookingsAwaitingReview(ctx)
	if err != nil {
		return sent, err
	}
	for _, id := range ids {
		if err := s.NotifyReviewRequest(ctx, id); err != nil {
			logger.Error("notify: review request", "bookingId", id, logger.Err(err))
			continue
		}
		sent++
	}
	return sent, nil
}
