# Java → Go parity audit

Java (source of truth): `Hall_Booking/marriage-hall-booking/monolith-service`
Go (migration): `wed/marriage-hall-booking`

Every finding below was **reproduced against the running API or the database**,
not inferred from reading code. Anything unverified is marked NOT VERIFIED.

---

## A. Media / S3 — IMPLEMENTED (2026-09-14)

`pkg/storage` now backs all media. `Store` has two implementations chosen by
whether `AWS_S3_BUCKET` is set: `S3` for deployments, `Local` for development.
A configured bucket that fails to initialise stops start-up rather than falling
back to local disk, because a production deployment quietly writing to a
container filesystem looks healthy while losing every upload on redeploy.

Verified against the real bucket (`venue-booking-public`, `eu-north-1`):

| Check | Result |
|---|---|
| Startup backend | `backend=s3://venue-booking-public` |
| Multipart image upload | key `vendors/vendor-{id}/venue-{id}/gallery/{rand}.png` |
| Object round-trip | GET 200, `image/png`, bytes identical to source |
| Multipart video upload | key `.../videos/{rand}.mp4`, GET 200 `video/mp4` |
| HTML renamed `.png` | rejected — content sniffed, not extension |
| Image posted to /videos | rejected `is not an MP4 or MOV video` |
| Missing file part | `image file is required` |
| JSON `{url}` form | still works — no client breakage |
| Delete | row removed; S3 object 403s (quarantine, see D) and is logged |

Implemented to match Java exactly: key layout, per-type size caps
(10MB / 200MB / 20MB), `publicUrl` format, and the allowlist-not-blocklist rule.

Still divergent from Java, deliberately:
- **No server-side image re-encode.** Java resizes to 1920px and re-encodes as
  JPEG q=0.82, cutting stored bytes 80-90%. Go stores the original. Worth adding;
  needs an image-decode step, so it is its own change.
- **No `width`/`height`/`mimeType`/`sizeBytes` columns** on `facility_images`.
  `storage.Result` already carries size and key, so persisting them is a
  migration plus a column list.

---

## A-prev. Media / S3 — the gap as originally found

### What Java actually does

`facility/service/S3UploadService.java` (214 lines) + `facility/config/S3Config.java`.

- `S3Client` from the AWS SDK v2, region from `aws.s3.region`, bucket from `aws.s3.bucket`.
- Credentials come from the SDK's default chain (`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`).
- Bytes are streamed **through the application** and `putObject`ed. There is **no presigned
  URL, no CloudFront, no MediaConvert, no ffmpeg, no HLS** anywhere in the Java tree
  (verified by grep across *.java, *.yml, *.properties — zero hits).
- Images are re-encoded server-side: max dimension 1920, JPEG quality 0.82, alpha flattened
  onto white. Returns width/height.
- Object keys:
  - `vendors/vendor-{vendorId}/venue-{facilityId}/gallery/{uuid}.{ext}`
  - `vendors/vendor-{vendorId}/venue-{facilityId}/videos/{uuid}.{ext}`
  - `venues/{facilityId}/...` when the facility has no vendor
  - `documents/{uuid}.{ext}` for quote attachments
- URL: `https://{bucket}.s3.amazonaws.com/{key}`
- Allowlists + caps: images jpg/jpeg/png/webp @ 10MB; videos mp4/mov @ 200MB;
  documents pdf/jpg/jpeg/png @ 20MB.

### What Go does

- **Zero** S3/AWS references in the entire Go tree (verified by grep).
- `internal/facility/handler/upload.go` writes to `./uploads` and serves it from the API
  (`GET /uploads/`). Marked as a deliberate local-dev shortcut in a `ponytail:` comment,
  with the seam correctly isolated in `saveUpload` — but never followed through.
- **No video upload at all.** Java has `POST /{id}/videos` (multipart); Go's route of the
  same name accepts a JSON `{"url": ...}`.
- **No image processing** — no resize, no re-encode, no width/height.
- `POST /facilities/{id}/images` takes a client-supplied URL. A caller can register any
  external URL as a venue photo; no file is ever uploaded or validated.
- `facility_images` columns: id, facility_id, url, is_cover, sort_order, created_at.
  Java also persists mimeType, sizeBytes, width, height.

### Consequence

Production media would be written to the API container's local disk and lost on redeploy.

---

## B. Verified behavioural defects (reproduced live)

### B1. Vendor profile edits destroy data — FIXED (2026-09-14)
`PUT /vendors/me` uses `excluded.*` for all 13 columns, so any omitted field is set to NULL.
Java (`VendorService.updateProfile`) null-guards each field: *"every field optional, partial
update"*.

Reproduced:
```
BEFORE: name "Kumar Weddings", address "42 MG Road", phone +919876543210, website kumar.com
PUT {"businessName": "Kumar Weddings & Events"}
AFTER : address NULL, phone NULL, website NULL
```

