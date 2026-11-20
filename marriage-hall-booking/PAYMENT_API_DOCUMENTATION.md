# 💳 Payment Gateway & Payment Policies — Integration & API Guide

A comprehensive, production-ready guide for developers, QA engineers, and frontend teams detailing the **Cashfree Payment Gateway Integration**, **Dynamic Advance & Balance Payments**, **Venue & Marketplace Policies**, and **Automated Refund & Webhook Workflows**.

---

## 📌 1. Architectural Overview

The payment engine supports flexible payment terms for Indian wedding halls and hotels:

```
                                +-----------------------------------+
                                |         Payment Policies          |
                                |  1. Venue Override (Priority 1)   |
                                |  2. Marketplace (Priority 2)      |
                                |  3. Hardcoded Defaults (Fallback) |
                                +-----------------+-----------------+
                                                  |
                                                  v
+------------------+     POST /payments/create    +-------------------+      Orders API      +-------------------+
|                  | ---------------------------> |                   | -------------------> |                   |
| Customer / App   |                              | Backend Go Engine |                      | Cashfree Gateway  |
|                  | <--------------------------- |                   | <------------------- |                   |
+------------------+   Returns paymentSessionId   +-------------------+  paymentSessionId    +-------------------+
         |                                                  ^
         | Completes UPI / Card payment                     | Webhook Notification
         v                                                  | POST /payments/webhook
+-----------------------------------------------------------+
```

### Key Capabilities
1. **Cashfree Gateway Integration:** Seamless support for UPI, Credit/Debit Cards, NetBanking, and Wallets via Cashfree API version `2022-09-01`.
2. **Safe Mock Mode:** When Cashfree credentials are not configured in `.env`, the system automatically runs in `MOCK` mode, enabling local development and automated testing without external API calls.
3. **Split Payments (`ADVANCE` vs `BALANCE` vs `FULL`):**
   - **First Payment (`ADVANCE`):** Charges the advance percentage (e.g. 50%) subject to min/max caps.
   - **Subsequent Payment (`BALANCE`):** Charges the remaining balance.
   - When `paid_amount >= total_amount`, the booking automatically transitions from `PENDING` to `CONFIRMED`.
4. **Policy Precedence:**
   - **Priority 1 (Venue Policy):** Custom terms set by the venue owner for their hall/hotel.
   - **Priority 2 (Marketplace Policy):** Platform-wide defaults managed by platform admins.
   - **Priority 3 (Built-in Defaults):** 50% advance, 72h balance due, 168h (7-day) cancellation window, 25% penalty.
5. **Automated Refund Calculation:**
   - Cancellation before the cutoff window (`eventStart - cancellationWindowHours`) qualifies for **100% Full Refund** of paid amount.
   - Cancellation after the cutoff window applies the policy penalty percent (`paidAmount - penalty`).

---

## ⚙️ 2. Environment Variables Configuration

Add the following keys to your `.env` file:

```env
# ==============================================================================
# Cashfree Payment Gateway Configuration
# ==============================================================================
CASHFREE_ENV=sandbox                           # 'sandbox' or 'production'
CASHFREE_CLIENT_ID=your_cashfree_app_id        # Provided by Cashfree Dashboard
CASHFREE_CLIENT_SECRET=your_cashfree_secret    # Provided by Cashfree Dashboard
CASHFREE_WEBHOOK_SECRET=your_webhook_secret    # Secret for HMAC verification
CASHFREE_BASE_URL=https://sandbox.cashfree.com # 'https://api.cashfree.com' for production
PAYMENT_WEBHOOK_SECRET=your_webhook_secret     # Fallback webhook secret key
```

> **Note on Local Development:** If `CASHFREE_CLIENT_ID` or `CASHFREE_CLIENT_SECRET` is left blank, the API uses internal `MOCK` order generation. Local bookings and webhook tests can be executed without a live gateway account.

---

## 🗄️ 3. Database Schema (`068_payment_policies.sql`)

```sql
CREATE TABLE IF NOT EXISTS payment_policies (
    id BIGSERIAL PRIMARY KEY,
    scope VARCHAR(30) NOT NULL CHECK (scope IN ('MARKETPLACE', 'VENUE')),
    facility_id UUID NULL REFERENCES facilities(id) ON DELETE CASCADE,
    advance_percent NUMERIC(5,2) NOT NULL DEFAULT 50,
    min_advance_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    max_advance_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    balance_due_hours_before_event INTEGER NOT NULL DEFAULT 72,
    cancellation_window_hours INTEGER NOT NULL DEFAULT 168,
    cancellation_penalty_percent NUMERIC(5,2) NOT NULL DEFAULT 25,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_policy_scope_facility_unique UNIQUE (scope, facility_id)
);
```

