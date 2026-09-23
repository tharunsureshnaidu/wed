# Importing scraped venues

Loads marriage halls scraped from Google Maps into Postgres: one vendor per
operator, one facility per hall, photos copied to S3, and the Google reviews
stored alongside.

## 1. Scrape (on the scraper machine)

`reviews_per_place` is what makes the scraper collect individual reviews; left
out, only the aggregate rating and count are captured.

```bash
curl -X POST localhost:5000/api/search -H 'Content-Type: application/json' \
  -d '{"business":"banquet hall","city":"Gorakhpur","max_results":120,"reviews_per_place":5}'
curl -o gorakhpur.xlsx localhost:5000/api/export/<search_id>
```

"banquet hall" returns far more venues than "marriage hall" in the UP districts
(116 vs 18 on a single Gorakhpur search) - it is worth running both phrasings
and letting the place id deduplicate them.

Scraping is bound by browser processes, not by Google. `WORKERS` in
`services/places.py` is the number of parallel Chromiums, each carrying about
four processes, so keep it at or below half the core count. Running 12 on a
12-core machine pushed the load average past 16 and starved the browsers: page
timeouts went from 3 in the first city to 117 in the fifth, and two workers died
outright, each taking its whole batch of venues with it.

The export writes three files: the workbook to read, plus `*_leads.csv` and
`*_reviews.csv`, which are what the importer consumes.

## 2. Import (on the server)

Migration `044_scraped_import.sql` must be applied first. It ships in the
binary and runs on API start-up, so deploy the release and restart the API
before importing; the script refuses to run otherwise.

```bash
./scripts/import_venues.sh /path/to/csv-dir
```

It backs the database up, dry-runs every city, prints what it would write, and
waits for confirmation before touching anything. `SKIP_PHOTOS=1` imports the
venue rows without copying images, which is much faster for a first pass.

Photos are copied 4 at a time (`photoWorkers` in `cmd/import/main.go`). Raising
it speeds up a large import but Google throttles a client that pulls its photo
CDN too hard, and the database pool only has 8 connections.

Each photo is fetched at full size rather than at the thumbnail size the
scraper recorded (`upscale` rewrites the URL's size suffix to `=s1600`), so
expect roughly 400KB per image: Gorakhpur's 224 photos came to 88MB, and ten
cities of this size land somewhere under a gigabyte. Budget S3 accordingly, and
use `SKIP_PHOTOS=1` if you want the venues listed before the media lands.

Re-running is safe. A venue is keyed by Google's own place id (`source_id`) and
a review by its review id, so a second run updates what the first created
rather than inserting duplicates. The place id is what makes this work across
cities too: a venue near a district border appears in both cities' scrapes, and
keying on the Maps URL imported it twice.

Expect the imported count to come in under the scraped count. Google serves the
same venue under two URL shapes between query passes - sometimes appending a
landmark to the name in one of them ("... (near Bagaha Baba Mandir)") - so the
importer collapses them on the place id embedded in the URL. Gorakhpur scraped
170 rows and imported 152 real venues.

## What lands where

| Scraped | Column |
|---|---|
| Business Name | `facilities.name` |
| Address | `full_address`, with `city`/`state`/`zipcode` parsed out |
| Rating, Reviews | `avg_rating`, `review_count`, pinned by `rating_is_manual` |
| Google Maps URL | `source_url`, with the place id in `source_id` |
| Phone, Email, Website | `contact_phone`, `contact_email`, `website` |
| Latitude/Longitude | `lat`, `lng` |
| All Photo URLs | `facility_images`, copied into S3 |
| Reviews sheet | `scraped_reviews` |

Imported halls are created `APPROVED`, so they are publicly visible as soon as
the import finishes.

## Imported accounts

Each operator gets a `users` row (`INACTIVE`, unverified, random password) and
a vendor. Halls are grouped onto one vendor by phone, then email, then name, so
a chain running several halls from one number gets a single account. An
operator claims their listing through password reset.

A venue with no scraped contact details gets a placeholder address at
`@invalid.local` - a reserved TLD (RFC 2606) that can never receive mail.
`rating_is_manual` stops `recalcRating` from wiping an imported rating: those
venues have no rows in `reviews` to recompute an average from.

## Rollback

The script prints the backup path and its restore command on completion.
Failing that, imported data is identifiable on its own:

Delete the users last: `vendors.user_id` references them, and deleting the
vendor rows on their own leaves the imported login accounts behind.

```sql
-- Capture the accounts first: once the vendor rows are gone there is nothing
-- left marking those users as imported.
CREATE TEMP TABLE imported_users AS
SELECT user_id FROM vendors WHERE source = 'GOOGLE_MAPS';

DELETE FROM facilities WHERE source = 'GOOGLE_MAPS';   -- cascades to images/reviews
DELETE FROM vendors    WHERE source = 'GOOGLE_MAPS';
-- Only accounts that never signed in and hold nothing else - an operator who
-- has claimed their listing must keep their account.
DELETE FROM users u
 WHERE u.id IN (SELECT user_id FROM imported_users)
   AND u.status = 'INACTIVE' AND u.last_login_at IS NULL;
```