**Fix:** every column in the ON CONFLICT clause now uses
`COALESCE(excluded.col, vendors.col)`. `businessName` became optional (`*string`)
to match Java's UpdateVendorProfileRequest; it is still required on the first
save because `business_name` is NOT NULL, and the existing value is read into
the INSERT slot on later saves — NOT NULL is checked on the proposed row before
ON CONFLICT can redirect, so a nil there fails with 23502.

Java's field validation was also reproduced (`UpdateVendorProfileRequest`):
`@Size` caps on 7 fields, `@Pattern` phone, `@Email`, `@Pattern` URL and UPI id —
with Java's exact message strings, which clients match on. Added
`validate.URL`, `validate.UpiID`, `validate.MaxLength`.

Regression test: `internal/vendors/handler/partial_update_test.go`, proven to
fail when `excluded.*` is restored.

### B2. Expired quotes are still acceptable
`valid_until` is stored and returned but never compared to now. Java calls
`applyExpiryIfNeeded` on every read and mutation.
Reproduced: accepted an offer with `validUntil = 2020-01-01` → `200 Quote accepted successfully`.

### B3. Blocked venues leak into public autocomplete
`names()` filters only `is_deleted`. Java filters `status = APPROVED`.
Reproduced: set two halls to BLOCKED; both still returned by `/search/autocomplete`.

### B4. No duplicate-quote guard
Java `createQuoteRequest` rejects an existing non-terminal quote for the same
customer+facility+date with `409 DUPLICATE_ACTIVE_QUOTE`.
Reproduced: three identical requests all returned 201.

### B5. Quote conversion is not atomic
`convert` calls `CreateHallBooking` (which commits) and then `UPDATE quotes` outside any
transaction. Java wraps `convertToBooking` in `@Transactional`. A failure after the booking
commits leaves a booking whose quote still reads ACCEPTED.

### B6. Review eligibility narrower than Java
Java `VERIFIABLE_STATUSES = CONFIRMED, PARTIALLY_PAID, COMPLETED`; Go SQL uses
`IN ('CONFIRMED','COMPLETED')`. Note: `PARTIALLY_PAID` is **not** in the Go bookings
status CHECK constraint, so this needs a schema decision, not just a query change.
Java also always accepts the review and stamps `verifiedPurchase`; Go hard-403s and has
no such field.

### B7. Admin soft-delete filters missing
`updateUser` and `deleteUser` have no `is_deleted = FALSE` in their WHERE clause, so a
soft-deleted user can be renamed and set back to ACTIVE. Java's `findUser` filters
`!u.isDeleted()` for update, delete and toggleBlock.

### B8. Fraud report has no terminal guard
Go's UPDATE has no status precondition. Java refuses to re-resolve a report already in
`RESOLVED_BANNED`/`RESOLVED_CLEARED` with `409 ALREADY_RESOLVED`.

---

## C. Architecture

| Module | Layers | SQL in handler | Tests |
|---|---|---|---|
| booking | H·S·R | 0 | 10 |
| auth, user, payment | H·S·R | 0 | yes |
| admin | H only | 33 | 0 |
| quote | H only | 21 | 0 |
| review / search / vendors | H only | 7–8 | 0 |

Every module with a service layer has tests; every handler-only module has none.
`quote.addVersion` is 112 lines doing authorization, money arithmetic and SQL in one
function — the pricing rule cannot be tested without an HTTP server and a live database.

---

## D. S3 credentials — BLOCKED

Bucket `venue-booking-public`, region `eu-north-1`, IAM user `venue-booking-app`
(account 702228043987). No CloudFront configured anywhere.

The credentials carry **`AWSCompromisedKeyQuarantineV3`** — AWS attaches this
automatically when it detects a key published publicly. Verified capability:

| Operation | Result |
|---|---|
| sts:GetCallerIdentity | works |
| s3:PutObject | works |
| s3:ListBucket | explicit deny |
| s3:DeleteObject | explicit deny |
| public object read | 200 |

Media deletion is an endpoint in both codebases and **cannot work** until the key
is rotated and the quarantine policy removed. S3 wiring is on hold for that.

`.env` is gitignored and untracked in both repos, so the leak came from
somewhere else — rotating alone will not prevent a recurrence if the other path
is still open.

Left behind: `s3://venue-booking-public/_probe/parity-check.txt` (write probe;
cannot be deleted under the current deny).

---

## E. Not yet verified

- Kafka topic/event parity (Java producers/consumers vs Go) — NOT VERIFIED
- Redis cache key/TTL parity beyond search — NOT VERIFIED
- Payment module parity — NOT VERIFIED
- Full validation matrix (Java DTO annotations vs Go validate.Errors) — partially verified
  for vendors/search/review only
