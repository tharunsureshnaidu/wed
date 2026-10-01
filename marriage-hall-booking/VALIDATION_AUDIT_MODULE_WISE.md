# Module-Wise Validation Specification & Audit

> **Target Project**: `wed/marriage-hall-booking` (Go Backend)  
> **Source Parity**: `Hall_Booking/marriage-hall-booking/monolith-service` (Java Spring Boot)  
> **Document Purpose**: Complete technical specification detailing every endpoint and input field requiring validation across all internal modules, without code changes.

---

## 1. Executive Summary & Architecture Overview

The Go backend currently relies on a lightweight internal validation package (`pkg/validate`) and manual ad-hoc checks within HTTP handlers. While critical core checks exist for authentication and basic required fields, multiple endpoints lack:
1. **Input Bounds & Character Limits**: Absence of maximum lengths allows payload bloat and potential database truncation/DoS errors (500 Internal Server Error instead of 400 Bad Request).
2. **Numeric Range & Coordinate Verification**: Fields like `lat`, `lng`, `basePrice`, `guestCount`, `discountValue`, and `refundPercentage` are sometimes unchecked for negative or out-of-bounds values.
3. **Format & Regex Consistency**: Phone numbers, IFSC codes, bank accounts, dates, times, and UUIDs are inconsistently enforced across different endpoints.
4. **State Machine & Transition Rules**: Business constraints (e.g. canceling completed bookings, refunding higher amounts than paid, quoting on past dates, or approving unverified KYC) require strict pre-validation.

---

## 2. Standardized API Error Response Contract

All validation failures across every module must strictly return `HTTP 400 Bad Request` conforming to the existing project envelope:

```json
{
  "success": false,
  "message": "Field validation failure description(s)",
  "error": {
    "code": "VALIDATION_ERROR"
  },
  "timestamp": "2026-09-30T10:00:00Z"
}
```

---

## 3. Module-Wise Validation Breakdown

```
├── 1. Auth Module (internal/auth)
├── 2. User Module (internal/user)
├── 3. Vendor Module (internal/vendors)
├── 4. Facility Module (internal/facility)
├── 5. Booking Module (internal/booking)
├── 6. Quote Negotiation Module (internal/quote)
├── 7. Payment & Refund Module (internal/payment)
├── 8. Coupon & Promo Module (internal/coupon)
├── 9. Review & Rating Module (internal/review)
├── 10. App Feedback Module (internal/feedback)
├── 11. Support & Helpline Module (internal/support)
├── 12. Notification & Device Module (internal/notification)
├── 13. Search & Discovery Module (internal/search)
└── 14. Admin & Backoffice Module (internal/admin)
```

---

### Module 1: Auth (`internal/auth`)

#### Current State
- Validates non-empty `fullName`, basic email/phone format, and password length (8-100 characters).
- Checks OTP type validity against enum domain.

#### Missing & Required Validations
1. **`POST /api/v1/auth/register` & `POST /api/v1/auth/register/vendor`**:
   - `fullName`: Min 2, max 100 characters; reject string with only spaces or control characters.
   - `email`: Enforce lowercase normalization, max 255 chars, standard RFC 5322 compliance.
   - `phoneNumber`: E.164 pattern (`^\+?[1-9]\d{9,14}$`), max 16 chars.
   - `password`: Enforce max 100 chars, reject whitespace-only passwords.
   - Cross-field: Exactly one or both of `email` / `phoneNumber` must be supplied.

2. **`POST /api/v1/auth/login`**:
   - `identifier`: Required, max 255 characters, trimmed of outer whitespace.
   - `password`: Required, max 100 characters.

3. **`POST /api/v1/auth/register/verify-email` & `verify-phone`**:
   - `target`: Required, valid email or phone format.
   - `otpCode`: Must match exact 6-digit numeric pattern: `^[0-9]{6}$`. Currently accepts arbitrary strings.

4. **`POST /api/v1/auth/otp/resend`**:
   - `target`: Required, valid email/phone format.
   - `otpType`: Allowlist validation: `REGISTRATION`, `PASSWORD_RESET`, `LOGIN_2FA`.

