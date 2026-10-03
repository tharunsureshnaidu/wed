package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/events"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/storage"
)

// mediaTables are the only tables a media event may write to. Table names come
// from this codebase, never from the message: an attacker who could reach the
// topic would otherwise choose the table to write to.
var mediaTables = map[string]string{
	"facility_images": "facility_images",
	"facility_videos": "facility_videos",
}

// mediaUploader uploads a spooled file to object storage and fills in the media
// row that was created PENDING.
//
// A failed upload is returned as a retryable error with the spool file kept, so
// the consume loop's retries - and a replay from the dead-letter topic - can
// still finish it. The row reads FAILED with the reason between attempts, and
// READY once one succeeds.
func mediaUploader(db *pgxpool.Pool, store storage.Store) events.Handler {
	return func(ctx context.Context, e events.Envelope) error {
		var up events.MediaUpload
		if err := e.Decode(&up); err != nil {
			return err
		}
		table, ok := mediaTables[up.Table]
		if !ok {
			return fmt.Errorf("%w: unknown media table %q", events.ErrPermanent, up.Table)
		}
		// The path arrives in a message, and the broker has no authentication:
		// without this a forged event could name any file the worker can read -
		// .env included - and have it uploaded to public storage.
		if !storage.InSpool(up.SpoolPath) {
			markFailed(ctx, db, table, up.MediaID, "spool path rejected")
			return fmt.Errorf("%w: spool path outside spool dir: %q", events.ErrPermanent, up.SpoolPath)
		}
		start := time.Now()

		f, err := os.Open(up.SpoolPath)
		if err != nil {
			// The spool file is gone: either this event was already processed
			// and cleaned up, or the file was lost. Retrying cannot help.
			markFailed(ctx, db, table, up.MediaID, "spooled file missing")
			logger.Error("spool file missing", "mediaId", up.MediaID,
				"path", up.SpoolPath, logger.Err(err))
			return nil
		}
		defer f.Close()

		res, err := store.Put(ctx, storage.Upload{
			Kind: storage.Kind(up.Kind), Body: f, Size: up.Size,
			ContentType: up.ContentType, Ext: up.Ext,
			FacilityID: up.FacilityID, VendorID: up.VendorID,
		})
		if err != nil {
			markFailed(ctx, db, table, up.MediaID, err.Error())
			return fmt.Errorf("media upload %s: %w", up.MediaID, err)
		}

		if _, err := db.Exec(ctx,
			`UPDATE `+table+` SET url = $2, status = 'READY', error_message = NULL
			  WHERE id = $1`, up.MediaID, res.URL); err != nil {
			// The object is in the bucket but the row still says PENDING. The
			// spool file stays, so the retry redoes both; a second upload just
			// overwrites the same key.
			return fmt.Errorf("media row update %s: %w", up.MediaID, err)
		}

		if err := storage.Unspool(up.SpoolPath); err != nil {
			logger.Warn("spool cleanup failed", "path", up.SpoolPath, logger.Err(err))
		}
		logger.Info("media uploaded", "mediaId", up.MediaID, "bytes", up.Size,
			logger.Dur(time.Since(start)))
		return nil
	}
}

func markFailed(ctx context.Context, db *pgxpool.Pool, table, mediaID, reason string) {
	if _, err := db.Exec(ctx,
		`UPDATE `+table+` SET status = 'FAILED', error_message = $2 WHERE id = $1`,
		mediaID, reason); err != nil {
		logger.Error("could not mark media failed", "mediaId", mediaID, logger.Err(err))
	}
}
