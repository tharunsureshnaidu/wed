# 🏰 Marriage Hall & Hotel Booking Platform
## Comprehensive Product & Business Overview (Executive & Non-Technical Edition)

---

## 📌 1. Executive Summary

The **Marriage Hall & Hotel Booking Platform** is an all-in-one digital marketplace designed to streamline how event organizers and families discover, negotiate, and book wedding venues, banquet halls, lawns, and hotels.

Much like *Airbnb* revolutionized vacation stays and *MakeMyTrip* transformed travel bookings, this platform digitalizes the multi-billion dollar wedding and event venue industry. It replaces time-consuming physical visits, opaque pricing, and manual pen-and-paper registers with a transparent, self-service online ecosystem.

### 🎯 Core Value Proposition:
1. **For Customers (Event Hosts & Families):** Discover certified halls and hotels based on location, budget, capacity, and amenities; take virtual photo/video tours; negotiate custom quotes; and securely book dates with flexible advance payments from home.
2. **For Venue Owners (Vendors):** Digitize property listings, showcase facilities with rich media, manage slot availability in real time, review customer booking inquiries, enforce custom advance/cancellation policies, and grow event revenue.
3. **For Platform Administrators:** Maintain marketplace quality, prevent double-booking fraud, verify listings, run promotional discount campaigns, and oversee revenue and disputes across the platform.

---

## 👥 2. User Roles & Ecosystem

The platform connects four key stakeholders within a single, secure environment:

```
                  ┌───────────────────────────────────────────────┐
                  │       Marriage Hall & Hotel Platform          │
                  └───────┬──────────────┬─────────────┬──────────┘
                          │              │             │
              ┌───────────▼──┐   ┌───────▼────┐   ┌────▼──────────┐
              │   Customer   │   │ Venue Owner│   │ Administrator │
              │  (Event Host)│   │  (Vendor)  │   │ (Super Admin) │
              └──────────────┘   └────────────┘   └───────────────┘
```

1. **Customer (End User):**
   - Individual seeking a venue for weddings, receptions, engagements, birthday celebrations, or corporate gatherings.
   - Can search properties, compare options side-by-side, request bespoke quotes, make advance payments, and review venues.

2. **Venue Owner / Vendor (Hall & Hotel Managers):**
   - Commercial operators managing marriage halls, convention centers, banquets, or hotels.
   - Responsible for listing venue details, rooms, packages, pricing, accepting/rejecting booking requests, and managing availability.

3. **Platform Administrator:**
   - Operational manager responsible for reviewing and approving new venue listings, moderating reviews, verifying vendor KYC documents, and managing FAQs.

4. **Super Administrator:**
   - Executive head with complete platform authority: configuring global advance payment policies, launching platform-wide discount codes, auditing financial transactions, and managing user roles.

---

## 🚀 3. Key Business Modules & Capabilities

---

### 3.1. User Account Management & Bank-Grade Security
- **OTP Verification (Email & Mobile):** Every registration requires a verified one-time password (OTP), eliminating fake accounts and spam bookings.
- **Secure Authentication:** Passwords are encrypted using enterprise-grade algorithms (BCrypt).
- **Session Protection:** Automated token expiration and multi-device session control (ability to log out from all devices in a single click).
- **Self-Service Password Recovery:** Secure password reset links sent via verified email/SMS channels.

---

### 3.2. Venue Discovery, Smart Search & Comparison
- **Smart City & Landmark Search:** Find venues by city, locality, or landmark with intelligent typo tolerance (e.g., searching "palce" automatically finds "Grand Palace").
- **GPS-Based Proximity Radius:** Automatically detects customer distance from venues to prioritize nearby options within a configurable radius (e.g., within 50 km).
- **Comprehensive Filters:**
  - *Guest Capacity:* Narrow down venues accommodating 50 to 5,000+ guests.
  - *Budget / Price Range:* Filter by total rental cost or per-plate catering budget.
  - *Venue Category:* Focus on Marriage Halls, Banquet Halls, Lawns, or Hotels.
  - *Amenities:* Air Conditioning, Valet Parking, Dedicated Bridal Suites, Backup Power Generators, In-House Catering, DJ/Sound Setup, Swimming Pool, and Room Counts.
- **Rich Media Showcases:** High-definition photo galleries and video walk-throughs allowing virtual inspections without physical travel.
- **Side-by-Side Comparison Engine:** Compare up to 3 venues simultaneously across guest capacity, per-day rates, available amenities, and guest ratings.

---

