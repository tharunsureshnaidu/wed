# Admin API — editing a marriage hall

Written for: whoever operates the admin panel or loads imported venue data.

Every endpoint here needs a `ROLE_ADMIN` token:

```
Authorization: Bearer <admin token>
```

Most facility editing already worked before: the vendor routes under
`/api/v1/facilities` accept an admin as well as the owner, so they are listed
here rather than duplicated under `/admin`. Only the genuinely admin-specific
pieces live under `/api/v1/admin`.

---

## The one call that shows everything

```
GET /api/v1/admin/facilities/{id}
```

Returns the facility plus its pricing rules, packages, amenities, images and
latest 50 reviews in a single response. Unlike the public `GET /api/v1/halls/{id}`
it also returns venues that are `PENDING`, `REJECTED` or `BLOCKED`, and includes
the owner's name and email.

---

## Editing fields

```
PATCH /api/v1/admin/facilities/{id}
```

Send **only the fields you want to change**. Anything you leave out is
untouched — unlike `PUT /api/v1/halls/{id}`, which replaces the whole record and
nulls whatever you omit.

```json
{
  "name": "Royal Grand Palace",
  "contactPhone": "+919876543210",
  "basePricePerDay": 125000
}
```

Editable: `name`, `description`, `city`, `fullAddress`, `state`, `zipcode`,
`country`, `lat`, `lng`, `contactPhone`, `contactEmail`, `website`,
`starRating`, `checkInTime`, `checkOutTime`, `capacityPax`, `areaSqft`,
`basePricePerDay`, `seatingCapacity`, `floatingCapacity`, `minBookingSize`,
`status`, `isVerified`, `isFeatured`, `ownerId`.

Sending `null` for a nullable field clears it. Changing `status` notifies the
owner; changing anything else does not.

`contactPhone` / `contactEmail` / `website` are new columns (migration 038).
Contact details previously existed only on the vendor record, which is one set
of details for the whole business — an operator with several halls needs a
different number per listing.

---

## Ratings and reviews

**Pin an imported rating:**

```
PUT /api/v1/admin/facilities/{id}/rating
{ "avgRating": 4.3, "reviewCount": 218 }
```

Normally `avgRating` is computed from the `reviews` table. An imported venue has
a rating earned on another platform and no review rows here, so the computed
value would be `0`.

This sets `rating_is_manual`, which makes the recalculation **skip** this
facility. Without it, the first review posted in this system would replace
"4.3 from 218 reviews" with "1.0 from 1 review". Verified: posting a 1-star
review against a pinned 4.3 left it at 4.3.

**Hand it back to real reviews:**

```
DELETE /api/v1/admin/facilities/{id}/rating
```

Unpins and recomputes immediately from actual review rows.

**Edit individual reviews** (these already existed):

```
POST   /api/v1/admin/reviews
PUT    /api/v1/admin/reviews/{id}
DELETE /api/v1/admin/reviews/{id}
```

---

## Prices, packages, amenities, images

These are the **vendor routes**, which an admin can call for any facility.
`{id}` is the facility id.

| Action | Endpoint |
|---|---|
| Add a price rule | `POST /api/v1/facilities/{id}/pricing` |
| Change a price rule | `PUT /api/v1/facilities/{id}/pricing/{childId}` |
| Delete a price rule | `DELETE /api/v1/facilities/{id}/pricing/{childId}` |
| List price rules | `GET /api/v1/facilities/{id}/pricing` |
| Add / delete a package | `POST` / `DELETE /api/v1/halls/{id}/packages[/{childId}]` |
| Add / delete an add-on | `POST` / `DELETE /api/v1/halls/{id}/addons[/{childId}]` |
| Attach / remove an amenity | `POST` / `DELETE /api/v1/facilities/{id}/amenities/{amenityId}` |
| Upload / delete an image | `POST` / `DELETE /api/v1/facilities/{id}/images[/{childId}]` |
| Set cover image | `PUT /api/v1/facilities/{id}/images/{childId}/cover` |
| Reorder images | `PUT /api/v1/facilities/{id}/images/reorder` |
| Policies | `POST` / `PUT` / `DELETE /api/v1/facilities/{id}/policies[/{childId}]` |
| Cancellation policies | `POST` / `DELETE /api/v1/facilities/{id}/cancellation-policies[/{childId}]` |
| Hotel room types | `POST` / `DELETE /api/v1/hotels/{id}/room-types[/{childId}]` |
| Delete the whole listing | `DELETE /api/v1/halls/{id}` (soft delete) |

Pricing example:

```json
POST /api/v1/facilities/{id}/pricing
{ "price": 95000, "eventType": "WEDDING", "dayType": "WEEKEND",
  "minGuests": 100, "maxGuests": 500 }
```

---

## Approvals, users, vendors

| Action | Endpoint |
|---|---|
| Approve / reject / block a listing | `POST /api/v1/admin/facilities/{id}/approve?status=APPROVED` |
| List facilities | `GET /api/v1/admin/facilities` |
| Block / unblock a user (toggles) | `POST /api/v1/admin/block` |
| Users CRUD | `GET` / `POST` / `PUT` / `DELETE /api/v1/admin/users[/{id}]` |
| Vendor KYC approve / reject | `POST /api/v1/admin/vendors/{vendorId}/kyc/approve\|reject` |

Each of these notifies the affected user automatically.

---

## Help & support helpline

The number and email the app shows on its Help screen. Stored in `app_settings`
as `support.*` keys, so adding a new support field later is a seeded row plus
one map entry — not a migration.

**What the app calls** — public, no token, because a customer who cannot sign in
is exactly the person who needs the helpline:

```
GET /api/v1/support
{ "supportEmail": "support@tripfactory.travel",
  "supportPhone": "+91 80 4000 0000",
  "supportWhatsapp": "", "supportHours": "Mon-Sat, 9:00 AM - 7:00 PM",
  "supportAddress": "" }
```

**What the admin UI calls:**

```
GET /api/v1/admin/support     # same fields + updatedBy / updatedAt
PUT /api/v1/admin/support     # send only the fields you are changing
```

Notes that matter when wiring the UI:

- **Partial by design.** Omitted fields are left alone; `""` clears a field and
  means "hide this in the app".
- **Email and phone cannot both be blank** — that would leave customers with no
  way to reach support at all. Rejected with 400.
- **Formatting is preserved.** `"+91 98765 43210"` stores as typed. Validation
  counts digits (7–15) rather than using `validate.Phone`, which enforces E.164
  for login numbers and would reject every realistic helpline.
- The PUT returns the full updated object, so the UI can store the response
  directly without re-fetching.

## Loading imported data

The intended order per venue:

1. Insert the `facilities` row (you said you would do this directly in the DB).
   `owner_id` must reference a real user.
2. `PATCH /api/v1/admin/facilities/{id}` — contact details and anything the
   insert did not set.
3. `POST /api/v1/facilities/{id}/pricing` — one call per price rule.
4. `PUT /api/v1/admin/facilities/{id}/rating` — the imported rating.
5. `POST /api/v1/admin/facilities/{id}/approve?status=APPROVED` — make it live.

**Set the rating last.** Step 5 does not touch it, but any review posted
between steps would.

---

## Fixed along the way

`requireOwner` checked only the caller's *primary* role, so an admin whose token
listed `ROLE_ADMIN` second was refused with 403 on every facility they did not
personally own. Confirmed by reverting the fix: 403 before, 200 after. If admin
editing has ever seemed to work only sometimes, this was why.
