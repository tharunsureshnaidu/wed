package storage

import (
	"context"
	"os"

	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
)

// New picks the backend: S3 when AWS_S3_BUCKET is set, local disk otherwise.
//
// A configured bucket that fails to initialise is fatal rather than a silent
// fallback to local disk - a production deployment quietly writing media to a
// container filesystem loses every upload on the next redeploy, and would look
// healthy while doing it.
func New(ctx context.Context, uploadDir, baseURL string) (Store, error) {
	bucket := os.Getenv("AWS_S3_BUCKET")
	if bucket == "" {
		s := NewLocal(uploadDir, baseURL)
		if os.Getenv("APP_ENV") == "production" {
			logger.Warn("media is being written to local disk and will be lost on redeploy - set AWS_S3_BUCKET",
				logger.Component, "storage")
		}
		return s, nil
	}
	s, err := NewS3(ctx, bucket)
	if err != nil {
		return nil, err
	}
	logger.Info("media storage ready", logger.Component, "storage", "backend", s.Name())
	return s, nil
}
