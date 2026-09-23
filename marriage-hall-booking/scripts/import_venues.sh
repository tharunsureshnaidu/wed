#!/usr/bin/env bash
#
# Import scraped venue CSVs into the production database.
#
# The schema this needs (044_scraped_import.sql) is applied by the API on boot,
# so deploy the release first and then run this. It verifies that, takes a
# backup, dry-runs every city, and only then writes.
#
#   ./scripts/import_venues.sh /path/to/csv-dir
#
# The directory holds the scraper's output pairs: <city>_leads.csv and
# <city>_reviews.csv. Re-running is safe - venues are keyed by Google Maps URL
# and reviews by Google's review id, so a second run updates instead of
# duplicating.
set -euo pipefail

CSV_DIR="${1:-}"
if [[ -z "$CSV_DIR" || ! -d "$CSV_DIR" ]]; then
    echo "usage: $0 <csv-dir>   (containing <city>_leads.csv / <city>_reviews.csv)" >&2
    exit 2
fi

cd "$(dirname "$0")/.."

# Config comes from .env exactly as the API reads it, so this cannot import into
# a different database than the one the app serves.
[[ -f .env ]] || { echo "no .env in $(pwd)" >&2; exit 1; }
set -a; . ./.env; set +a

DB_HOST="${DB_HOST:-localhost}"; DB_PORT="${DB_PORT:-5432}"
DB_NAME="${DB_NAME:-venue}";     DB_USERNAME="${DB_USERNAME:-postgres}"
export PGPASSWORD="${DB_PASSWORD:-}"
psql_() { psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USERNAME" -d "$DB_NAME" -tAq "$@"; }

echo "==> target: $DB_USERNAME@$DB_HOST:$DB_PORT/$DB_NAME"
psql_ -c 'SELECT 1' >/dev/null || { echo "cannot connect" >&2; exit 1; }

# The importer writes columns that only exist after 044. Without this check the
# first failure would be a confusing per-row SQL error on every venue.
if [[ "$(psql_ -c "SELECT COUNT(*) FROM information_schema.columns
                    WHERE table_name='facilities' AND column_name='source_url'")" != "1" ]]; then
    echo "ERROR: 044_scraped_import.sql has not been applied." >&2
    echo "       Deploy the current release and restart the API first - migrations run on boot." >&2
    exit 1
fi
echo "==> schema ok (044 applied)"

# AWS_S3_BUCKET decides whether photos go to S3 or to local disk, and local disk
# is lost on the next redeploy. Importing thousands of photos to the wrong place
# is expensive to undo, so it is a prompt rather than a warning.
if [[ -z "${AWS_S3_BUCKET:-}" ]]; then
    echo "WARNING: AWS_S3_BUCKET is not set - photos would be written to local disk"
    read -rp "continue anyway? [y/N] " a; [[ "$a" == "y" ]] || exit 1
fi

BACKUP="/tmp/venue-backup-$(date +%Y%m%d-%H%M%S).sql.gz"
echo "==> backing up to $BACKUP"
pg_dump -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USERNAME" "$DB_NAME" | gzip > "$BACKUP"
echo "    $(du -h "$BACKUP" | cut -f1)"

go build -o /tmp/venue-import ./cmd/import

shopt -s nullglob
LEADS=("$CSV_DIR"/*_leads.csv)
(( ${#LEADS[@]} )) || { echo "no *_leads.csv in $CSV_DIR" >&2; exit 1; }

# Dry-run everything before writing anything: a malformed CSV should be found
# now, not after three cities are already in the database.
echo "==> dry run"
for f in "${LEADS[@]}"; do
    /tmp/venue-import -leads "$f" -reviews "${f%_leads.csv}_reviews.csv" -dry-run 2>&1 \
        | grep -E "parsed|ERROR" | sed "s|^|    $(basename "${f%_leads.csv}"): |"
done

read -rp "==> proceed with import? [y/N] " a; [[ "$a" == "y" ]] || { echo "aborted"; exit 1; }

before=$(psql_ -c "SELECT COUNT(*) FROM facilities")
for f in "${LEADS[@]}"; do
    city=$(basename "${f%_leads.csv}")
    echo "==> importing $city"
    /tmp/venue-import -leads "$f" -reviews "${f%_leads.csv}_reviews.csv" ${SKIP_PHOTOS:+-skip-photos}
done

echo "==> done"
psql_ -c "SELECT city, COUNT(*) FROM facilities
           WHERE source='GOOGLE_MAPS' GROUP BY city ORDER BY 2 DESC" \
    | awk -F'|' '{printf "    %-22s %s\n", $1, $2}'
echo "    facilities: $before -> $(psql_ -c 'SELECT COUNT(*) FROM facilities')"
echo "    scraped reviews: $(psql_ -c 'SELECT COUNT(*) FROM scraped_reviews')"
echo "    images: $(psql_ -c "SELECT COUNT(*) FROM facility_images WHERE source_url IS NOT NULL")"
echo
echo "rollback if needed:  gunzip -c $BACKUP | psql -h $DB_HOST -U $DB_USERNAME -d $DB_NAME"