---

## 📡 4. Complete API Specifications

### 4.1 Create Payment Order
Initializes a payment order against an existing booking. The engine computes the exact due amount (Advance or Balance) based on the applicable policy.

- **Method:** `POST`
- **Path:** `/api/v1/payments/create`
- **Auth Required:** Bearer JWT (`ROLE_CUSTOMER` or booking owner)
- **Request Body (`application/json`):**
  ```json
  {
    "bookingId": "c5a6b7c8-9d0e-1f2a-3b4c-5d6e7f8a9b0c"
  }
  ```
- **Response (`200 OK`):**
  ```json
  {
    "success": true,
    "message": "Payment created successfully",
    "data": {
      "id": "pay-f8a1b2c3-4d5e",
      "bookingId": "c5a6b7c8-9d0e-1f2a-3b4c-5d6e7f8a9b0c",
      "userId": 101,
      "amount": 50000.00,
      "currency": "INR",
      "status": "PENDING",
      "gateway": "CASHFREE",
      "gatewayOrderId": "booking-c5a6b7c8-1698765432",
      "paymentType": "ADVANCE",
      "paymentSessionId": "session_cf_xyz_sample_token",
      "checkoutUrl": "",
      "createdAt": "2026-11-07T10:00:00.000000Z"
    },
    "timestamp": "2026-11-07T10:00:00.123456000"
  }
  ```

---

### 4.2 Payment Gateway Webhook
Asynchronous server-to-server callback sent by Cashfree upon transaction completion.

- **Method:** `POST`
- **Path:** `/api/v1/payments/webhook`
- **Auth:** HMAC-SHA256 signature passed in `X-Signature` header computed over raw request body.
- **Request Body (`application/json`):**
  ```json
  {
    "eventId": "evt_987654321",
    "orderId": "booking-c5a6b7c8-1698765432",
    "paymentId": "cf_pay_11223344",
    "status": "SUCCESS",
    "amount": 50000.00,
    "reason": ""
  }
  ```
- **Response (`200 OK`):**
  ```json
  {
    "success": true,
    "message": "Webhook processed",
    "data": null
  }
  ```
- **Business Logic Triggered:**
  - Idempotent processing using unique constraint on `payment_webhook_events(event_id)`.
  - Increments `bookings.paid_amount`.
  - If `paid_amount >= total_amount`, updates `bookings.status = 'CONFIRMED'`.
  - Emits `TopicPaymentCompleted` domain event on Kafka/local bus.

---

### 4.3 Process Refund
Processes a partial or full refund for a previous successful payment transaction.

- **Method:** `POST`
- **Path:** `/api/v1/refunds/{paymentId}?amount=15000&reason=Customer_Requested`
- **Auth Required:** `ROLE_ADMIN` or `ROLE_HALL_OWNER` (must own the venue)
- **Path Variables:**
  - `:paymentId`: Payment UUID
- **Query Parameters:**
  - `amount` (float, required): Refund amount (must not exceed paid amount).
  - `reason` (string, optional): Justification for the refund.
- **Response (`200 OK`):**
  ```json
  {
    "success": true,
    "message": "Refund processed successfully",
    "data": {
      "refundId": "ref-uuid-5678"
    }
  }
  ```

---

### 4.4 Get Active Payment Policy
Fetches the active payment policy. If a `bookingId` is supplied, it resolves the specific venue policy (or falls back to the marketplace policy).

- **Method:** `GET`
- **Path:** `/api/v1/payment-policies?bookingId={optionalBookingId}`
- **Auth Required:** Authenticated User
- **Response (`200 OK`):**
  ```json
  {
    "success": true,
    "message": "Payment policy retrieved",
    "data": {
      "advancePercent": 50,
      "minAdvanceAmount": 10000,
      "maxAdvanceAmount": 100000,
      "balanceDueHoursBeforeEvent": 72,
      "cancellationWindowHours": 168,
      "cancellationPenaltyPercent": 25
    }
  }
  ```

---

### 4.5 Update Marketplace Policy (Admin Only)
Updates the global platform-wide payment terms and default cancellation rules.

