package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/events"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/storage"
)

// consumeMediaUploads uploads spooled files to object storage and fills in the
// media row that was created PENDING.
//
// Its own consumer rather than a case in handle(): this one needs a database
// pool and a storage backend, and a slow S3 PUT here must not hold up
// notification events on other topics.
func consumeMediaUploads(ctx context.Context, brokers []string, db *pgxpool.Pool, store storage.Store) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   events.TopicMediaUploadRequested,
		GroupID: "media-upload",
		// A venue photo is a few hundred KB and every message is one upload,
		// so there is nothing to gain from batching reads.
		MaxWait: time.Second,
		// Topic may not exist yet on a fresh broker; see the note in consume().
		WatchPartitionChanges: true,
	})
	defer r.Close()

	failing := false
	for {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !failing {
				failing = true
				logger.Warn("kafka unreachable, retrying",
					"topic", events.TopicMediaUploadRequested, logger.Err(err))
			}
			time.Sleep(2 * time.Second)
			continue
		}
		if failing {
			failing = false
			logger.Info("kafka reconnected", "topic", events.TopicMediaUploadRequested)
		}

		var e events.Envelope
		if err := json.Unmarshal(m.Value, &e); err != nil {
			logger.Error("malformed media event", logger.Err(err))
			continue // a message that cannot be parsed will never parse; drop it
		}
		var up events.MediaUpload
		raw, _ := json.Marshal(e.Payload)
		if err := json.Unmarshal(raw, &up); err != nil {
			logger.Error("malformed media payload", logger.Err(err))
			continue
		}
		uploadOne(ctx, db, store, up)
	}
}

// uploadOne performs the upload and records the outcome on the row.
func uploadOne(ctx context.Context, db *pgxpool.Pool, store storage.Store, up events.MediaUpload) {
	start := time.Now()

	f, err := os.Open(up.SpoolPath)
	if err != nil {
		// The spool file is gone: either this event was already processed and
		// cleaned up, or the file was lost. Either way retrying cannot help.
		markFailed(ctx, db, up, "spooled file missing")
		logger.Error("spool file missing", "mediaId", up.MediaID,
			"path", up.SpoolPath, logger.Err(err))
		return
	}
	defer f.Close()

	res, err := store.Put(ctx, storage.Upload{
		Kind: storage.Kind(up.Kind), Body: f, Size: up.Size,
		ContentType: up.ContentType, Ext: up.Ext,
		FacilityID: up.FacilityID, VendorID: up.VendorID,
	})
	if err != nil {
		// Left PENDING with the reason recorded, and the spool file kept, so a
		// replay can retry it. Only a missing file is terminal.
		markFailed(ctx, db, up, err.Error())
		logger.Error("media upload failed", "mediaId", up.MediaID, logger.Err(err))
		return
	}

	// Table names come from this codebase, never from the message: an attacker
	// who could reach the topic would otherwise choose the table to write to.
	table, ok := map[string]string{
		"facility_images": "facility_images",
		"facility_videos": "facility_videos",
	}[up.Table]
	if !ok {
		logger.Error("unknown media table", "table", up.Table, "mediaId", up.MediaID)
		return
	}

	if _, err := db.Exec(ctx,
		`UPDATE `+table+` SET url = $2, status = 'READY', error_message = NULL
		  WHERE id = $1`, up.MediaID, res.URL); err != nil {
		// The object is in the bucket but the row still says PENDING. Leave the
		// spool file so a replay can redo the update; a second upload just
		// overwrites the same key.
		logger.Error("media row update failed", "mediaId", up.MediaID, logger.Err(err))
		return
	}

	if err := storage.Unspool(up.SpoolPath); err != nil {
		logger.Warn("spool cleanup failed", "path", up.SpoolPath, logger.Err(err))
	}
	logger.Info("media uploaded", "mediaId", up.MediaID, "bytes", up.Size,
		logger.Dur(time.Since(start)))
}

func markFailed(ctx context.Context, db *pgxpool.Pool, up events.MediaUpload, reason string) {
	table, ok := map[string]string{
		"facility_images": "facility_images",
		"facility_videos": "facility_videos",
	}[up.Table]
	if !ok {
		return
	}
	if _, err := db.Exec(ctx,
		`UPDATE `+table+` SET status = 'FAILED', error_message = $2 WHERE id = $1`,
		up.MediaID, reason); err != nil {
		logger.Error("could not mark media failed", "mediaId", up.MediaID, logger.Err(err))
	}
}
