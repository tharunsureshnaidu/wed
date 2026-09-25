# Postman

`postman_collection.json` — 125 requests across 15 folders, covering every
endpoint the API serves.

It is **generated** from the routes registered in the Go code:

```bash
make postman     # regenerate after adding or renaming a route
```

A hand-maintained collection goes stale the first time a route changes; this one
cannot, because `scripts/gen_postman.py` reads the `mux.Handle` calls directly.

## Import

1. Postman → Import → `postman_collection.json`
2. The only variable you must set is `baseUrl` — it defaults to
   `http://localhost:8080`, which is right for a local run.

## First run

Folders are ordered so you can work top to bottom.

1. **Auth → Register (venue owner)**
2. There is no mail service, so read the OTP from the log:
   ```bash
   make otp
   ```
   Paste it into the `otp` collection variable.
3. **Auth → Verify email OTP** — this stores `accessToken` and, for a vendor,
   `ownerToken`, automatically.
4. **Facilities → Create facility** — stores `hallId`.

From there most requests work without copying anything: test scripts capture
ids (`hallId`, `bookingId`, `quoteId`, `paymentId`, …) as they are created.

Access tokens last 1 hour. On a 401, run **Auth → Refresh token**.

## Folder order matters

Requests run in dependency order, not alphabetically, and destructive requests
are isolated:

- **Cleanup (destructive)** holds every DELETE and runs last. Without this,
  "Delete hall" ran inside the Facilities folder and every later booking and
  quote failed with `INVALID_HALL`.
- Inside Cleanup, children are deleted before their parent facility, or the
  child deletes 404 against an already-deleted parent.
- **Favourites** runs after Facilities, since a favourite needs a facility.

## Running the whole collection

With [newman](https://github.com/postmanlabs/newman):

```bash
npm install -g newman
make postman-test
```

The API's global rate limit is 100 requests/minute, and a full run is 125, so
`make postman-test` starts the API with `RATE_LIMIT_DISABLED=true`. Never set
that outside local testing.

A full run against a fresh database gives roughly **108/125 requests returning
200**. The rest are correct behaviour, not failures:

| Request | Why |
|---|---|
| Register (customer / vendor) | `409` on a second run — the account already exists |
| Verify email / phone OTP | `400` unless you set a current `otp` variable |
| Login | `403 UNVERIFIED_ACCOUNT` until that account's OTP is verified |
| Resend OTP | `429` — one OTP per minute per target, by design |
| Refresh / Logout / Reset password | `400` until a refresh or reset token exists |
| Gateway webhook | `401` without a valid HMAC signature (see below) |
| Create payment | `409` when the booking is already paid |
| Reject / Cancel quote | `409` after the quote was accepted and converted |
| Write a review | `403` unless that account has a CONFIRMED booking there |
| Refund payment | needs `paymentId`, which only a successful payment sets |
| Delete user | `400` — an admin cannot delete their own account |

## The payment webhook

It is authenticated by signature, not a token, because a gateway calls it. Sign
the exact raw body with `PAYMENT_WEBHOOK_SECRET` from `.env`:

```bash
BODY='{"eventId":"evt-001","orderId":"<gatewayOrderId>","paymentId":"pay_abc","status":"SUCCESS","amount":150000}'
SECRET=$(grep '^PAYMENT_WEBHOOK_SECRET=' .env | cut -d= -f2)
printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $NF}'
```

Put that in the `webhookSignature` variable, and send the same body verbatim —
the signature covers the raw bytes, so reformatting the JSON invalidates it.

## Adding a route

Add it in Go, then:

```bash
make postman
```

A new route appears automatically. To give it a friendly name, an example body
or a place in the run order, add it to `NAMES`, `BODIES` and `REQUEST_ORDER` in
`scripts/gen_postman.py`.