### 3.3. High-Reliability Booking Engine
The booking system is built to eliminate the single biggest risk in event management: **Double Booking**.
- **Real-Time Slot Locking:** When a customer initiates a booking for a specific date or session (e.g., Morning Session, Evening Reception, or Full Day), the system atomically locks the slot. No two customers can ever reserve the same hall on the same day.
- **Room & Package Inventory:** Hotels and halls can bundle room stays, catering packages, and decor options into a single reservation.
- **Booking Lifecycle States:**
  - `PENDING`: Initial reservation created, awaiting advance payment or owner review.
  - `CONFIRMED`: Advance payment verified, dates officially blocked on the calendar.
  - `CANCELLED`: Released by either customer or venue owner in accordance with cancellation policy.
  - `COMPLETED`: Event concluded successfully.

---

### 3.4. Custom Quotation & Price Negotiation Engine
Weddings and large events rarely have fixed, one-size-fits-all pricing. The platform provides an interactive negotiation workflow:
- **Quote Requests:** Customers submit detailed requirements (e.g., *"3-day wedding, 600 guests, 15 deluxe rooms, North Indian buffet"*).
- **Negotiation Thread:** Venue owners can reply with counter-offers, itemized service charges, and customized terms.
- **Immutable History:** Every counter-offer is preserved as a timestamped version to ensure transparency and accountability.
- **Instant Booking Conversion:** Once both parties agree on a negotiated total, the quote is converted into a confirmed booking with a single click.

---

### 3.5. Payment Processing & Dynamic Split-Payment Policies
Integrated with the **Cashfree Payment Gateway**, supporting UPI (Google Pay, PhonePe, Paytm), Debit/Credit Cards, and Net Banking:

- **Split Payments (Advance vs. Balance Settlement):**
  - High-value venue bookings typically do not require 100% upfront payment.
  - The platform allows customers to pay an **Advance Amount (e.g., 50%)** to confirm the date.
  - The remaining **Balance Amount (e.g., 50%)** is scheduled to be settled before the event date (e.g., 72 hours prior to check-in).
- **Hierarchical Policy Precedence:**
  - *Priority 1 (Venue-Specific Rule):* Individual venue owners can define custom advance rules (e.g., *"Grand Palace requires 40% advance"*).
  - *Priority 2 (Marketplace Default Rule):* If a venue has no custom rule, the platform default rule applies (e.g., 50% advance, 72 hours balance due).
- **Automated Refund & Penalty Engine:**
  - If a customer cancels within the free cancellation window, eligible refunds are calculated automatically based on defined penalty tiers.
- **Local Development Mock Mode:** Safe testing environment for developers and QA without processing real bank transactions.

---

### 3.6. Discount Coupons & Promotional Marketing
- **Platform-Wide Campaigns:** Administrators can launch seasonal campaigns (e.g., `DIWALI20`, `SUMMERWED5000`) applicable across all halls and hotels.
- **Venue-Specific Promotions:** Individual hall owners can create exclusive discounts for their properties.
- **Flexible Discount Types:** Supports both percentage discounts (e.g., 15% off up to ₹10,000) and flat discounts (e.g., Flat ₹5,000 off).
- **Usage Limits & Minimum Booking Guardrails:** Controls preventing coupon misuse by enforcing minimum booking values and per-user limits.
- **Visual Banner Integration:** Promotional banners display coupon graphics directly on the homepage and venue cards.

---

### 3.7. Verified Customer Reviews & Community Trust
- **Fraud-Proof Reviews:** Only customers with a verified, completed booking at that specific venue can write a review. Competitor spam and fake ratings are strictly prohibited.
- **5-Star Scoring:** Overall ratings, sub-category ratings (cleanliness, staff, ambiance), and written feedback.
- **Administrative Moderation:** Administrators can review flagged content to remove inappropriate or defamatory comments.

---

### 3.8. Real-Time Notification & Alert Center
- **Customer Updates:** Instant SMS, email, and push notifications when a booking is received, approved, or payment succeeds.
- **Vendor Alerts:** Immediate alerts sent to venue managers whenever a new inquiry, booking request, or payment occurs.
- **Payment Reminders:** Automated reminders dispatched before the balance due date to avoid automated reservation cancellations.

---

### 3.9. Customer Support & Inquiries
- **Interactive FAQ System:** Categorized repository answering common questions regarding booking policies, cancellations, and payment methods.
- **In-App Help Center:** Registered customers can raise support inquiries and track administrative replies directly from their dashboard.
- **Public Contact Us Form:** Allows visitors and prospective partners to submit business inquiries, which are forwarded directly to the administrator operations queue.
- **App Feedback:** Dedicated channel for users to suggest platform enhancements and report user-experience bugs.

---

## 🔄 4. Real-World User Workflows

### Workflow 1: Customer Booking a Wedding Venue