5. **`POST /api/v1/auth/forgot-password`**:
   - `identifier`: Required, must be a valid email or phone number.

6. **`POST /api/v1/auth/reset-password`**:
   - `identifier`: Required.
   - `token`: Required, non-empty, hex or UUID format (32-64 chars).
   - `newPassword`: Min 8, max 100 characters.

---

### Module 2: User (`internal/user`)

#### Current State
- Validates `firstName` (1-100 chars), `lastName` (max 100 chars), `bio` (max 500 chars).
- UUID check on path parameter `{facilityId}` for favourites.

#### Missing & Required Validations
1. **`PUT /api/v1/users/me`**:
   - `firstName`: Strip leading/trailing whitespace. Must not be empty after trimming.
   - `lastName`: Optional, but if provided, must not exceed 100 chars after trimming.
   - `bio`: Max 500 chars, strip potential malicious script tags / HTML escape.

2. **`GET /api/v1/users/me/favourites`**:
   - Query parameter `type`: Allowlist validation. Only `""`, `HOTEL`, or `MARRIAGE_HALL` allowed. Any other value should return a 400 Bad Request instead of returning an empty set silently.

3. **`POST /api/v1/users/me/favourites/{facilityId}`**:
   - `{facilityId}`: Must be a valid UUID v4 and exist in the active facilities database.

---

### Module 3: Vendors (`internal/vendors`)

#### Current State
- Checks max lengths for profile strings, regex for GST/PAN, and basic presence for bank fields.

#### Missing & Required Validations
1. **`PUT /api/v1/vendors/me` (Profile Update)**:
   - `businessName`: 2-255 characters. Required on initial creation.
   - `businessType`: Enforce enum allowlist: `INDIVIDUAL`, `PROPRIETORSHIP`, `PARTNERSHIP`, `PVT_LTD`, `LLP`, `OTHER`.
   - `businessTypeOther`: Required if `businessType == "OTHER"`, max 100 chars.
   - `website`: Must begin with `http://` or `https://` and be a valid URL, max 500 chars.
   - `upiId`: Strict UPI regex: `^[\w.\-]{2,256}@[a-zA-Z]{2,64}$`.
   - `businessPhone`: E.164 phone format, max 20 chars.
   - `businessEmail`: Standard email format, max 255 chars.
   - `businessRegistrationNumber`: Alphanumeric, max 50 chars.

2. **`PUT /api/v1/vendors/me/kyc`**:
   - `documentUrl`: Required, valid URL or internal storage key pointing to an uploaded document (`.pdf`, `.jpg`, `.jpeg`, `.png`).
   - `gstNumber`: Strict 15-character Indian GSTIN regex: `^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$`.
   - `panNumber`: Strict 10-character Indian PAN regex: `^[A-Z]{5}[0-9]{4}[A-Z]{1}$`.
   - State Constraint: Vendor cannot re-submit KYC if current status is already `APPROVED` without admin intervention.

3. **`PUT /api/v1/vendors/me/bank-account`**:
   - `accountNumber`: Required, numeric string between 9 and 18 digits (`^[0-9]{9,18}$`). Currently any string is accepted.
   - `ifsc`: Required, exact 11-character Indian IFSC regex: `^[A-Z]{4}0[A-Z0-9]{6}$`.
   - `holderName`: Required, 2-100 characters, letters and spaces only.

---

### Module 4: Facility (`internal/facility`)

#### Current State
- Basic type checks (`HOTEL` or `HALL`), star rating (1-5), and negative base price check.

