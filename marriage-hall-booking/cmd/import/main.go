// Command import loads scraped venue data into Postgres.
//
// It reads the two CSVs the Google Maps scraper exports alongside its workbook
// (leads and reviews), creates a vendor per venue operator, a facility per
// venue, copies each venue photo into the configured object store, and records
// the scraped Google reviews.
//
// Re-running it is safe: every venue is keyed by its Google Maps URL and every
// review by Google's own review id, so a second run updates what it created the
// first time instead of inserting duplicates.
//
// Usage:
//
//	import -leads leads_x_leads.csv [-reviews leads_x_reviews.csv] [-dry-run]
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/config"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/storage"
)

func main() {
	logger.Init("import")

	leadsPath := flag.String("leads", "", "path to the leads CSV (required)")
	reviewsPath := flag.String("reviews", "", "path to the reviews CSV")
	dryRun := flag.Bool("dry-run", false, "parse and report, write nothing")
	skipPhotos := flag.Bool("skip-photos", false, "do not download or upload venue photos")
	flag.Parse()

	if *leadsPath == "" {
		fmt.Fprintln(os.Stderr, "usage: import -leads <leads.csv> [-reviews <reviews.csv>] [-dry-run]")
		os.Exit(2)
	}

	leadRows, err := readCSV(*leadsPath)
	if err != nil {
		logger.Fatal("read leads csv", logger.Err(err))
	}
	halls := parseHalls(leadRows)

	var reviews []scrapedReview
	if *reviewsPath != "" {
		reviewRows, err := readCSV(*reviewsPath)
		if err != nil {
			logger.Fatal("read reviews csv", logger.Err(err))
		}
		reviews = parseReviews(reviewRows)
	}

	// Group reviews by venue up front: one pass here beats scanning the whole
	// slice once per hall.
	// Keyed by place id, not raw URL: the same venue's reviews can be written
	// under either URL shape, and a hall dropped as a duplicate would otherwise
	// take its reviews with it.
	reviewsByPlace := map[string][]scrapedReview{}
	for _, r := range reviews {
		id := placeID(r.MapsURL)
		reviewsByPlace[id] = append(reviewsByPlace[id], r)
	}

	vendors := map[string]bool{}
	photos := 0
	for _, h := range halls {
		vendors[vendorKey(h)] = true
		photos += len(h.PhotoURLs)
	}
	logger.Info("parsed",
		"halls", len(halls), "vendors", len(vendors),
		"photos", photos, "reviews", len(reviews))

	if *dryRun {
		for i, h := range halls {
			if i >= 5 {
				logger.Info("dry-run: showing first 5 only")
				break
			}
			logger.Info("hall", "name", h.Name, "city", h.City, "state", h.State,
				"zip", h.Zipcode, "phone", h.Phone, "photos", len(h.PhotoURLs),
				"reviews", len(reviewsByPlace[placeID(h.MapsURL)]), "vendor", vendorKey(h))
		}
		return
	}

	ctx := context.Background()
	cfg := config.Load()

	db, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("database", logger.Err(err))
	}
	defer db.Close()

	var store storage.Store
	if !*skipPhotos {
		store, err = storage.New(ctx, env("UPLOAD_DIR", "uploads"), env("UPLOAD_BASE_URL", ""))
		if err != nil {
			logger.Fatal("storage", logger.Err(err))
		}
		logger.Info("media storage", "backend", store.Name())
	}

	imp := &importer{db: db, store: store, ctx: ctx}
	start := time.Now()
	for i, h := range halls {
		if err := imp.hall(h, reviewsByPlace[placeID(h.MapsURL)]); err != nil {
			logger.Error("import hall", "name", h.Name, logger.Err(err))
			imp.failed++
			continue
		}
		if (i+1)%25 == 0 {
			logger.Info("progress", "done", i+1, "of", len(halls))
		}
	}
	logger.Info("import complete",
		"facilities", imp.facilities, "vendors", imp.vendors,
		"images", imp.images, "reviews", imp.reviews,
		"failed", imp.failed, logger.Dur(time.Since(start)))
}