```
[1. Search & Filter] ───► Customer filters by City, 500 Guests, & AC Banquets
           │
           ▼
[2. Venue Inspection] ──► Views photo gallery, amenities, and verified guest reviews
           │
           ▼
[3. Slot Selection] ────► Selects desired wedding date & session (Evening)
           │
           ▼
[4. Apply Coupon] ──────► Enters promotional code (e.g., "DIWALI20") for instant discount
           │
           ▼
[5. Advance Payment] ───► Pays 50% advance via UPI (Google Pay / PhonePe)
           │
           ▼
[6. Confirmation] ──────► Slot locked; instant SMS & Email confirmation sent to host & venue
```

---

### Workflow 2: Venue Owner (Vendor) Workflow

```
[1. Registration] ──────► Vendor creates business account and submits KYC verification
           │
           ▼
[2. Property Setup] ────► Uploads photos, sets hall capacities, room rates, & advance rules
           │
           ▼
[3. Admin Approval] ────► Platform admin verifies credentials and activates public listing
           │
           ▼
[4. Booking Intake] ────► Receives instant notification of new customer booking / quote
           │
           ▼
[5. Execution & Payout] ► Confirms booking, hosts event, and receives scheduled financial payout
```

---

## 🛠️ 5. Technology Architecture (Explained for Non-Engineers)

The technology behind this platform has been selected for speed, security, and enterprise reliability:

| Technology Layer | What It Does (Plain English) | Business Impact & Benefit |
| :--- | :--- | :--- |
| **Go (Golang) Backend** | The digital brain executing all business rules, calculations, and validations. | Ultra-fast execution; handles thousands of concurrent booking visitors without slowdowns or crashes. |
| **PostgreSQL Database** | Secure vault storing user records, venue catalogs, booking slots, and transactions. | Financial-grade ACID compliance; guarantees dates are never double-booked and payment records remain tamper-proof. |
| **Cashfree Payment Gateway** | Licensed payment gateway processing card, netbanking, and UPI transactions. | Seamless, high-success-rate checkout experience for Indian customers; automated webhooks update booking status in seconds. |
| **AWS S3 Cloud Storage** | Scalable media cloud holding high-resolution venue photos, videos, and PDFs. | Blazing-fast page load speeds without clogging the application server. |
| **Redis In-Memory Cache** | Super-fast memory storing recent search history, popular venues, and rate limit counters. | Delivers instant search results and defends the platform against automated scraping or malicious bot attacks. |
| **Apache Kafka Event Bus** | Background messaging highway routing notifications, audit logs, and emails. | Ensures users never wait on a loading screen while emails or invoices are generated in the background. |

---

## 🛡️ 6. Business Governance, Safety & Compliance

1. **Role-Based Access Control (RBAC):** Strict separation between standard customers, venue managers, support staff, and platform administrators. No customer can see another customer's booking or financial details.
2. **Audit Logging:** Every critical action (booking confirmation, cancellation, manual price adjustment, review moderation, and refund) is stamped with the user ID, timestamp, and reason code for complete auditability.
3. **Idempotency Safeguard:** Network glitches often cause users to tap "Pay" or "Confirm" twice. The platform enforces idempotency keys, ensuring cards are charged and slots are reserved exactly once.
4. **Data Privacy:** Personal guest contacts are shielded and only shared with the booked venue owner after advance payment confirmation.

---

## 📖 7. Plain English Glossary

- **API (Application Programming Interface):** The secure digital highway connecting the mobile application or website to the backend database.
- **Advance vs. Balance Payment:** Splitting a large payment into two installments: an initial advance to reserve the date and a final balance paid prior to event day.
- **Idempotency:** A software guarantee that repeating a request (e.g., clicking a button twice) produces the same result without double-charging the customer.
- **KYC (Know Your Customer):** Identity and business verification checks ensuring that venue owners are legitimate business entities.
- **Slot / Session:** The specific time block booked at a venue (e.g., Morning, Evening, or Full Day).
- **Webhook:** An automated instant message sent by the payment gateway directly to the server confirming that a customer's money has been received.

---

### 📄 Document Metadata
- **Project Name:** Marriage Hall & Hotel Booking Platform
- **Document Version:** 2.0 (Official Stakeholder Release)
- **Primary Audience:** Product Managers, Business Stakeholders, Investors, and Non-Technical Teammates
- **Workspace File Path:** `d:\Codes\venue\PROJECT_OVERVIEW_AND_BUSINESS_GUIDE.md`
- **Backend Service Path:** `d:\Codes\venue\wed\marriage-hall-booking\PROJECT_OVERVIEW_AND_BUSINESS_GUIDE.md`