#### Missing & Required Validations
1. **`POST /api/v1/facilities` & `PUT /api/v1/hotels/{id}` & `PUT /api/v1/halls/{id}`**:
   - `name`: Required, 3 to 255 characters.
   - `type`: Must be `HOTEL` or `MARRIAGE_HALL` (or `HALL`).
   - `lat` & `lng`:
     - `lat`: Must be between `-90.0` and `+90.0`.
     - `lng`: Must be between `-180.0` and `+180.0`.
     - (Currently entirely unconstrained, which breaks geo-spatial distance calculations).
   - `zipcode`: 6-digit PIN code (`^[1-9][0-9]{5}$`) or international postal pattern (max 20 chars).
   - `checkInTime` & `checkOutTime`: Format must match `HH:MM`. Cannot be equal to each other.
   - `capacityPax`: Positive integer (`1` to `50,000`).
   - `seatingCapacity` & `floatingCapacity`:
     - Positive integers.
     - Business logic check: `seatingCapacity` must not exceed `floatingCapacity` or `capacityPax`.
   - `areaSqft`: Positive integer (`50` to `500,000`).
   - `basePricePerDay`: Float >= 0.00, max cap 100,000,000.
   - `minBookingSize`: Integer >= 1.

2. **`GET /api/v1/facilities` (Catalog Query)**:
   - `page`: Integer >= 0.
   - `size`: Integer between 1 and 100 (prevent unbounded full-table memory dump).
   - `minPrice` & `maxPrice`: Floats >= 0; `minPrice <= maxPrice`.
   - `rating`: Integer between 1 and 5.
   - `sortBy`: Allowlist validation: `price_asc`, `price_desc`, `rating_desc`, `newest`, `popularity`.

3. **`POST /api/v1/facilities/{id}/cancellation-policies`**:
   - `daysBeforeEvent`: Integer >= 0.
   - `refundPercentage`: Float between `0.00` and `100.00`.
   - `policyType`: Allowlist (`FLEXIBLE`, `MODERATE`, `STRICT`, `CUSTOM`).
   - Duplicate Check: Cannot add duplicate `daysBeforeEvent` tier for the same venue.

4. **`POST /api/v1/facilities/{id}/images` & `/videos`**:
   - File Size Caps: Images <= 10MB; Videos <= 200MB.
   - Magic Byte MIME Sniffing: Images must be `image/jpeg`, `image/png`, or `image/webp`. Videos must be `video/mp4` or `video/quicktime`.
   - Venue Image Quota: Limit maximum 50 images per facility to prevent storage exhaustion.

5. **`GET /api/v1/facilities/compare`**:
   - `ids`: Query string must contain between 2 and 4 comma-separated valid UUIDs. Comparing 1 venue or >4 venues must be rejected with 400.

6. **`POST /api/v1/facilities/{id}/faqs`**:
   - `question`: Required, 5 to 500 characters.
   - `answer`: Required, 5 to 2000 characters.

7. **`PUT /api/v1/facilities/{id}/events`**:
   - `events`: Array of strings. Every entry must exist in the master event catalogue (`GET /api/v1/events`). Max 30 event types.

---

### Module 5: Booking (`internal/booking`)

#### Current State
- Validates date formats (`YYYY-MM-DD`), slot enums (`MORNING`, `EVENING`, `FULL_DAY`), positive guest counts, and room counts (0-1000).

#### Missing & Required Validations
1. **`POST /api/v1/bookings/halls`**:
   - `startDate`: Cannot be in the past (`startDate >= currentDate`). Max lead time: cannot book more than 730 days (2 years) in advance.
   - `endDate`: `endDate >= startDate`. Event duration cannot exceed 30 consecutive days for a single booking.
   - `startTime` & `endTime`: If provided, must both be `HH:MM`. `endTime` must be logically after `startTime` (unless crossing midnight).
   - `guestCount`: Must not exceed the venue's `capacityPax` or `floatingCapacity` by more than a 10% buffer.
   - `idempotentKey`: Required UUID string to prevent double billing on network retry.
   - `packageIds`: Array of valid UUIDs belonging specifically to this facility.

2. **`POST /api/v1/bookings/hotels`**:
   - `checkIn` & `checkOut`: Valid `YYYY-MM-DD`. `checkIn >= today`. `checkOut > checkIn`. Max stay duration <= 30 days.
   - `rooms`: Non-empty array.
   - `rooms[].roomTypeId`: Valid UUID, must exist for this hotel.
   - `rooms[].quantity`: Integer >= 1, max 50 rooms per booking request.

