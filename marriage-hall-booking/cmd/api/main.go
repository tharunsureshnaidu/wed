package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	adminhandler "github.com/tripfcatory/marriage-hall-booking/internal/admin/handler"
	authhandler "github.com/tripfcatory/marriage-hall-booking/internal/auth/handler"
	authrepo "github.com/tripfcatory/marriage-hall-booking/internal/auth/repository"
	authservice "github.com/tripfcatory/marriage-hall-booking/internal/auth/service"
	bookinghandler "github.com/tripfcatory/marriage-hall-booking/internal/booking/handler"
	bookingrepo "github.com/tripfcatory/marriage-hall-booking/internal/booking/repository"
	bookingservice "github.com/tripfcatory/marriage-hall-booking/internal/booking/service"
	facilityhandler "github.com/tripfcatory/marriage-hall-booking/internal/facility/handler"
	facilityrepo "github.com/tripfcatory/marriage-hall-booking/internal/facility/repository"
	"github.com/tripfcatory/marriage-hall-booking/internal/health"
	"github.com/tripfcatory/marriage-hall-booking/internal/migrations"
	paymenthandler "github.com/tripfcatory/marriage-hall-booking/internal/payment/handler"
	paymentservice "github.com/tripfcatory/marriage-hall-booking/internal/payment/service"
	quotehandler "github.com/tripfcatory/marriage-hall-booking/internal/quote/handler"
	reviewhandler "github.com/tripfcatory/marriage-hall-booking/internal/review/handler"
	searchhandler "github.com/tripfcatory/marriage-hall-booking/internal/search/handler"
	userhandler "github.com/tripfcatory/marriage-hall-booking/internal/user/handler"
	userrepo "github.com/tripfcatory/marriage-hall-booking/internal/user/repository"
	vendorhandler "github.com/tripfcatory/marriage-hall-booking/internal/vendors/handler"
	"github.com/tripfcatory/marriage-hall-booking/pkg/config"
	"github.com/tripfcatory/marriage-hall-booking/pkg/database"
	"github.com/tripfcatory/marriage-hall-booking/pkg/events"
	"github.com/tripfcatory/marriage-hall-booking/pkg/jwt"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
	"github.com/tripfcatory/marriage-hall-booking/pkg/middleware"
	"github.com/tripfcatory/marriage-hall-booking/pkg/storage"
)

func main() {
	logger.Init("api")
	cfg := config.Load()
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
	fh.Register(mux)
	fh.RegisterInventory(mux)
	fh.RegisterMedia(mux)
	fh.RegisterCancellation(mux)
	// Serves files uploaded with a facility (see internal/facility/handler/upload.go).
	facilityhandler.ServeUploads(mux)
	bookinghandler.New(bookingSvc, signer).Register(mux)
	paymenthandler.New(paymentSvc, signer).Register(mux)
	vendorhandler.New(db, signer).Register(mux)
	quotehandler.New(db, signer, bookingSvc).Register(mux)
	reviews := reviewhandler.New(db, signer)
	reviews.Register(mux)
	reviews.RegisterAdmin(mux)
	searchhandler.New(db, rdb, signer).Register(mux)
	adminhandler.New(db, signer).Register(mux)

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
