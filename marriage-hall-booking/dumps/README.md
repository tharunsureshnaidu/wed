# Venue data dump

1,134 marriage halls scraped from Google Maps across ten UP districts
(Gorakhpur, Maharajganj, Kushinagar, Deoria, Siddharthnagar, Khalilabad, Basti,
Azamgarh, Mau, Balrampur), with 2,444 reviews.

| file | |
|---|---|
| `venue_halls_dataonly.sql.gz` | INSERTs only, for a database that already has data (880KB) |
| `venue_halls_full.sql.gz` | full `pg_dump` - schema and data, for an empty database (840KB) |

Both are also present uncompressed. They were produced with pg_dump 14 and
restore into PostgreSQL 16; the psql 17 `\restrict` meta-commands pg_dump
emitted have been stripped, since psql 16 rejects them.

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

## Which file to use

| your server's `venue` database | file |
|---|---|
| already has real users, vendors or bookings | `venue_halls_dataonly.sql.gz` |
| empty / brand new | `venue_halls_full.sql.gz` |

If in doubt, check first - anything above 1 means use the data-only file:

```bash
psql -U postgres -d venue -tAc "SELECT count(*) FROM users;"
```

Both were restored and verified before shipping, and both are safe to run twice.

## Restoring into an existing database (data-only)

The API applies migration `044_scraped_import.sql` on start-up, so deploy and
restart the API first - the tables this loads into will not exist otherwise.

```bash
cd ~/wed/marriage-hall-booking
sudo systemctl restart venue-api        # applies migration 044
gunzip -c dumps/venue_halls_dataonly.sql.gz | psql -U postgres -d venue
```

Every statement is `INSERT ... ON CONFLICT DO NOTHING`, so it adds the venues
without touching anything already in those tables, and re-running changes
nothing.

## Restoring into an empty database (full)

```bash
createdb -U postgres venue
gunzip -c dumps/venue_halls_full.sql.gz | psql -U postgres -d venue
```

This carries the schema as well, so it must go into a database with no tables.

## Verifying

```sql
SELECT count(*) FROM facilities;        -- 1134
SELECT count(*) FROM scraped_reviews;   -- 2444
SELECT city, count(*) FROM facilities GROUP BY city ORDER BY 2 DESC LIMIT 5;
```

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