3. **`POST /api/v1/bookings/{id}/cancel`**:
   - `{id}`: Valid UUID.
   - State Validation: Booking must be in `PENDING` or `CONFIRMED` status. Cannot cancel `COMPLETED` or already `CANCELLED` bookings.
   - `reason`: Optional string, max 500 characters.

4. **`POST /api/v1/bookings/quote` (Pricing Preview)**:
   - Enforce identical date and time validations as `createHall` (dates in future, valid format, valid UUID).

---

### Module 6: Quote Negotiation (`internal/quote`)

#### Current State
- Checks quote UUID validity and loads ownership context.

#### Missing & Required Validations
1. **`POST /api/v1/quotes/request`**:
   - `facilityId`: Required, valid UUID.
   - `eventDate`: Required, `YYYY-MM-DD`, must be in the future.
   - `endDate`: Optional, `YYYY-MM-DD`, `endDate >= eventDate`.
   - `guestCount`: Positive integer (`1` to `50,000`).
   - `requirements` / `note`: Max 2000 characters.

2. **`POST /api/v1/quotes/{id}/reply` (Owner Offer)**:
   - State Validation: Quote must be in `REQUESTED` or `COUNTERED` status. Cannot reply to `EXPIRED`, `ACCEPTED`, or `REJECTED` quotes.
   - `lineItems`: Array with at least 1 item and at most 50 items.
   - `lineItems[].description`: Required, 2 to 255 characters.
   - `lineItems[].amount`: Float > 0.00.
   - `lineItems[].quantity`: Integer >= 1.
   - `validUntil`: Must be an ISO 8601 timestamp in the future and prior to `eventDate`.
   - `totalAmount`: Must accurately equal the sum of line items + taxes/fees.

3. **`POST /api/v1/quotes/{id}/counter` (Customer / Owner Counter)**:
   - State Validation: Allowed only on `OFFERED` or `COUNTERED` status.
   - `proposedAmount`: Float > 0.00, maximum 100,000,000.
   - `counterNote`: Required or optional string, max 1000 characters.

4. **`POST /api/v1/quotes/{id}/accept`**:
   - State & Time Validation: Quote status must be `OFFERED` or `COUNTERED`. `validUntil` must not have passed (`validUntil > NOW()`).

5. **`POST /api/v1/quotes/{id}/convert-to-booking`**:
   - State Validation: Status MUST be `ACCEPTED`.
   - Real-time Slot Availability: Pre-check that the requested date and slot have not been booked by another customer in the interim.

6. **`POST /api/v1/quotes/{id}/messages`**:
   - `message`: Required, 1 to 2000 characters after whitespace trimming. Empty or blank strings rejected.

7. **`POST /api/v1/quotes/{id}/attachments`**:
   - File formats allowed: `.pdf`, `.jpg`, `.jpeg`, `.png`. Max file size 20MB.

---

### Module 7: Payment & Refunds (`internal/payment`)

#### Current State
- Basic UUID checks, signature verification on webhook, and positive refund amount.

#### Missing & Required Validations
1. **`POST /api/v1/payments/create`**:
   - `bookingId`: Required, valid UUID.
   - State Validation: Booking must be in `PENDING` payment state. Cannot create payment for `CONFIRMED`, `CANCELLED`, or `COMPLETED` bookings.
   - Amount Reconciliation: Internal calculation check confirming that the requested charge matches the calculated booking total minus any applied coupons.

2. **`POST /api/v1/payments/webhook`**:
   - Header `X-Signature`: Required, non-empty hexadecimal HMAC string.
   - Payload Validation: JSON must include required gateway fields: `event`, `paymentId`, `orderId`, `status`.
   - Status Allowlist: Gateway status must map to known statuses (`SUCCESS`, `FAILED`, `PENDING`).
   - Idempotency Validation: Webhook event ID must be de-duplicated to prevent processing the same payment twice.

