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

	"github.com/jackc/pgx/v5/pgxpool"
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
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/migrations"
	notifyhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/handler"
	notifyrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/repository"
	notifysvc "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/service"
	paymenthandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/payment/handler"
	paymentservice "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/payment/service"
	quotehandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/quote/handler"
	reviewhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/review/handler"
	searchhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/search/handler"
	supporthandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/support/handler"
	userhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/user/handler"
	userrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/user/repository"
	vendorhandler "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/vendors/handler"
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
	tokenSvc := authservice.NewTokenService(authRepo, signer, cfg.JWTRefreshExpiry, revoker)
	authSvc := authservice.NewAuthService(authRepo, otpSvc, tokenSvc, cfg.ResetPasswordURL)
	authSvc.OnUserCreated = func(ctx context.Context, userID int64, first string, last *string) error {
		if err := profiles.EnsureProfile(ctx, userID, first, last); err != nil {
			return err
		}
		publisher.Publish(ctx, events.TopicUserRegistered, strconv.FormatInt(userID, 10),
			map[string]any{"userId": userID, "firstName": first})
		return nil
	}

	bookingSvc := bookingservice.New(bookings, db)
	bookingSvc.OnBookingCreated = func(ctx context.Context, b *bookingrepo.Booking) {
		publisher.Publish(ctx, events.TopicBookingCreated, b.ID, map[string]any{
			"bookingId": b.ID, "userId": b.UserID, "targetId": b.TargetID,
			"totalAmount": b.TotalAmount,
		})
	}
	bookingSvc.OnBookingCancelled = func(ctx context.Context, b *bookingrepo.Booking) {
		publisher.Publish(ctx, events.TopicBookingCancelled, b.ID, map[string]any{
			"bookingId": b.ID, "userId": b.UserID,
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
			return publisher.PublishSync(ctx, events.TopicMediaUploadRequested, m.MediaID,
				map[string]any{
					"mediaId": m.MediaID, "table": m.Table,
					"facilityId": m.FacilityID, "vendorId": m.VendorID,
					"spoolPath": m.SpoolPath, "contentType": m.ContentType,
					"ext": m.Ext, "size": m.Size, "kind": m.Kind,
				})
		}
	}
	fh.Register(mux)
	fh.RegisterInventory(mux)
	fh.RegisterMedia(mux)
	fh.RegisterCancellation(mux)
	// Serves files uploaded with a facility (see internal/facility/handler/upload.go).
	facilityhandler.ServeUploads(mux)
	bookinghandler.New(bookingSvc, signer).Register(mux)
	// Sending happens in the worker; the API only enqueues, so this service
	// has no senders wired. The trigger hooks below share it.
	notifier := notifysvc.New(notifyrepo.New(db), nil)
	notifyhandler.New(notifier, signer).Register(mux)
	paymenthandler.New(paymentSvc, signer).Register(mux)
	vendorhandler.New(db, signer).Register(mux)
	quotehandler.New(db, signer, bookingSvc).Register(mux)

	coupons := couponhandler.New(db, signer)
	coupons.OnCouponCreated = func(ctx context.Context, couponID, code, facilityID string, createdBy int64) {
		notifier.AnnounceFacilityNearby(ctx, facilityID, "has a new offer: "+code, "coupon:"+couponID)
	}
	coupons.Register(mux)

	reviews := reviewhandler.New(db, signer)
	reviews.OnReviewCreated = func(ctx context.Context, facilityID string, rating int) {
		notifyFacilityOwner(ctx, db, notifier, facilityID, rating)
	}
	reviews.Register(mux)
	reviews.RegisterAdmin(mux)
	searchhandler.New(db, rdb, signer).Register(mux)
	supporthandler.New(db, signer).Register(mux)

	// App feedback. The ops notification is a hook so this package never
	// imports notification, and it fires after the insert commits.
	feedback := feedbackhandler.New(db, signer, media)
	feedback.OnSubmitted = func(ctx context.Context, id, message string, rating *int) {
		subject := "[ops] New app feedback"
		if rating != nil {
			subject = fmt.Sprintf("[ops] New app feedback (%d/5)", *rating)
		}
		if err := notifier.NotifyAdmins(ctx, notifysvc.Event{
			Type: "feedback.submitted", SubjectID: id,
			Subject: subject, Body: message,
		}); err != nil {
			logger.Error("feedback: notify ops", "id", id, logger.Err(err))
		}
	}
	feedback.Register(mux)

	admins := adminhandler.New(db, signer)
	admins.OnStatusChange = func(ctx context.Context, ev adminhandler.StatusChange) {
		onAdminStatusChange(ctx, notifier, ev)
	}
	admins.Register(mux)
	admins.RegisterFacilityAdmin(mux)

	// New amenities at an existing venue: told to nearby customers, not to
	// admins - it needs no approval.
	fh.OnAmenitiesAdded = func(ctx context.Context, facilityID string, names []string) {
		if len(names) == 0 {
			return
		}
		notifier.AnnounceFacilityNearby(ctx, facilityID,
			"now offers "+strings.Join(names, ", "),
			"amenities:"+strings.Join(names, ","))
	}

	fh.OnFacilityCreated = func(ctx context.Context, facilityID, name string, ownerID int64) {
		if err := notifier.NotifyAdmins(ctx, notifysvc.Event{
			Type:      "facility.created",
			SubjectID: facilityID,
			Subject:   "[ops] New listing awaiting approval: " + name,
			Body: fmt.Sprintf("A new facility has been submitted and is waiting for approval.\n\n%s\nFacility ref: %s\n\nIt stays invisible to customers until approved.",
				name, facilityID),
		}); err != nil {
			logger.Error("notify: facility created", "facilityId", facilityID, logger.Err(err))
		}
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

// onAdminStatusChange turns one admin decision into a notification the
// affected user can act on. Wording matters more than usual here: these are
// the messages that tell someone their livelihood listing was rejected.
func onAdminStatusChange(ctx context.Context, n *notifysvc.Service, ev adminhandler.StatusChange) {
	var subject, body string
	// A blocked user's sessions are revoked as part of the block, so push and
	// anything in-app is unreadable by the time it arrives. Email and SMS are
	// the only channels that still reach them.
	var channels []notify.Channel

	switch ev.Entity {
	case "vendor.kyc":
		if ev.Status == "APPROVED" {
			subject = "Your KYC has been approved"
			body = "Good news - your KYC verification for " + ev.Name + " has been approved.\n\nYou can now publish listings and accept bookings."
		} else {
			subject = "Your KYC needs attention"
			body = "Your KYC verification for " + ev.Name + " was not approved."
			if ev.Reason != "" {
				body += "\n\nReason: " + ev.Reason
			}
			body += "\n\nYou can correct the details and submit again."
		}
	case "facility":
		switch ev.Status {
		case "APPROVED":
			subject = "Your listing is live: " + ev.Name
			body = ev.Name + " has been approved and is now visible to customers."
			// Announced on approval rather than on creation: a PENDING venue is
			// invisible to customers, so telling them about it sends them to a
			// listing they cannot open.
			n.AnnounceFacilityNearby(ctx, ev.EntityID, "is a new venue near you", "approved")
		case "REJECTED":
			subject = "Your listing was not approved: " + ev.Name
			body = ev.Name + " was not approved."
			if ev.Reason != "" {
				body += "\n\nReason: " + ev.Reason
			}
		case "BLOCKED":
			subject = "Your listing has been suspended: " + ev.Name
			body = ev.Name + " has been suspended and is no longer visible to customers."
		default:
			// PENDING and anything added later: no message worth sending.
			return
		}
	case "user":
		if ev.Status == "SUSPENDED" {
			subject = "Your account has been suspended"
			body = "Your account has been suspended and you have been signed out."
			if ev.Reason != "" {
				body += "\n\nReason: " + ev.Reason
			}
			body += "\n\nContact support if you believe this is a mistake."
			channels = []notify.Channel{notify.Email, notify.SMS}
		} else {
			subject = "Your account has been reactivated"
			body = "Your account is active again. You can sign in as usual."
		}
	default:
		return
	}

	if err := n.NotifyUser(ctx, notifysvc.Event{
		Type:      ev.Entity + "." + strings.ToLower(ev.Status),
		SubjectID: ev.EntityID,
		UserID:    ev.UserID,
		Subject:   subject,
		Body:      body,
		Channels:  channels,
	}); err != nil {
		logger.Error("notify: admin status change",
			"entity", ev.Entity, "entityId", ev.EntityID, logger.Err(err))
	}
}

// notifyFacilityOwner tells a venue owner they have a new review. The owner is
// looked up here rather than passed in: the review handler has no reason to
// know who owns the facility.
func notifyFacilityOwner(ctx context.Context, db *pgxpool.Pool, n *notifysvc.Service, facilityID string, rating int) {
	var ownerID int64
	var name string
	if err := db.QueryRow(ctx,
		`SELECT owner_id, name FROM facilities WHERE id = $1 AND is_deleted = FALSE`,
		facilityID).Scan(&ownerID, &name); err != nil {
		logger.Error("notify: review owner lookup", "facilityId", facilityID, logger.Err(err))
		return
	}
	// The review id would be the natural subject, but the handler does not
	// return it; facility+rating is enough to dedupe a redelivery.
	if err := n.NotifyUser(ctx, notifysvc.Event{
		Type:      "review.created",
		SubjectID: facilityID + ":" + strconv.Itoa(rating),
		UserID:    ownerID,
		Subject:   fmt.Sprintf("New %d-star review for %s", rating, name),
		Body: fmt.Sprintf("%s received a new %d-star review.\n\nOpen the app to read it and reply.",
			name, rating),
	}); err != nil {
		logger.Error("notify: review created", "facilityId", facilityID, logger.Err(err))
	}
}
