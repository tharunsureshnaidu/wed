# Venue data dump

1,134 marriage halls scraped from Google Maps across ten UP districts
(Gorakhpur, Maharajganj, Kushinagar, Deoria, Siddharthnagar, Khalilabad, Basti,
Azamgarh, Mau, Balrampur), with 2,444 reviews.

| file | |
|---|---|
| `venue_halls_full.sql.gz` | full `pg_dump` - schema and data, 840KB |
| `venue_halls_full.sql` | the same dump uncompressed, 2.6MB |

## What is in it

| table | rows | |
|---|---|---|
| `facilities` | 1,134 | the halls, all `APPROVED` and publicly visible |
| `scraped_reviews` | 2,444 | Google reviews: author, stars, text, relative date |
| `vendors` | 1,145 | one per venue operator, grouped by phone |
| `users` | 1,146 | the vendors' login accounts, plus the seeded admin |
| `facility_images` | 0 | **no photos** - see below |

Every hall has coordinates; 638 carry a phone number and 1,090 a rating.
Ratings are pinned with `rating_is_manual`, so `recalcRating` will not reset
them to zero - a scraped venue has no rows in `reviews` to recompute from.

## Restoring

This is a **full dump including schema**, so it restores into an empty database.
Restoring it over a database that already holds data will fail on the existing
tables.

```bash
scp venue_halls_full.sql.gz user@server:/tmp/

# On the server:
createdb -U postgres venue           # empty target
gunzip -c /tmp/venue_halls_full.sql.gz | psql -U postgres -d venue
```

It restores without errors and is safe to re-run into a fresh database. Verify:

```sql
SELECT count(*) FROM facilities;        -- 1134
SELECT count(*) FROM scraped_reviews;   -- 2444
```

**If the server database already has real data**, do not restore this over it.
Take a dump of the two tables' rows instead and load them with `ON CONFLICT DO
NOTHING`, or re-run `scripts/import_venues.sh` from the source CSVs.

## Imported accounts

Each venue operator has a `users` row that is `INACTIVE`, unverified, and holds
a random password nobody knows, so none of them can be logged into. Venues are
grouped onto one vendor by phone number, then email, then name, so an operator
running several halls from one number gets a single account. They claim their
listing through password reset.

A venue with no scraped contact details gets a placeholder address at
`@invalid.local`, a reserved TLD (RFC 2606) that can never receive mail.

## No photos

`facility_images` is empty. The scrape collected photo URLs, but every import
ran with `-skip-photos` because the AWS key carries
`AWSCompromisedKeyQuarantineV3` - AWS attaches that automatically when it
detects a leaked access key, and it denies the S3 operations the importer needs.

Rotate that key before importing media. The photo URLs are not in this dump, so
recovering images means re-scraping the cities.

## Removing this data again

```sql
CREATE TEMP TABLE imported_users AS
SELECT user_id FROM vendors WHERE source = 'GOOGLE_MAPS';

DELETE FROM facilities WHERE source = 'GOOGLE_MAPS';   -- cascades to reviews
DELETE FROM vendors    WHERE source = 'GOOGLE_MAPS';
DELETE FROM users u
 WHERE u.id IN (SELECT user_id FROM imported_users)
   AND u.status = 'INACTIVE' AND u.last_login_at IS NULL;
```

Delete the users last: `vendors.user_id` references them, and the status check
keeps an operator who has since claimed their listing.
