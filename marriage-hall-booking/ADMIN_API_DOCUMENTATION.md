# ApnaVenue / Marriage Hall Booking — Admin APIs Documentation

All endpoints in this document require an **Admin Bearer Token** in the authorization header:
```http
Authorization: Bearer <ADMIN_JWT_TOKEN>
```
*(Token must have `ROLE_ADMIN` or `ROLE_SUPER_ADMIN` authority).*

---

## 📑 Table of Contents
1. [Dashboard & Analytics](#1-dashboard--analytics)
2. [User Management](#2-user-management)
3. [Vendor & KYC Verification](#3-vendor--kyc-verification)
4. [Venue / Facility Management & Approvals](#4-venue--facility-management--approvals)
5. [Ratings & Reviews Moderation](#5-ratings--reviews-moderation)
6. [Platform Coupons Management](#6-platform-coupons-management)
7. [App Feedback & Triage](#7-app-feedback--triage)
8. [Platform FAQs Management](#8-platform-faqs-management)
9. [Contact Us Inquiries](#9-contact-us-inquiries)
10. [Privacy Policy Versioning & Management](#10-privacy-policy-versioning--management)
11. [Customer Support & Helpline Settings](#11-customer-support--helpline-settings)
12. [Payment & Cancellation Policy Settings](#12-payment--cancellation-policy-settings)
13. [Refunds & Booking Overrides](#13-refunds--booking-overrides)
14. [Fraud Reports](#14-fraud-reports)

---

## 1. Dashboard & Analytics

### `GET /api/v1/admin/dashboard`
* **Use / Purpose:** Admin dashboard overview stats display karne ke liye (Total Users, Total Hotels, Total Marriage Halls, Total Bookings, and Total Platform Revenue).
* **Response Example:**
  ```json
  {
    "success": true,
    "message": "Dashboard data retrieved",
    "data": {
      "totalUsers": 1250,
      "totalHotels": 45,
      "totalMarriageHalls": 110,
      "totalBookings": 340,
      "totalRevenue": 2850000.00
    }
  }
  ```

### `GET /api/v1/admin/analytics/decisions`
* **Use / Purpose:** Admin approval/rejection decision trends aur audit statistics track karne ke liye.

---

## 2. User Management

### `GET /api/v1/admin/users`
* **Use / Purpose:** Platform ke sabhi registered users ki list (Customers, Vendors, Admins) pagination aur filter ke sath dekhne ke liye.
* **Query Params:** `?page=1&limit=20&role=ROLE_CUSTOMER&status=ACTIVE`

### `POST /api/v1/admin/users`
* **Use / Purpose:** Admin panel se manually naya staff, vendor ya admin create karne ke liye.
* **Request Body:**
  ```json
  {
    "email": "staff@apnavenue.com",
    "password": "SecurePassword123",
    "firstName": "Rahul",
    "lastName": "Sharma",
    "phoneNumber": "9876543210",
    "roles": ["ROLE_STAFF"]
  }
  ```

### `PUT /api/v1/admin/users/{id}`
* **Use / Purpose:** Kisi existing user ki profile details, phone number ya roles update karne ke liye.

### `DELETE /api/v1/admin/users/{id}`
* **Use / Purpose:** Kisi account ko soft-delete ya remove karne ke liye.

### `POST /api/v1/admin/block`
* **Use / Purpose:** Kisi suspicious/fraudulent user ya vendor ko platform se block/unblock karne ke liye.
* **Request Body:**
  ```json
  {
    "userId": 123,
    "blocked": true,
    "reason": "Suspicious fraudulent booking activity"
  }
  ```

---

## 3. Vendor & KYC Verification

### `GET /api/v1/admin/vendors`
* **Use / Purpose:** Sabhi registered vendors aur unke KYC verification status dekhne ke liye.
* **Query Params:** `?status=PENDING&page=1&limit=20`

### `GET /api/v1/admin/vendors/{vendorId}`
* **Use / Purpose:** Ek specific vendor ki full details, business profile aur uploaded KYC documents dekhne ke liye.

### `POST /api/v1/admin/vendors/{vendorId}/kyc/approve`
* **Use / Purpose:** Vendor ke KYC documents verify karke usko officially approve karne ke liye, jisse wo platform par venues list kar sake.
* **Request Body:**
  ```json
  {
    "notes": "All identity and tax documents verified successfully"
  }
  ```

### `POST /api/v1/admin/vendors/{vendorId}/kyc/reject`
* **Use / Purpose:** Vendor ki KYC application reject karne ke liye sath hi reason provide karna (Vendor ko notification jata hai).
* **Request Body:**
  ```json
  {
    "reason": "GST document was blurry and expired. Please upload valid certificate."
  }
  ```

---

## 4. Venue / Facility Management & Approvals

### `GET /api/v1/admin/facilities`
* **Use / Purpose:** Sabhi facilities ki list (PENDING, APPROVED, REJECTED, BLOCKED) dekhne ke liye.
* **Query Params:** `?status=PENDING&type=MARRIAGE_HALL&page=1&limit=20`

### `GET /api/v1/admin/facilities/{id}`
* **Use / Purpose:** Kisi venue ka single-call full overview (Facility, Packages, Pricing, Amenities, Images, Reviews aur Owner Details).

### `POST /api/v1/admin/facilities/{id}/approve`
* **Use / Purpose:** Vendor dwara create ki gayi nayi facility ko inspect karke public live listing ke liye approve karna.

### `PATCH /api/v1/admin/facilities/{id}`
* **Use / Purpose:** Facility ke selected fields bina poora record overwrite kiye edit karna (e.g. status change, contact phone, base price, featured flag).
* **Request Body:**
  ```json
  {
    "name": "Grand Royal Palace",
    "contactPhone": "+919876543210",
    "basePricePerDay": 120000,
    "isFeatured": true,
    "status": "APPROVED"
  }
  ```

### `PUT /api/v1/admin/facilities/{id}/rating`
* **Use / Purpose:** Imported ya offline venues ke rating aur review count ko manual set/pin karna (taaki naye review aane se rating wipe na ho).
* **Request Body:**
  ```json
  {
    "avgRating": 4.5,
    "reviewCount": 180
  }
  ```

### `DELETE /api/v1/admin/facilities/{id}/rating`
* **Use / Purpose:** Manually pinned rating ko unpin karke actual platform reviews se automatic recompute karna.

---

## 5. Ratings & Reviews Moderation

### `GET /api/v1/admin/reviews`
* **Use / Purpose:** Platform ke sabhi customer reviews ko list karna moderation ke liye.
* **Query Params:** `?status=PENDING&facilityId=UUID`

### `POST /api/v1/admin/reviews`
* **Use / Purpose:** Admin dwara direct seed review post karna (bina booking constraint ke).

### `PUT /api/v1/admin/reviews/{id}`
* **Use / Purpose:** Kisi abusive ya misleading review ke text, title ya rating ko edit/correct karna.

### `PATCH /api/v1/admin/reviews/{id}/approve`
* **Use / Purpose:** Submitted review ko approve karke venue page par live karna.

### `PATCH /api/v1/admin/reviews/{id}/reject`
* **Use / Purpose:** Offensive ya fake review ko reject/hide karna.

### `DELETE /api/v1/admin/reviews/{id}`
* **Use / Purpose:** Spam ya inappropriate review ko permanently delete karna.

---

## 6. Platform Coupons Management

### `GET /api/v1/admin/coupons`
* **Use / Purpose:** Admin level ke global promotional coupons ki list dekhna.

### `POST /api/v1/admin/coupons`
* **Use / Purpose:** Global/Platform-wide discount coupon create karna jo Marriage Halls / Hotels par chal sake.
* **Request Body:**
  ```json
  {
    "code": "DIWALI2026",
    "title": "Diwali Festival Bonanza",
    "discountType": "PERCENTAGE",
    "discountValue": 15,
    "maxDiscountAmount": 5000,
    "minBookingAmount": 25000,
    "appliesTo": "ALL",
    "validFrom": "2026-10-01T00:00:00Z",
    "validUntil": "2026-11-30T23:59:59Z",
    "usageLimit": 500
  }
  ```

### `PUT /api/v1/admin/coupons/{id}`
* **Use / Purpose:** Platform coupon ki validity, percentage ya limit update karna.

### `DELETE /api/v1/admin/coupons/{id}`
* **Use / Purpose:** Active promotional coupon ko deactivate/remove karna.

---

## 7. App Feedback & Triage

### `GET /api/v1/admin/feedback`
* **Use / Purpose:** Users aur mobile app se aayi hui rating aur feedback list dekhna.
* **Query Params:** `?page=1&limit=20&status=PENDING`

### `GET /api/v1/admin/feedback/summary`
* **Use / Purpose:** Feedback analytics summary (Average App Rating, Net Promoter Score, Category breakdown).

### `GET /api/v1/admin/feedback/{id}`
* **Use / Purpose:** Kisi feedback ka full message aur uploaded screenshot view karna.

### `PUT /api/v1/admin/feedback/{id}`
* **Use / Purpose:** Feedback ka status update karna (`RESOLVED`, `INVESTIGATING`, `CLOSED`) aur internal notes add karna.
* **Request Body:**
  ```json
  {
    "status": "RESOLVED",
    "adminNotes": "Bug reported was fixed in mobile app release v1.4.2"
  }
  ```

---

## 8. Platform FAQs Management

### `GET /api/v1/admin/faqs`
* **Use / Purpose:** Platform ke sabhi FAQs (General, Booking, Payment, Vendor) manage karne ke liye list karna.

### `POST /api/v1/admin/faqs`
* **Use / Purpose:** Help center ya app FAQ section ke liye naya FAQ add karna.
* **Request Body:**
  ```json
  {
    "question": "What is the advance booking cancellation policy?",
    "answer": "Bookings can be cancelled up to 7 days before event start for a full refund minus processing fee.",
    "category": "CANCELLATION",
    "displayOrder": 1,
    "isActive": true
  }
  ```

### `GET /api/v1/admin/faqs/{faqId}`
* **Use / Purpose:** Kisi single FAQ ki details fetch karna.

### `PUT /api/v1/admin/faqs/{faqId}`
* **Use / Purpose:** FAQ ke text, display order ya active status ko modify karna.

### `DELETE /api/v1/admin/faqs/{faqId}`
* **Use / Purpose:** Obsolete FAQ ko delete karna.

---

## 9. Contact Us Inquiries

### `GET /api/v1/contact-us`
* **Use / Purpose:** Website aur app ke "Contact Us" form se aayi queries aur leads dekhna.

### `GET /api/v1/contact-us/{id}`
* **Use / Purpose:** Specific inquiry ka user email, phone aur inquiry message read karna.

### `PUT /api/v1/contact-us/{id}`
* **Use / Purpose:** Inquiry ka resolution status mark karna (`RESOLVED`, `IN_PROGRESS`, `PENDING`).

### `DELETE /api/v1/contact-us/{id}`
* **Use / Purpose:** Spam contact submission ko delete karna.

---

## 10. Privacy Policy Versioning & Management

### `GET /api/v1/admin/privacy-policy`
* **Use / Purpose:** Sabhi drafted aur past privacy policy versions dekhna.

### `POST /api/v1/admin/privacy-policy`
* **Use / Purpose:** Naya Privacy Policy draft content aur version create karna.
* **Request Body:**
  ```json
  {
    "version": "2.0",
    "content": "<h1>Privacy Policy v2.0</h1><p>Updated terms on user data protection...</p>",
    "effectiveFrom": "2026-12-01T00:00:00Z"
  }
  ```

### `GET /api/v1/admin/privacy-policy/{id}`
* **Use / Purpose:** Specific policy version ka HTML content view karna.

### `PUT /api/v1/admin/privacy-policy/{id}`
* **Use / Purpose:** Policy draft ke text content ko edit karna.

### `PATCH /api/v1/admin/privacy-policy/{id}/activate`
* **Use / Purpose:** Naye policy version ko **ACTIVE** mark karna (Purana active version automatically deactivate ho jata hai).

### `PATCH /api/v1/admin/privacy-policy/{id}/deactivate`
* **Use / Purpose:** Active policy version ko deactivate karna.

---

## 11. Customer Support & Helpline Settings

### `GET /api/v1/admin/support`
* **Use / Purpose:** App me display hone wale official support contact channels (Email, Phone, WhatsApp, Hours, Address) aur audit timestamps fetch karna.

### `PUT /api/v1/admin/support`
* **Use / Purpose:** Customer support phone number, helpline WhatsApp ya support email live update karna.
* **Request Body:**
  ```json
  {
    "supportEmail": "support@apnavenue.com",
    "supportPhone": "+918001234567",
    "supportWhatsapp": "+918001234567",
    "supportHours": "9:00 AM - 9:00 PM (Mon-Sun)",
    "supportAddress": "123 Tech Park, MG Road, Bengaluru"
  }
  ```

---

## 12. Payment & Cancellation Policy Settings

### `PUT /api/v1/payment-policies`
* **Use / Purpose:** Poore platform ke liye **Marketplace Default Payment Policy** set karna (Advance deposit %, balance due hours, cancellation refund rules).
* **Request Body:**
  ```json
  {
    "advancePercent": 50,
    "minAdvanceAmount": 2000,
    "maxAdvanceAmount": 100000,
    "balanceDueHoursBeforeEvent": 72,
    "cancellationWindowHours": 168,
    "cancellationPenaltyPercent": 25
  }
  ```

### `PUT /api/v1/facilities/{facilityId}/payment-policy`
* **Use / Purpose:** Kisi specific venue ke liye custom payment/cancellation policy override set karna.

---

## 13. Refunds & Booking Overrides

### `POST /api/v1/refunds/{paymentId}`
* **Use / Purpose:** Successful payment par partial ya full refund initiate karna.
* **Request Body:**
  ```json
  {
    "amount": 5000,
    "reason": "Customer cancellation as per policy"
  }
  ```

### `POST /api/v1/bookings/{id}/accept` & `POST /api/v1/bookings/{id}/reject`
* **Use / Purpose:** Agar vendor respond na kare ya dispute ho, to admin ke paas booking ko override approve ya reject karne ka access hota hai.

---

## 14. Fraud Reports

### `POST /api/v1/admin/fraud-reports`
* **Use / Purpose:** Fraud flag report log karna kisi user, booking ya vendor ke against.

### `POST /api/v1/admin/fraud-reports/{reportId}/resolve`
* **Use / Purpose:** Fraud report investigation ko review karke RESOLVED mark karna sath hi resolution notes likhna.
