package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	adminhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/admin/handler"
	authhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/handler"
	authrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/repository"
	authservice "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/auth/service"
	bookinghandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/handler"
	bookingrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/repository"
	bookingservice "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/service"
	couponhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/coupon/handler"
	facilityhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/handler"
	facilityrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/facility/repository"
	feedbackhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/feedback/handler"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/health"
	helpdomain "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/domain"
	helphandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/help/handler"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/migrations"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/consumer"
	notifyhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/handler"
	notifyrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/repository"
	notifysvc "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/service"
	paymenthandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/payment/handler"
	paymentservice "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/payment/service"
	privacypolicyhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/handler"
	privacypolicyrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/repository"
	privacypolicysvc "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/privacypolicy/service"
	quotehandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/quote/handler"
	recommendationhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/handler"
	recommendationrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/repository"
	recommendationsvc "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/recommendation/service"
	reviewhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/review/handler"
	searchhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/search/handler"
	supporthandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/support/handler"
	userhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/user/handler"
	userrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/user/repository"
	vendorhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/vendors/handler"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/audit"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/config"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/events"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/jwt"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/middleware"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/notify"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/storage"
)

func main() {
	logger.Init("api")
	cfg := config.Load()

	// Refuse to serve real users on a development configuration. Each of these
	// is silent at startup and only visible once a customer hits it - a dead
	// acknowledge link, an unauthenticated payment webhook, an OTP that is
	// always 000000. In development they are warnings so a laptop still starts.
	if problems := cfg.Validate(); len(problems) > 0 {
		if config.IsProduction() {
			for _, p := range problems {
				logger.Error("config", "problem", p)
			}
			logger.Fatal("refusing to start in production with an unsafe configuration",
				"problems", len(problems))
		}
		for _, p := range problems {
			logger.Warn("config (dev)", "problem", p)
		}
	}
	ctx := context.Background()

	if cfg.JWTSecret == "" {
		logger.Fatal("JWT_SECRET is required", "hint", "openssl rand -base64 48")
	}
	signer, err := jwt.NewSigner(cfg.JWTSecret, cfg.JWTExpiry)
	if err != nil {
		logger.Fatal("jwt", logger.Err(err))
	}

	db, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("database", logger.Err(err))
	}
	defer db.Close()

	if err := database.Migrate(ctx, db, migrations.FS, "."); err != nil {
		logger.Fatal("migrate", logger.Err(err))
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		// Not fatal: the rate limiter fails open, so the API still serves.
		logger.Warn("redis unavailable, rate limiting disabled", "addr", cfg.RedisAddr, logger.Err(err))
	}
	defer rdb.Close()

	publisher := events.NewPublisher(cfg.KafkaBrokers)
	defer publisher.Close()

	// --- wiring ---
	authRepo := authrepo.New(db)
	profiles := userrepo.New(db)
	facilities := facilityrepo.New(db)
	bookings := bookingrepo.New(db)

	// Logout has to end access tokens too, not just refresh tokens - see
	// pkg/middleware/revoke.go. Installed globally because every module's
	// RequireAuth must apply it.
	revoker := middleware.NewRevoker(rdb, cfg.JWTExpiry)
	middleware.SetRevoker(revoker)

	otpSvc := authservice.NewOtpService(authRepo, cfg.LogOtpCodes)
	// Codes and reset links go straight to the provider, by email when the
	// target is an address and by SMS otherwise. A sender with no credentials
	// is skipped rather than called: its log-only fallback prints the body,
	// and the code is logged only under LOG_OTP_CODES.
	otpSenders := notify.FromEnv(nil)
	otpSvc.Send = func(ctx context.Context, target, subject, body string) error {
		s := otpSenders[notify.SMS]
		if strings.Contains(target, "@") {
			s = otpSenders[notify.Email]
		}
		if !s.Live() {
			return nil
		}
		return s.Send(ctx, notify.Message{To: target, Subject: subject, Body: body})
	}
	tokenSvc := authservice.NewTokenService(authRepo, signer, cfg.JWTRefreshExpiry, revoker)
	authSvc := authservice.NewAuthService(authRepo, otpSvc, tokenSvc, cfg.ResetPasswordURL)
	authSvc.OnUserCreated = func(ctx context.Context, userID int64, first string, last *string, addr *authservice.Address) error {
		if err := profiles.EnsureProfile(ctx, userID, first, last); err != nil {
			return err
		}
		if addr != nil {
			if err := profiles.AddAddress(ctx, userID, userrepo.Address{
				Street: addr.Street, City: addr.City, State: addr.State,
				ZipCode: addr.ZipCode, Country: addr.Country,
			}); err != nil {
				return err
			}
		}
		publisher.Publish(ctx, events.TopicUserRegistered, strconv.FormatInt(userID, 10),
			map[string]any{"userId": userID, "firstName": first})
		return nil
	}
	// A vendor signup creates the vendors row immediately. Wired here rather
	// than imported so auth stays independent of the vendors module, matching
	// OnUserCreated above.
	//
	// ON CONFLICT DO NOTHING because user_id is unique: a retried registration
	// must not fail on a row that already exists.
	authSvc.OnVendorCreated = func(ctx context.Context, userID int64, businessName string, businessAddress *string) error {
		_, err := db.Exec(ctx,
			`INSERT INTO vendors (user_id, business_name, business_address)
			 VALUES ($1, $2, $3) ON CONFLICT (user_id) DO NOTHING`, userID, businessName, businessAddress)
		return err
	}

	bookingSvc := bookingservice.New(bookings, db)
	bookingSvc.OnBookingCreated = func(ctx context.Context, b *bookingrepo.Booking) {
		publisher.Publish(ctx, events.TopicBookingCreated, b.ID, map[string]any{
			"bookingId": b.ID, "userId": b.UserID, "targetId": b.TargetID,
			"totalAmount": b.TotalAmount,
		})
	}
	bookingSvc.OnBookingCancelled = func(ctx context.Context, b *bookingrepo.Booking, actorID int64) {
		publisher.Publish(ctx, events.TopicBookingCancelled, b.ID, map[string]any{
			"bookingId": b.ID, "userId": b.UserID,
		})
		// The occasion rides on the audit row, so the rejection analytics can
		// break down by event without joining back to a booking that may since
		// have been deleted.
		eventType := ""
		if b.EventType != nil {
			eventType = *b.EventType
		}
		audit.Record(ctx, db, audit.Decision{
			Actor: actorID, Action: "CANCEL_BOOKING",
			Entity: audit.EntityBooking, EntityID: b.ID,
			Status: "CANCELLED", EventType: eventType,
			Extra: map[string]any{"userId": b.UserID},
		})
	}
	paymentSvc := paymentservice.New(db, bookings, cfg.WebhookSecret)
	paymentSvc.OnPaid = func(ctx context.Context, bookingID, paymentID string, amount float64) {
		publisher.Publish(ctx, events.TopicPaymentCompleted, bookingID,
			map[string]any{"bookingId": bookingID, "paymentId": paymentID, "amount": amount})
	}
	paymentSvc.OnFailed = func(ctx context.Context, bookingID, paymentID, reason string) {
		publisher.Publish(ctx, events.TopicPaymentFailed, bookingID,
			map[string]any{"bookingId": bookingID, "paymentId": paymentID, "reason": reason})
	}

	mux := http.NewServeMux()
	health.NewHandler(db).Register(mux)
	authhandler.New(authSvc, signer).Register(mux)
	userhandler.New(profiles, signer).Register(mux)
	// S3 when AWS_S3_BUCKET is set, local disk otherwise. A configured bucket
	// that fails to initialise stops start-up rather than silently falling back
	// to a container filesystem that loses every upload on redeploy.
	media, err := storage.New(ctx, "uploads", os.Getenv("PUBLIC_BASE_URL"))
	if err != nil {
		logger.Fatal("media storage unavailable", logger.Err(err))
	}

	fh := facilityhandler.New(facilities, signer, media)
	// With Kafka configured, media uploads are queued for the worker instead of
	// running on the request - the S3 PUT is seconds of network the caller has
	// no reason to wait for. Without it the handler uploads inline.
	if publisher.Enabled {
		fh.OnMediaUpload = func(ctx context.Context, m events.MediaUpload) error {
			return publisher.PublishSync(ctx, events.TopicMediaUploadRequested, m.MediaID, m)
		}
	}
	fh.Register(mux)
	fh.RegisterInventory(mux)
	fh.RegisterMedia(mux)
	fh.RegisterCancellation(mux)
	// Serves files uploaded with a facility (see internal/facility/handler/upload.go).
	facilityhandler.ServeUploads(mux)
	bookingHandler := bookinghandler.New(bookingSvc, signer)
	// Assigned rather than constructed inline so the event check can be wired;
	// the facilities repo already exists here, and booking never imports facility.
	bookingHandler.HostsEvent = facilities.HostsEvent
	bookingHandler.Register(mux)
	// Sending happens in the worker; the API only enqueues, so this service
	// has no senders wired. The feed/device/ack endpoints use it, and so does
	// the publisher's in-process fallback below.
	notifier := notifysvc.New(notifyrepo.New(db), nil)

	// The customer is waiting on the owner's answer, so it goes straight to
	// them. NotifyUser rather than a new topic: this needs no fan-out and no
	// retry-until-acknowledged, and a topic would have to be added to both
	// events.Topics and consumer.Topics to avoid being published and dropped.
	bookingSvc.OnBookingDecided = func(ctx context.Context, b *bookingrepo.Booking, confirmed bool, reason string, actorID int64) {
		// Deciding is the owner's answer, so stop chasing them (and ops) about
		// the request; otherwise the retries ran to max attempts regardless.
		if _, err := notifier.AckDecided(ctx, b.ID); err != nil {
			logger.Error("ack decided booking", "bookingId", b.ID, logger.Err(err))
		}
		subject, body := "Your booking is confirmed", "The venue has confirmed your booking."
		eventType := "booking.confirmed"
		if !confirmed {
			subject, body = "Your booking was not accepted",
				"The venue could not accept this booking."
			eventType = "booking.rejected"
			if reason != "" {
				body += " Reason: " + reason
			}
		}
		audit.Record(ctx, db, audit.Decision{
			Actor: actorID, Action: "DECIDE_BOOKING", Entity: audit.EntityBooking, EntityID: b.ID,
			Status: b.Status, Reason: reason,
			EventType: derefString(b.EventType),
		})
		if err := notifier.NotifyUser(ctx, notifysvc.Event{
			Type: eventType, SubjectID: b.ID, UserID: b.UserID,
			Subject: subject, Body: body,
		}); err != nil {
			logger.Error("notify booking decision", "bookingId", b.ID, logger.Err(err))
		}
	}

	// Every hook below only publishes; the worker reacts. When Kafka is off or
	// refuses the write, the same reactions run here instead - see
	// events.Publisher.Local.
	publisher.Local = consumer.New(notifier, db).Handle
	notifyhandler.New(notifier, signer).Register(mux)
	paymenthandler.New(paymentSvc, signer).Register(mux)
	vendorhandler.New(db, signer).Register(mux)
	quotehandler.New(db, signer, bookingSvc).Register(mux)

	coupons := couponhandler.New(db, signer)
	coupons.OnCouponCreated = func(ctx context.Context, c couponhandler.Created) {
		publisher.Publish(ctx, events.TopicCouponCreated, c.ID, events.CouponCreated{
			CouponID: c.ID, Code: c.Code, FacilityID: c.FacilityID,
			DiscountType: c.DiscountType, DiscountValue: c.DiscountValue,
			MaxDiscount: c.MaxDiscount, CreatedBy: c.CreatedBy,
		})
	}
	coupons.Register(mux)

	reviews := reviewhandler.New(db, signer)
	reviews.OnReviewCreated = func(ctx context.Context, reviewID, facilityID string, rating int) {
		publisher.Publish(ctx, events.TopicReviewCreated, facilityID, events.ReviewCreated{
			ReviewID: reviewID, FacilityID: facilityID, Rating: rating,
		})
	}
	reviews.Register(mux)
	reviews.RegisterAdmin(mux)
	searchhandler.New(db, rdb, signer).Register(mux)
	supporthandler.New(db, signer).Register(mux)
	recommendationhandler.New(recommendationsvc.New(recommendationrepo.New(db))).Register(mux)

	// App feedback. The ops notification is a hook so this package never
	// imports notification, and it fires after the insert commits.
	feedback := feedbackhandler.New(db, signer, media)
	feedback.OnSubmitted = func(ctx context.Context, id, message string, rating *int) {
		publisher.Publish(ctx, events.TopicFeedbackSubmitted, id, events.FeedbackSubmitted{
			FeedbackID: id, Message: message, Rating: rating,
		})
	}
	feedback.Register(mux)

	// Help Center customer support messages. Super admins receive notifications
	// when a customer submits a new message.
	helpCenter := helphandler.New(db, signer)
	helpCenter.OnMessageCreated = func(ctx context.Context, msg *helpdomain.HelpCenterMessage) {
		email := "N/A"
		if msg.UserEmail != nil && *msg.UserEmail != "" {
			email = *msg.UserEmail
		}
		phone := "N/A"
		if msg.UserPhone != nil && *msg.UserPhone != "" {
			phone = *msg.UserPhone
		}
		body := fmt.Sprintf("A new support message has been received from %s.\n\nEmail:\n%s\n\nPhone:\n%s\n\nMessage:\n%s",
			msg.UserName, email, phone, msg.Message)
		if err := notifier.NotifySuperAdmins(ctx, notifysvc.Event{
			Type:      "HELP_CENTER_MESSAGE",
			SubjectID: msg.ID,
			Subject:   "New Help Center Message",
			Body:      body,
		}); err != nil {
			logger.Error("help: notify super admin", "messageId", msg.ID, logger.Err(err))
		}
	}
	helpCenter.Register(mux)

	privacyRepo := privacypolicyrepo.New(db)
	privacySvc := privacypolicysvc.New(privacyRepo, db)
	privacypolicyhandler.New(privacySvc, signer).Register(mux)

	admins := adminhandler.New(db, signer)
	admins.OnStatusChange = func(ctx context.Context, ev adminhandler.StatusChange) {
		publisher.Publish(ctx, events.TopicAdminStatusChanged, ev.EntityID, events.AdminStatusChanged{
			Entity: ev.Entity, EntityID: ev.EntityID, UserID: ev.UserID,
			Status: ev.Status, Reason: ev.Reason, Name: ev.Name,
		})
	}
	admins.Register(mux)
	admins.RegisterFacilityAdmin(mux)

	fh.OnAmenitiesAdded = func(ctx context.Context, facilityID string, names []string) {
		publisher.Publish(ctx, events.TopicAmenitiesAdded, facilityID, events.AmenitiesAdded{
			FacilityID: facilityID, Names: names,
		})
	}
	fh.OnFacilityCreated = func(ctx context.Context, facilityID, name string, ownerID int64) {
		publisher.Publish(ctx, events.TopicFacilityCreated, facilityID, events.FacilityCreated{
			FacilityID: facilityID, Name: name, OwnerID: ownerID,
		})
	}

	handler := middleware.Chain(mux,
		middleware.Recover,
		middleware.RequestID,
		middleware.RequestLog,
		middleware.SecurityHeaders,
		middleware.CORS(cfg.CORSOrigins),
		middleware.RateLimit(rdb),
	)

	srv := &http.Server{
		Addr:              ":" + cfg.ServerPort,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("api listening", "port", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("listen", logger.Err(err))
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", logger.Err(err))
	}
	logger.Info("stopped")
}

// derefString is "" for a nil pointer, for optional columns going into a log
// or an audit payload.
func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