type importer struct {
	db    *pgxpool.Pool
	store storage.Store
	ctx   context.Context

	// vendorCache maps a vendorKey to the vendor row it resolved to, so halls
	// sharing an operator do not each re-query for it.
	vendorCache map[string]vendorRef

	// mu guards the counters, which the photo workers increment concurrently.
	mu                                           sync.Mutex
	facilities, vendors, images, reviews, failed int
}

type vendorRef struct {
	vendorID string
	userID   int64
}

// hall imports one venue and everything hanging off it, in a single
// transaction: a venue that fails halfway leaves nothing behind.
func (im *importer) hall(h scrapedHall, revs []scrapedReview) error {
	tx, err := im.db.Begin(im.ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(im.ctx)

	v, err := im.vendor(tx, h)
	if err != nil {
		return fmt.Errorf("vendor: %w", err)
	}

	facilityID, err := im.facility(tx, h, v)
	if err != nil {
		return fmt.Errorf("facility: %w", err)
	}

	if err := im.reviewRows(tx, facilityID, revs); err != nil {
		return fmt.Errorf("reviews: %w", err)
	}

	if err := tx.Commit(im.ctx); err != nil {
		return err
	}
	im.facilities++

	// Photos are copied after the commit, not inside it: each one is a network
	// download plus an upload, and holding a transaction open across that would
	// pin a pool connection for the whole venue. A photo that fails leaves the
	// venue listed without it, which is recoverable by re-running.
	if im.store != nil {
		im.photos(facilityID, v.vendorID, h)
	}
	return nil
}

// vendor finds or creates the vendor (and its login user) for a venue.
func (im *importer) vendor(tx pgx.Tx, h scrapedHall) (vendorRef, error) {
	key := vendorKey(h)
	if im.vendorCache == nil {
		im.vendorCache = map[string]vendorRef{}
	}
	if v, ok := im.vendorCache[key]; ok {
		return v, nil
	}

	phone := normalisePhone(h.Phone)
	email := strings.ToLower(strings.TrimSpace(h.Email))

	// An earlier run, or a real signup on the same number, already owns this
	// vendor - reuse it rather than creating a second account for one operator.
	var v vendorRef
	err := tx.QueryRow(im.ctx, `
		SELECT ven.id::text, ven.user_id
		  FROM vendors ven
		  JOIN users u ON u.id = ven.user_id
		 WHERE ($1 <> '' AND u.phone_number = $1)
		    OR ($2 <> '' AND u.email = $2)
		 LIMIT 1`, phone, email).Scan(&v.vendorID, &v.userID)
	if err == nil {
		im.vendorCache[key] = v
		return v, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return vendorRef{}, err
	}

	// users.email is UNIQUE and NOT NULL-ish (one of email/phone is required),
	// so a venue with no scraped email gets a deliberately invalid placeholder.
	loginEmail := email
	if loginEmail == "" {
		loginEmail = placeholderEmail(key)
	}
	var phonePtr *string
	if phone != "" {
		phonePtr = &phone
	}

	// An imported account has no password anyone knows: a random one, hashed,
	// so the row satisfies NOT NULL without being a login anyone can use. The
	// operator claims the listing through password reset.
	hash, err := randomPasswordHash()
	if err != nil {
		return vendorRef{}, err
	}

	// INACTIVE and unverified: nobody has confirmed this address or number, and
	// an imported account must not be able to log in until someone claims it.
	err = tx.QueryRow(im.ctx, `
		INSERT INTO users (full_name, email, phone_number, password_hash, status,
		                   is_email_verified, is_phone_verified)
		VALUES ($1, $2, $3, $4, 'INACTIVE', FALSE, FALSE)
		ON CONFLICT (email) DO UPDATE SET updated_at = CURRENT_TIMESTAMP
		RETURNING id`, truncate(h.Name, 100), loginEmail, phonePtr, hash).Scan(&v.userID)
	if err != nil {
		return vendorRef{}, fmt.Errorf("user: %w", err)
	}

	// xmax = 0 identifies a genuinely inserted row, as opposed to one the
	// ON CONFLICT branch updated - otherwise a re-import reports every venue it
	// merely refreshed as a newly created vendor.
	var inserted bool
	err = tx.QueryRow(im.ctx, `
		INSERT INTO vendors (user_id, business_name, business_address,
		                     business_description, business_type, source)
		VALUES ($1, $2, $3, $4, 'MARRIAGE_HALL', 'GOOGLE_MAPS')
		ON CONFLICT (user_id) DO UPDATE SET business_name = EXCLUDED.business_name
		RETURNING id::text, (xmax = 0)`,
		v.userID, truncate(h.Name, 200), nullable(h.Address), nullable(h.Description)).
		Scan(&v.vendorID, &inserted)
	if err != nil {
		return vendorRef{}, err
	}

	// ROLE_HALL_OWNER, so the claimed account can manage its listings.
	if _, err := tx.Exec(im.ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, r.id FROM roles r WHERE r.role_name = 'ROLE_HALL_OWNER'
		ON CONFLICT DO NOTHING`, v.userID); err != nil {
		return vendorRef{}, err
	}

	if inserted {
		im.vendors++
	}
	im.vendorCache[key] = v
	return v, nil
}

// facility upserts the venue row, keyed on its Google Maps URL.
//
// uq_facility_owner_name forbids one owner having two venues with the same
// name, but a chain really does run several identically named branches under
// one phone number - so a repeat name is disambiguated by its locality rather
// than dropped. The Maps URL still keys the upsert, so a re-run updates the
// same row instead of inventing another suffix.
func (im *importer) facility(tx pgx.Tx, h scrapedHall, v vendorRef) (string, error) {
	name, err := im.uniqueName(tx, h, v)
	if err != nil {
		return "", err
	}
	var id string
	// rating_is_manual pins the scraped rating: recalcRating derives avg_rating
	// from the reviews table, which holds none of these, and would otherwise
	// reset an imported venue to zero the moment anything recalculated it.
	err = tx.QueryRow(im.ctx, `
		INSERT INTO facilities (
			owner_id, vendor_id, name, description, type, status,
			city, full_address, state, zipcode, country, lat, lng,
			contact_phone, contact_email, website,
			avg_rating, review_count, rating_is_manual,
			source, source_url, source_id
		) VALUES ($1,$2,$3,$4,'MARRIAGE_HALL','APPROVED',
		          $5,$6,$7,$8,$9,$10,$11,
		          $12,$13,$14,
		          COALESCE($15::numeric,0), COALESCE($16::int,0), TRUE,
		          'GOOGLE_MAPS', $17, $18)
		ON CONFLICT (source_id) WHERE source_id IS NOT NULL DO UPDATE SET
			name          = EXCLUDED.name,
			description   = COALESCE(NULLIF(EXCLUDED.description,''), facilities.description),
			city          = EXCLUDED.city,
			full_address  = EXCLUDED.full_address,
			state         = EXCLUDED.state,
			zipcode       = EXCLUDED.zipcode,
			lat           = EXCLUDED.lat,
			lng           = EXCLUDED.lng,
			contact_phone = EXCLUDED.contact_phone,
			contact_email = EXCLUDED.contact_email,
			website       = EXCLUDED.website,
			avg_rating    = EXCLUDED.avg_rating,
			review_count  = EXCLUDED.review_count,
			source_url    = EXCLUDED.source_url,
			updated_at    = CURRENT_TIMESTAMP
		RETURNING id::text`,
		v.userID, v.vendorID, name, nullable(h.Description),
		nullable(h.City), nullable(h.Address), nullable(h.State),
		nullable(h.Zipcode), nullable(h.Country), h.Lat, h.Lng,
		nullable(truncate(h.Phone, 20)), nullable(truncate(h.Email, 255)),
		nullable(truncate(h.Website, 255)),
		h.Rating, h.ReviewCount, h.MapsURL, placeID(h.MapsURL),
	).Scan(&id)
	return id, err
}

// uniqueName returns a name this owner can actually hold, given
// uq_facility_owner_name (owner_id, lower(trim(name)) where not deleted).
//
// The venue this import is about to upsert does not count as a clash - it is
// the row being updated - so the check excludes its own place id.
func (im *importer) uniqueName(tx pgx.Tx, h scrapedHall, v vendorRef) (string, error) {
	taken := func(candidate string) (bool, error) {
		var exists bool
		err := tx.QueryRow(im.ctx, `
			SELECT EXISTS(
				SELECT 1 FROM facilities
				 WHERE owner_id = $1 AND is_deleted = FALSE
				   AND lower(trim(name)) = lower(trim($2))
				   AND (source_id IS NULL OR source_id <> $3))`,
			v.userID, candidate, placeID(h.MapsURL)).Scan(&exists)
		return exists, err
	}

	clash, err := taken(h.Name)
	if err != nil || !clash {
		return h.Name, err
	}

	// "Sapthapadi Convention Hall" -> "Sapthapadi Convention Hall (Hebbal)",
	// using the locality the address leads with, which is what actually tells
	// two branches apart to a person reading the listing.
	if loc := locality(h); loc != "" {
		candidate := truncate(h.Name+" ("+loc+")", 255)
		clash, err = taken(candidate)
		if err != nil {
			return "", err
		}
		if !clash {
			return candidate, nil
		}
	}

	// Locality was missing or itself taken: fall back to a counter, so the
	// import never fails over a name.
	for i := 2; i < 50; i++ {
		candidate := truncate(fmt.Sprintf("%s (%d)", h.Name, i), 255)
		clash, err = taken(candidate)
		if err != nil {
			return "", err
		}
		if !clash {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot find a free name for %q", h.Name)
}

// locality is the leading component of the scraped address ("Hebbal, Mysuru,
// Karnataka 570016" -> "Hebbal"), skipping a pure street number.
func locality(h scrapedHall) string {
	for _, part := range strings.Split(h.Address, ",") {
		part = strings.TrimSpace(part)
		if part == "" || part == h.City {
			continue
		}
		if strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
			continue // bare street number, tells nobody anything
		}
		return truncate(part, 60)
	}
	return ""
}

func (im *importer) reviewRows(tx pgx.Tx, facilityID string, revs []scrapedReview) error {
	for _, r := range revs {
		if _, err := tx.Exec(im.ctx, `
			INSERT INTO scraped_reviews (facility_id, external_id, author_name,
			                             author_photo, rating, comment, relative_date)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (facility_id, external_id) DO UPDATE SET
				comment       = EXCLUDED.comment,
				rating        = EXCLUDED.rating,
				relative_date = EXCLUDED.relative_date,
				scraped_at    = CURRENT_TIMESTAMP`,
			facilityID, r.ExternalID, nullable(r.Author), nullable(r.AuthorPhoto),
			r.Rating, nullable(r.Text), nullable(r.RelativeDate)); err != nil {
			return err
		}
		im.reviews++
	}
	return nil
}

// photos copies each Google photo into our own object store. Google's URLs
// expire and block hotlinking, so recording them directly would give a listing
// that looks fine today and is broken images in a month.
// photoWorkers bounds the concurrent downloads. Each photo is a download from
// Google plus an upload to S3 - almost entirely waiting on the network, so
// running a few at once cuts the wall clock of a multi-city import from hours
// to minutes. Kept small deliberately: Google throttles a client that pulls its
// photo CDN too hard, and the pool has only 8 connections.
const photoWorkers = 4

func (im *importer) photos(facilityID, vendorID string, h scrapedHall) {
	type job struct {
		i   int
		src string
	}
	jobs := make(chan job)
	var wg sync.WaitGroup

	for w := 0; w < photoWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				// Already copied on an earlier run? The source URL is on the row.
				var exists bool
				if err := im.db.QueryRow(im.ctx,
					`SELECT EXISTS(SELECT 1 FROM facility_images
					                WHERE facility_id = $1::uuid AND source_url = $2)`,
					facilityID, j.src).Scan(&exists); err == nil && exists {
					continue
				}

				res, err := im.fetchAndStore(j.src, facilityID, vendorID)
				if err != nil {
					logger.Warn("photo", "hall", h.Name,
						"url", truncate(j.src, 60), logger.Err(err))
					continue
				}

				if _, err := im.db.Exec(im.ctx, `
					INSERT INTO facility_images (facility_id, url, is_cover,
					                             sort_order, status, source_url)
					VALUES ($1::uuid, $2, $3, $4, 'READY', $5)
					ON CONFLICT DO NOTHING`,
					facilityID, res.URL, j.i == 0, j.i, j.src); err != nil {
					logger.Warn("photo row", "hall", h.Name, logger.Err(err))
					continue
				}
				im.mu.Lock()
				im.images++
				im.mu.Unlock()
			}
		}()
	}

	for i, src := range h.PhotoURLs {
		jobs <- job{i: i, src: src}
	}
	close(jobs)
	wg.Wait()
}

// maxPhotoBytes caps a single download. storage enforces its own image limit on
// upload; this stops a mislabelled URL streaming something huge into memory first.
const maxPhotoBytes = storage.MaxImageBytes

// upscale asks Google's photo CDN for a full-size image.
//
// A scraped URL ends in a size suffix ("=w408-h306-k-no") because that is the
// thumbnail the place panel rendered, and fetching it verbatim stores a 160x120
// image that is useless on a listing page. The suffix is just a request
// parameter: replacing it with =s1600 returns the same photo at up to 1600px.
func upscale(src string) string {
	if i := strings.LastIndexByte(src, '='); i > strings.LastIndexByte(src, '/') {
		return src[:i] + "=s1600"
	}
	return src
}

func (im *importer) fetchAndStore(src, facilityID, vendorID string) (storage.Result, error) {
	req, err := http.NewRequestWithContext(im.ctx, http.MethodGet, upscale(src), nil)
	if err != nil {
		return storage.Result{}, err
	}
	// Google serves its photo CDN differently to an unrecognised client.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; venue-importer/1.0)")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return storage.Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return storage.Result{}, fmt.Errorf("http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPhotoBytes+1))
	if err != nil {
		return storage.Result{}, err
	}
	if int64(len(body)) > maxPhotoBytes {
		return storage.Result{}, fmt.Errorf("photo exceeds %dMB", maxPhotoBytes>>20)
	}
	if len(body) == 0 {
		return storage.Result{}, errors.New("empty body")
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		// Google omits the header on some CDN paths; sniffing keeps a real image
		// from being rejected over a missing header, and rejects a non-image.
		ct = http.DetectContentType(body)
		if !strings.HasPrefix(ct, "image/") {
			return storage.Result{}, fmt.Errorf("not an image (%s)", ct)
		}
	}

	res, err := im.store.Put(im.ctx, storage.Upload{
		Kind:        storage.Image,
		Body:        strings.NewReader(string(body)),
		Size:        int64(len(body)),
		ContentType: ct,
		Ext:         extFor(ct, src),
		FacilityID:  facilityID,
		VendorID:    vendorID,
	})
	return res, err
}

func extFor(contentType, src string) string {
	switch {
	case strings.Contains(contentType, "png"):
		return ".png"
	case strings.Contains(contentType, "webp"):
		return ".webp"
	case strings.Contains(contentType, "gif"):
		return ".gif"
	}
	if e := path.Ext(strings.SplitN(path.Base(src), "=", 2)[0]); len(e) > 1 && len(e) <= 5 {
		return e
	}
	return ".jpg"
}

// randomPasswordHash produces a bcrypt hash of a password nobody holds, so an
// imported account satisfies NOT NULL without being loggable-into.
func randomPasswordHash() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(b)), bcrypt.DefaultCost)
	return string(h), err
}

// nullable turns an empty string into a NULL, so an absent value is absent
// rather than an empty string masquerading as data.
func nullable(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	t := strings.TrimSpace(s)
	return &t
}

// truncate cuts s to at most n characters.
//
// It counts runes, not bytes, for two reasons: Postgres varchar(n) is a limit
// on characters, and slicing bytes can cut a multi-byte character in half. A
// venue calling itself "𝐏𝐫𝐞𝐦𝐢𝐮𝐦 𝐖𝐞𝐝𝐝𝐢𝐧𝐠" in mathematical-bold Unicode is 4 bytes
// per letter, and the fragment a byte slice left behind was rejected outright
// as invalid UTF-8.
func truncate(s string, n int) string {
	if len(s) <= n { // ASCII fast path: byte length caps rune count
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