3. **`POST /api/v1/refunds/{paymentId}`**:
   - `{paymentId}`: Required, valid UUID.
   - `amount`: Query param or body float > 0.00.
   - Balance Check: `amount` cannot exceed `original_payment_amount - already_refunded_amount`.
   - `reason`: Max 255 characters.

---

### Module 8: Coupons (`internal/coupon`)

#### Current State
- Validates code presence, percent range (0-100), flat discount > 0, and date parsing.

#### Missing & Required Validations
1. **`POST /api/v1/coupons` & `PUT /api/v1/coupons/{id}`**:
   - `code`: Uppercase alphanumeric pattern `^[A-Z0-9_\-]{3,30}$`. No spaces or lowercase letters.
   - `discountType`: Enum `PERCENT` or `FLAT`.
   - `discountValue`:
     - If `PERCENT`: Must be between `0.01` and `100.00`.
     - If `FLAT`: Float > `0.00`, max `1,000,000.00`.
   - `maxDiscount`: If `discountType == PERCENT`, `maxDiscount` must be required and > `0.00` to prevent unbounded discounts on luxury bookings.
   - `minBookingAmount`: Optional float >= `0.00`.
   - `validFrom` & `validUntil`: `validUntil > validFrom`. Max lifespan: 365 days.
   - `usageLimit`: If provided, integer >= `1`.
   - `facilityId`: If provided, valid UUID. Vendor creating coupon must own the facility.

2. **`POST /api/v1/coupons/validate`**:
   - `code`: Required string, max 30 chars.
   - `facilityId`: Required, valid UUID.
   - `bookingAmount`: Float > 0.00.
   - Pre-conditions: Current time within `validFrom` - `validUntil`; `times_used < usage_limit`; `bookingAmount >= minBookingAmount`.

---

### Module 9: Review & Rating (`internal/review`)

#### Current State
- Checks facility UUID, rating between 1 and 5, and verified booking existence.

#### Missing & Required Validations
1. **`POST /api/v1/reviews`**:
   - `facilityId`: Required, valid UUID.
   - `rating`: Integer between 1 and 5.
   - `title`: Optional, max 100 characters. Trim whitespace.
   - `comment`: Optional, max 2000 characters. Reject empty strings if provided.
   - Eligibility & Uniqueness: User must have at least one booking with status `COMPLETED` (or `CONFIRMED`). User cannot review the same facility more than once unless authorized for multiple completed visits.

2. **`GET /api/v1/reviews/facility/{facilityId}`**:
   - `page`: Integer >= 0.
   - `size`: Integer between 1 and 50.
   - `sort`: Allowlist: `newest`, `oldest`, `highest_rating`, `lowest_rating`.

3. **`POST /api/v1/admin/reviews` & `PUT /api/v1/admin/reviews/{id}`**:
   - `rating`: Integer between 1 and 5.
   - `userId`: Must be a valid existing user ID.
   - `status`: Allowlist: `PENDING`, `APPROVED`, `REJECTED`.

---

### Module 10: Feedback (`internal/feedback`)

#### Current State
- File attachment size cap (10MB) and allowed image MIME types.

#### Missing & Required Validations
1. **`POST /api/v1/feedback`**:
   - `message`: Required, min 5, max 2000 characters after whitespace trimming.
   - `rating`: Optional integer between 1 and 5.
   - `platform`: Optional enum allowlist: `IOS`, `ANDROID`, `WEB`, `MOBILE_WEB`.
   - `appVersion`: Optional string, max 20 characters (semver pattern e.g. `1.4.2`).
   - `attachment`: Max 10MB; MIME types `image/jpeg`, `image/png`, `image/webp`.

2. **`PUT /api/v1/admin/feedback/{id}`**:
   - `status`: Required enum allowlist: `NEW`, `IN_REVIEW`, `RESOLVED`, `IGNORED`.
   - `adminNote`: Optional string, max 1000 characters.

---

### Module 11: Support & Helpline (`internal/support`)

#### Current State
- Key/value persistence without field validation on admin update.