- **Method:** `PUT`
- **Path:** `/api/v1/payment-policies`
- **Auth Required:** `ROLE_ADMIN`
- **Request Body (`application/json`):**
  ```json
  {
    "advancePercent": 40.0,
    "minAdvanceAmount": 5000.0,
    "maxAdvanceAmount": 50000.0,
    "balanceDueHoursBeforeEvent": 48,
    "cancellationWindowHours": 120,
    "cancellationPenaltyPercent": 20.0
  }
  ```
- **Response (`200 OK`):** Returns the normalized updated policy object.

---

### 4.6 Get Facility Payment Policy
Fetches the custom payment policy configured for a specific facility / venue.

- **Method:** `GET`
- **Path:** `/api/v1/facilities/{facilityId}/payment-policy`
- **Auth Required:** Authenticated User
- **Path Variable:** `:facilityId` (Venue UUID)
- **Response (`200 OK`):** Returns venue payment policy if configured, otherwise the marketplace default.

---

### 4.7 Update Facility Payment Policy (Owner / Admin)
Allows a hall or hotel owner (or platform admin) to customize payment terms for their specific venue.

- **Method:** `PUT`
- **Path:** `/api/v1/facilities/{facilityId}/payment-policy`
- **Auth Required:** `ROLE_HALL_OWNER` (must own the facility) or `ROLE_ADMIN`
- **Path Variable:** `:facilityId` (Venue UUID)
- **Request Body (`application/json`):**
  ```json
  {
    "advancePercent": 30.0,
    "minAdvanceAmount": 15000.0,
    "maxAdvanceAmount": 80000.0,
    "balanceDueHoursBeforeEvent": 96,
    "cancellationWindowHours": 240,
    "cancellationPenaltyPercent": 30.0
  }
  ```
- **Response (`200 OK`):** Returns the updated venue policy object.

---

## 📱 5. Frontend & Mobile SDK Integration Guide

### Web (React / Next.js)
Include the Cashfree JS SDK in your checkout page:
```html
<script src="https://sdk.cashfree.com/js/v3/cashfree.js"></script>
```

```javascript
import { load } from '@cashfreepayments/cashfree-js';

// 1. Call your Go backend to create order
const response = await fetch('/api/v1/payments/create', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${accessToken}`
  },
  body: JSON.stringify({ bookingId: currentBookingId })
});
const { data } = await response.json();

// 2. Initialize Cashfree Checkout
const cashfree = await load({ mode: 'sandbox' }); // or 'production'
cashfree.checkout({
  paymentSessionId: data.paymentSessionId,
  redirectTarget: '_self' // or '_modal' for popup
});
```

### Mobile App (Flutter / React Native)
Pass the returned `data.paymentSessionId` and `data.gatewayOrderId` directly to the official `cashfree-pg-sdk` native component:
- **Flutter:** `cashfree_pg: ^2.x.x` ➔ Call `CFPaymentGatewayService().doPayment(session)`.
- **React Native:** `react-native-cashfree-pg-sdk` ➔ Call `CFPaymentGatewayService.doPayment(session)`.

---

## 🧪 6. Testing & QA Verification Flow

### Test Scenario A: Advance Payment
1. **Book Venue:** `POST /api/v1/bookings/venues` ➔ Total: ₹1,00,000. Status: `PENDING`.
2. **Create Order:** `POST /api/v1/payments/create` ➔ Returns Amount: ₹50,000, `paymentType: "ADVANCE"`.
3. **Trigger Webhook:** `POST /api/v1/payments/webhook` with `status: "SUCCESS"` and `amount: 50000`.
4. **Verify Booking:** `GET /api/v1/bookings/{id}` ➔ `paidAmount: 50000`, `totalAmount: 100000`.

### Test Scenario B: Balance Payment & Final Confirmation
1. **Create Second Order:** `POST /api/v1/payments/create` ➔ Returns Amount: ₹50,000, `paymentType: "BALANCE"`.
2. **Trigger Webhook:** `POST /api/v1/payments/webhook` with `status: "SUCCESS"` and `amount: 50000`.
3. **Verify Booking:** `GET /api/v1/bookings/{id}` ➔ `paidAmount: 100000`, `status: "CONFIRMED"`.

### Test Scenario C: Unit Tests Execution
Run the policy verification test suite:
```bash
cd internal/payment/service
go test -v -run TestPolicy
```
*Expected Result: `PASS` across normalization, precedence resolution, and cancellation calculations.*