#### Missing & Required Validations
1. **`PUT /api/v1/admin/support`**:
   - `supportEmail`: Optional, must be valid RFC 5322 email format, max 255 chars.
   - `supportPhone`: Optional, E.164 phone format (`^\+?[1-9]\d{9,14}$`), max 20 chars.
   - `supportWhatsapp`: Optional, E.164 phone format, max 20 chars.
   - `supportHours`: Optional string, max 100 characters (e.g. `Mon-Sat 09:00 - 20:00 IST`).
   - `supportAddress`: Optional string, max 500 characters.

---

### Module 12: Notification & Devices (`internal/notification`)

#### Current State
- Basic device registration.

#### Missing & Required Validations
1. **`POST /api/v1/devices`**:
   - `deviceToken`: Required string, min 10, max 500 characters (FCM / APNs token).
   - `platform`: Required enum: `ANDROID`, `IOS`, `WEB`.

2. **`PUT /api/v1/users/me/location`**:
   - `latitude`: Required float between `-90.0000000` and `+90.0000000`.
   - `longitude`: Required float between `-180.0000000` and `+180.0000000`.
   - (Currently unchecked, allowing illegal latitude/longitude coordinates into user profiles).

3. **`PUT /api/v1/users/me/geo-notifications`**:
   - `enabled`: Required boolean (`true` or `false`).

4. **`GET /ack/{token}` & `GET /decline/{token}`**:
   - `{token}`: Required alphanumeric token (32 to 64 chars). Expired or already consumed tokens must display a user-friendly expired page.

---

### Module 13: Search (`internal/search`)

#### Current State
- Accepts query parameters with basic parsing.

#### Missing & Required Validations
1. **`GET /api/v1/search/venues`**:
   - `q`: Query string max 100 characters. Strip SQL / punctuation wildcards (`%`, `_`).
   - `city`: Max 100 characters.
   - `lat` & `lng`: If one is provided, both must be provided. `-90 <= lat <= 90`, `-180 <= lng <= 180`.
   - `radiusKm`: Float between `1.0` and `500.0`. Default 50km.
   - `type`: Allowlist `HOTEL`, `MARRIAGE_HALL`, `ALL`.
   - `minPrice` & `maxPrice`: Floats >= `0.00`. `minPrice <= maxPrice`.
   - `capacity`: Integer >= `1`.
   - `date`: `YYYY-MM-DD`, must be `>= today`.
   - `limit`: Integer between `1` and `100` (default 20).
   - `offset`: Integer >= `0`.

2. **`POST /api/v1/search/venues/{id}/view`**:
   - `{id}`: Valid UUID.
   - Rate limit check to prevent bots from artificially inflating venue view counters.

---

### Module 14: Admin & Backoffice (`internal/admin`)

#### Current State
- Role verification (`ROLE_ADMIN`) present on all endpoints.

#### Missing & Required Validations
1. **`POST /api/v1/admin/users` (Admin Create User)**:
   - `fullName`: 2-100 characters.
   - `email`: Valid email format, max 255 chars.
   - `phoneNumber`: Valid E.164 phone format, max 20 chars.
   - `password`: Min 8, max 100 characters.
   - `roles`: Non-empty array of valid roles: `ROLE_USER`, `ROLE_HALL_OWNER`, `ROLE_ADMIN`.

2. **`POST /api/v1/admin/block`**:
   - `userId`: Positive int64, must exist.
   - Self-Block Prevention: Admin cannot block their own user account (`userId != callerAdminId`).
   - `reason`: Required string, 5 to 500 characters.

3. **`POST /api/v1/admin/vendors/{vendorId}/kyc/approve` & `/reject`**:
   - `{vendorId}`: Valid UUID.
   - On Reject: `rejectionReason` is required, min 5, max 500 characters.

4. **`PATCH /api/v1/admin/facilities/{id}`**:
   - For every provided non-null field, apply the standard facility rules (e.g. `lat` between -90 and 90, `basePricePerDay >= 0`, `starRating` between 1 and 5).

5. **`POST /api/v1/admin/fraud-reports`**:
   - `targetType`: Required enum: `USER`, `VENDOR`, `FACILITY`, `BOOKING`.
   - `targetId`: Required non-empty string / UUID.
   - `reason`: Required string, 5 to 1000 characters.

6. **`POST /api/v1/admin/fraud-reports/{reportId}/resolve`**:
   - `resolution`: Required string, 5 to 1000 characters.
   - `status`: Enum allowlist: `RESOLVED`, `DISMISSED`.

---

## 4. Master Validation Matrix (Prioritized)

| Priority | Module | Endpoint | Target Fields | Rule / Constraint |
|:---:|:---|:---|:---|:---|
| **P0** | **Payment** | `POST /api/v1/refunds/{paymentId}` | `amount` | Amount must be > 0 and <= (paid - refunded) |
| **P0** | **Booking** | `POST /api/v1/bookings/halls` | `startDate`, `endDate` | `startDate >= today`, `endDate >= startDate` |
| **P0** | **Booking** | `POST /api/v1/bookings/hotels` | `checkIn`, `checkOut` | `checkIn >= today`, `checkOut > checkIn` |
| **P0** | **Vendor** | `PUT /api/v1/vendors/me/bank-account` | `accountNumber`, `ifsc` | Numeric 9-18 digits; IFSC `^[A-Z]{4}0[A-Z0-9]{6}$` |
| **P0** | **Vendor** | `PUT /api/v1/vendors/me/kyc` | `gstNumber`, `panNumber` | Strict 15-char GSTIN and 10-char PAN regex |
| **P1** | **Facility** | `POST /api/v1/facilities` | `lat`, `lng` | `-90 <= lat <= 90`, `-180 <= lng <= 180` |
| **P1** | **Facility** | `POST /api/v1/facilities` | `basePricePerDay` | Base price >= 0 |
| **P1** | **Coupon** | `POST /api/v1/coupons` | `code`, `discountValue` | Alphanumeric uppercase; % between 0.01 and 100 |
| **P1** | **Auth** | `POST /api/v1/auth/register/verify-*` | `otpCode` | Exact 6-digit numeric `^[0-9]{6}$` |
| **P1** | **Quote** | `POST /api/v1/quotes/{id}/reply` | `lineItems`, `validUntil` | Items > 0, amounts > 0, `validUntil > now` |
| **P1** | **Notification** | `PUT /api/v1/users/me/location` | `latitude`, `longitude` | Valid geo coordinate ranges |
| **P2** | **Review** | `POST /api/v1/reviews` | `rating`, `comment` | Rating 1-5, comment max 2000 chars |
| **P2** | **Feedback** | `POST /api/v1/feedback` | `message`, `attachment` | 5-2000 chars, MIME sniff, max 10MB |
| **P2** | **Search** | `GET /api/v1/search/venues` | `limit`, `radiusKm` | Limit 1-100, radius 1-500 km |
| **P2** | **Facility** | `GET /api/v1/facilities/compare` | `ids` | Exactly 2 to 4 comma-separated UUIDs |
| **P3** | **Support** | `PUT /api/v1/admin/support` | `supportEmail`, `supportPhone` | RFC email & E.164 phone formats |
| **P3** | **User** | `PUT /api/v1/users/me` | `firstName`, `bio` | 1-100 chars, bio max 500 chars |

---

## 5. Implementation Strategy (For Future Reference)

When ready to implement, expand `pkg/validate` with reusable helper methods without introducing heavyweight external dependencies:

```go
// Recommended additions to pkg/validate/validate.go:
func (e *Errors) RangeFloat(field string, value, min, max float64)
func (e *Errors) RangeInt(field string, value, min, max int)
func (e *Errors) Coordinates(lat, lng *float64)
func (e *Errors) IFSC(field string, value *string)
func (e *Errors) BankAccount(field string, value *string)
func (e *Errors) DateFuture(field string, t time.Time)
func (e *Errors) DateOrder(startField string, start time.Time, endField string, end time.Time)
func (e *Errors) Enum(field, value string, allowed []string)
```
