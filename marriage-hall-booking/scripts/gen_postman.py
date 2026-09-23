#!/usr/bin/env python3
"""Generate postman_collection.json from the routes registered in the Go code.

Reads the mux.Handle/HandleFunc lines so the collection can never drift from
what the server actually serves - a hand-maintained collection goes stale the
first time a route is renamed.

Usage: python3 scripts/gen_postman.py > postman_collection.json
"""
import json
import re
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Example bodies, keyed by "METHOD /path". Fields match the Go request structs.
BODIES = {
    # Emails and phones come from variables a pre-request script fills with a
    # fresh value each run. Hardcoding them made the collection work exactly
    # once: the second run got 409 EMAIL_EXISTS on the very first request and
    # every authenticated request after it failed 401 for want of a token.
    "POST /api/v1/auth/register": {
        "fullName": "Priya Sharma", "email": "{{customerEmail}}",
        "phoneNumber": "{{customerPhone}}", "password": "SecurePass@123",
    },
    "POST /api/v1/auth/register/vendor": {
        "fullName": "Rajesh Kumar", "email": "{{ownerEmail}}",
        "phoneNumber": "{{ownerPhone}}", "password": "SecurePass@123",
    },
    "POST /api/v1/auth/register/verify-email": {
        "target": "{{customerEmail}}", "otpCode": "{{otp}}",
    },
    "POST /api/v1/auth/register/verify-phone": {
        "target": "{{customerPhone}}", "otpCode": "{{otp}}",
    },
    "POST /api/v1/auth/login": {
        "identifier": "{{customerEmail}}", "password": "SecurePass@123",
    },
    "POST /api/v1/auth/otp/resend": {
        "target": "{{customerEmail}}", "otpType": "EMAIL_VERIFICATION",
    },
    "POST /api/v1/auth/refresh": {"refreshToken": "{{refreshToken}}"},
    "POST /api/v1/auth/login/refresh": {"refreshToken": "{{refreshToken}}"},
    "POST /api/v1/auth/logout": {"refreshToken": "{{refreshToken}}"},
    "POST /api/v1/auth/forgot-password": {"identifier": "{{customerEmail}}"},
    "POST /api/v1/auth/reset-password": {
        "identifier": "{{customerEmail}}", "token": "{{resetToken}}",
        "newPassword": "NewSecurePass@123",
    },
    "PUT /api/v1/users/me": {
        "firstName": "Priya", "lastName": "Sharma",
        "bio": "Wedding planner looking for the perfect venue",
    },
    "PUT /api/v1/vendors/me": {
        "businessName": "Kumar Weddings & Events",
        "businessAddress": "42 MG Road, Bengaluru 560001",
        "businessDescription": "Premium wedding venues since 1998",
        "businessType": "VENUE",
    },
    "PUT /api/v1/vendors/me/kyc": {
        "documentUrl": "https://files.example.com/kyc/rajesh-pan.pdf",
    },
    "PUT /api/v1/vendors/me/bank-account": {
        "accountNumber": "1234567890123456", "ifsc": "HDFC0001234",
        "holderName": "Rajesh Kumar",
    },
    "POST /api/v1/facilities#hotel": {
        "name": "Grand Residency", "type": "HOTEL",
        "description": "4-star business hotel near the venue",
        "city": "Bengaluru", "fullAddress": "9 Residency Road", "state": "Karnataka",
        "zipcode": "560025", "country": "India", "lat": 12.9716, "lng": 77.5946,
        "amenityIds": ["PARKING", "AIR_CONDITIONING", "BREAKFAST_INCLUDED"],
        "starRating": 4, "checkInTime": "14:00", "checkOutTime": "11:00",
    },
    "POST /api/v1/facilities": {
        "name": "Kumar Grand Palace", "type": "MARRIAGE_HALL",
        "description": "Premium wedding venue with lawn and banquet hall",
        "city": "Bengaluru", "fullAddress": "42 MG Road", "state": "Karnataka",
        "zipcode": "560001", "country": "India", "lat": 12.9716, "lng": 77.5946,
        "amenityIds": ["PARKING", "AIR_CONDITIONING", "CATERING"],
        "capacityPax": 800, "areaSqft": 12000, "basePricePerDay": 150000,
        "seatingCapacity": 600, "floatingCapacity": 800, "minBookingSize": 100,
    },
    "PUT /api/v1/halls/{id}": {
        "name": "Kumar Grand Palace (Renovated)", "type": "MARRIAGE_HALL",
        "city": "Bengaluru", "capacityPax": 900, "basePricePerDay": 175000,
    },
    "PUT /api/v1/hotels/{id}": {
        "name": "Grand Residency", "type": "HOTEL", "city": "Bengaluru",
        "starRating": 4, "checkInTime": "14:00", "checkOutTime": "11:00",
    },
    "POST /api/v1/hotels/{id}/room-types": {
        "name": "Deluxe Room", "description": "King bed with city view",
        "capacityAdults": 2, "capacityChildren": 1,
        "basePricePerNight": 4500, "totalRooms": 10,
    },
    "POST /api/v1/halls/{id}/packages": {
        "name": "Premium Wedding Package",
        "description": "Full hall rental with decoration and catering",
        "price": 350000, "guestCapacity": 500, "includesCatering": True,
        "includedServices": "Decoration, Catering, DJ, Photography",
        "excludedServices": "Alcohol, Fireworks",
    },
    "POST /api/v1/halls/{id}/addons": {
        "name": "Live Music Band", "description": "3-hour live performance",
        "price": 25000, "serviceType": "BAND", "unit": "EVENT",
    },
    "POST /api/v1/halls/{id}/advance-rules": {
        "advancePercentage": 25, "minAdvanceAmount": 50000,
        "balanceDueDaysBefore": 15, "autoReminderEnabled": True,
    },
    "POST /api/v1/facilities/{id}/policies": {
        "policyType": "ALCOHOL",
        "description": "Alcohol permitted only with prior written approval.",
    },
    "PUT /api/v1/facilities/{id}/policies/{childId}": {
        "policyType": "ALCOHOL",
        "description": "Alcohol permitted with prior approval, until 11pm.",
    },
    "POST /api/v1/facilities/{id}/pricing": {
        "eventType": "Wedding", "dayType": "WEEKEND", "season": "Peak Season",
        "minGuests": 300, "maxGuests": 700, "price": 180000,
        "validFrom": "2027-10-01", "validTo": "2028-03-31",
    },
    "PUT /api/v1/facilities/{id}/pricing/{childId}": {
        "eventType": "Wedding", "dayType": "WEEKEND", "season": "Peak Season",
        "minGuests": 300, "maxGuests": 700, "price": 200000,
        "validFrom": "2027-10-01", "validTo": "2028-03-31",
    },
    "POST /api/v1/facilities/{id}/images": {
        "url": "https://cdn.example.com/venues/hall-main.jpg",
        "isCover": True, "sortOrder": 0,
    },
    "POST /api/v1/facilities/{id}/videos": {
        "url": "https://cdn.example.com/venues/hall-tour.mp4",
        "thumbnailUrl": "https://cdn.example.com/venues/hall-tour.jpg",
        "durationSeconds": 90, "sortOrder": 0,
    },
    # One id is enough to demonstrate the endpoint; add more ids to the array
    # after creating more images. An empty placeholder is rejected as invalid.
    "PUT /api/v1/facilities/{id}/images/reorder": {"order": ["{{imageId}}"]},
    "PUT /api/v1/facilities/{id}/videos/reorder": {"order": ["{{videoId}}"]},
        "POST /api/v1/bookings/halls": {
        "hallId": "{{hallId}}",
        "startDate": "2027-06-25", "endDate": "2027-06-26",
        "startTime": "18:00", "endTime": "23:00",
        "guestCount": 400, "eventType": "WEDDING",
        # Optional: guest rooms the party needs alongside the hall. Not priced.
        "roomCount": 12,
        "idempotentKey": "hall-{{runId}}",
    },
    "PATCH /api/v1/admin/facilities/{id}": {
        "name": "Royal Grand Palace",
        "contactPhone": "+919876543210",
        "basePricePerDay": 125000,
        "discountPercent": 15,
        "discountLabel": "Monsoon offer",
    },
    "PUT /api/v1/admin/facilities/{id}/rating": {
        "avgRating": 4.3, "reviewCount": 218,
    },
    "PUT /api/v1/admin/support": {
        "supportEmail": "support@tripfactory.travel",
        "supportPhone": "+91 80 4000 0000",
        "supportWhatsapp": "",
        "supportHours": "Mon-Sat, 9:00 AM - 7:00 PM",
        "supportAddress": "",
    },
    "POST /api/v1/devices": {
        "token": "fcm-device-token-{{runId}}", "platform": "ANDROID",
    },
    "PUT /api/v1/users/me/location": {
        "lat": 12.9716, "lng": 77.5946, "source": "DEVICE",
    },
    "PUT /api/v1/users/me/geo-notifications": {
        "enabled": True,
    },
    "POST /api/v1/coupons": {
        "code": "WED{{runId}}", "discountType": "PERCENT", "discountValue": 15,
        "maxDiscount": 10000, "minBookingAmount": 50000,
        "facilityId": "{{hallId}}",
    },
    "PUT /api/v1/coupons/{id}": {
        "code": "WED{{runId}}", "discountType": "PERCENT", "discountValue": 20,
        "maxDiscount": 12000, "isActive": True,
    },
    "POST /api/v1/coupons/validate": {
        "code": "WED{{runId}}", "amount": 200000, "facilityId": "{{hallId}}",
    },
    "POST /api/v1/bookings/quote": {
        "hallId": "{{hallId}}",
        "startDate": "2027-06-25", "endDate": "2027-06-26",
        "startTime": "18:00", "endTime": "23:00",
        "guestCount": 400,
    },
    "POST /api/v1/facilities/{id}/cancellation-policies": {
        "policyType": "FLEXIBLE", "daysBeforeEvent": 30, "refundPercentage": 100,
    },
    "POST /api/v1/admin/reviews": {
        "facilityId": "{{hallId}}", "rating": 5,
        "title": "Excellent venue", "comment": "Beautiful hall, great service.",
    },
    "PUT /api/v1/admin/reviews/{id}": {
        "rating": 4, "title": "Very good", "comment": "Updated by admin.",
    },
    "POST /api/v1/bookings/hotels": {
        "hotelId": "{{hotelId}}", "checkIn": "2027-05-01", "checkOut": "2027-05-03",
        "rooms": [{"roomTypeId": "{{roomTypeId}}", "quantity": 2}],
        "idempotentKey": "hotel-{{runId}}",
        "guestName": "Priya Sharma", "guestEmail": "{{customerEmail}}",
        "guestPhone": "{{customerPhone}}",
    },
    "POST /api/v1/payments/create": {"bookingId": "{{bookingId}}"},
    "POST /api/v1/payments/webhook": {
        "eventId": "evt-001", "orderId": "{{gatewayOrderId}}",
        "paymentId": "pay_abc123", "status": "SUCCESS", "amount": 150000,
    },
    "POST /api/v1/quotes/request": {
        "facilityId": "{{hallId}}",
        "eventType": "Wedding Reception",
        "startDate": "2027-01-15",
        "endDate": "2027-01-16",
        "startTime": "18:00",
        "endTime": "23:00",
        "guestCount": 450,
        "budgetMin": 250000,
        "budgetMax": 400000,
        "specialRequirements": "Vegetarian catering and a stage for the sangeet",
        "preferredContact": "PHONE",
    },
    "POST /api/v1/quotes/{id}/reply": {
        "items": [
            {"name": "Hall Rental (Full Day)", "quantity": 1, "unitPrice": 150000},
            {"name": "Vegetarian Catering (per plate)", "quantity": 450, "unitPrice": 600},
            {"name": "Stage & Sangeet Decoration", "quantity": 1, "unitPrice": 45000},
        ],
        "discount": 10000, "tax": 18000, "serviceCharge": 5000,
        "notes": "Valid for 14 days", "validUntil": "2027-01-01",
    },
    "POST /api/v1/quotes/{id}/counter": {
        "items": [
            {"name": "Hall Rental (Full Day)", "quantity": 1, "unitPrice": 150000},
            {"name": "Vegetarian Catering (per plate)", "quantity": 450, "unitPrice": 500},
            {"name": "Stage & Sangeet Decoration", "quantity": 1, "unitPrice": 35000},
        ],
        "discount": 15000, "tax": 15000, "serviceCharge": 5000,
    },
    "POST /api/v1/quotes/{id}/reject": {"reason": "Not available on that date"},
    "POST /api/v1/quotes/{id}/cancel": {"reason": "Changed my mind"},
    "POST /api/v1/quotes/{id}/messages": {
        "messageType": "TEXT", "content": "This looks great, thank you!",
    },
    "POST /api/v1/quotes/{id}/attachments": {
        "fileName": "floor-plan.pdf",
        "fileUrl": "https://files.example.com/quotes/floor-plan.pdf",
        "contentType": "application/pdf", "sizeBytes": 248000,
    },
    "POST /api/v1/quotes/{id}/convert-to-booking": {
        "idempotentKey": "quote-{{runId}}",
    },
    "POST /api/v1/reviews": {
        "facilityId": "{{hallId}}", "bookingId": "{{bookingId}}",
        "rating": 5, "comment": "Beautiful venue and excellent service.",
    },
    "POST /api/v1/admin/users": {
        "fullName": "Staff Member", "email": "staff@example.com",
        "password": "StaffPass@123", "role": "ROLE_STAFF",
    },
    "PUT /api/v1/admin/users/{id}": {"fullName": "Updated Name", "status": "ACTIVE"},
    # Targets this run's own customer, not a hardcoded id: the endpoint is a
    # toggle, so pointing it at a fixed user flips that account's status on
    # every run and leaves the result depending on how many times it has run.
    "POST /api/v1/admin/block": {
        "id": "{{userId}}", "type": "user", "reason": "Suspicious activity",
    },
    "POST /api/v1/admin/block#unblock": {
        "id": "{{userId}}", "type": "user", "reason": "Appeal upheld",
    },
    "POST /api/v1/admin/block#facility": {
        "id": "{{hallId}}", "type": "MARRIAGE_HALL", "reason": "Listing under review",
    },
}

# Query strings worth pre-filling, so a request is useful the moment it opens.
QUERIES = {
    "GET /api/v1/facilities": "type=MARRIAGE_HALL&search=&city=&page=0&size=20",
    "GET /api/v1/halls": "search=&page=0&size=20",
    "GET /api/v1/halls/my-halls": "page=0&size=20",
    "GET /api/v1/hotels/my-hotels": "page=0&size=20",
    "GET /api/v1/amenities": "",
    "GET /api/v1/bookings": "page=0&size=20",
    "GET /api/v1/users/me/favourites": "",
    "GET /api/v1/vendors/me/properties": "page=0&size=20",
    "GET /api/v1/quotes/my-requests": "status=&page=0&size=20",
    "GET /api/v1/quotes/owner": "status=&page=0&size=20",
    "GET /api/v1/reviews/facility/{facilityId}": "page=0&size=20",
    "GET /api/v1/admin/users": "search=&page=0&size=20",
    "GET /api/v1/admin/vendors": "status=PENDING&page=0&size=20",
    "GET /api/v1/admin/facilities": "type=&search=&page=0&size=20",
    "GET /api/v1/admin/dashboard": "",
    "GET /api/v1/search/venues": ("city=Bengaluru&venueType=MARRIAGE_HALL"
        "&minCapacity=100&maxCapacity=1000&minBudget=100000&maxBudget=500000"
        "&amenities=Parking&sort=PRICE_LOW_TO_HIGH&page=0&size=20"),
    "GET /api/v1/search/autocomplete": "q=Kum",
    "GET /api/v1/search/suggestions": "q=Beng",
    "GET /api/v1/search/venues/{id}/similar": "limit=10",
    "POST /api/v1/search/venues/{id}/view": "searchId={{searchId}}",
    "GET /api/v1/search/popular-cities": "limit=10",
    "POST /api/v1/refunds/{paymentId}": "amount=50000&reason=Change of plans",
    "POST /api/v1/admin/fraud-reports": ("targetType=VENDOR&targetId={{vendorId}}"
        "&reason=Suspicious activity pattern"),
    "POST /api/v1/admin/fraud-reports/{reportId}/resolve": "status=RESOLVED_CLEARED&note=",
    "POST /api/v1/admin/vendors/{vendorId}/kyc/reject": "reason=Document unreadable",
    "POST /api/v1/admin/facilities/{id}/approve": "status=APPROVED",
}

# Which token each folder should send. Requests that must stay anonymous are
# listed in NO_AUTH below.
# Which identity each folder acts as. Owning and editing a listing is the
# vendor's job; browsing, booking and reviewing are the customer's. Keys must
# match FOLDER_ORDER exactly - a name that falls through defaults to the
# customer token and every owner-only request in it answers 403.
FOLDER_TOKEN = {
    "Health": None,
    "Auth": "accessToken",
    "User Profile": "accessToken",
    "Vendors": "ownerToken",
    "Facilities (unified)": "ownerToken",
    "Marriage Halls": "ownerToken",
    "Hotels": "ownerToken",
    "Room Types": "ownerToken",
    "Hall Packages": "ownerToken",
    "Add-on Services": "ownerToken",
    "Token Advance Rules": "ownerToken",
    "Amenities": "ownerToken",
    "Facility Policies": "ownerToken",
    "Facility Pricing Rules": "ownerToken",
    "Facility Media": "ownerToken",
    "Favourites": "accessToken",
    "Bookings": "accessToken",
    "Payments": "accessToken",
    "Refunds": "accessToken",
    "Quotes & Negotiation": "accessToken",
    "Reviews": "accessToken",
    # Device registration, location and acknowledge are all customer actions.
    # The public /ack/ and /decline/ links carry their own token in the path
    # and need no header, but sending one does no harm.
    "Notifications": "accessToken",
    # Coupons are created by a vendor or an admin, so the owner token.
    "Coupons": "ownerToken",
    # GET /api/v1/support is public; the admin PUT lives in the Admin folder.
    "Support": None,
    "Search": "accessToken",
    "Admin": "adminToken",
    "Cleanup (destructive)": "ownerToken",
}

# A few requests need a different identity from the rest of their folder: in a
# negotiation the customer asks and the owner answers, and only the owner may
# refund. Without these the request runs as the wrong party and gets 403.
REQUEST_TOKEN = {
    # reply is the owner's move; counter is the customer answering that reply.
    "POST /api/v1/quotes/{id}/reply": "ownerToken",
    "GET /api/v1/quotes/owner": "ownerToken",
    "GET /api/v1/quotes/owner/stats": "ownerToken",
    "POST /api/v1/refunds/{paymentId}": "ownerToken",
}

NO_AUTH = {
    "POST /api/v1/auth/register", "POST /api/v1/auth/register/vendor",
    "POST /api/v1/auth/register/verify-email", "POST /api/v1/auth/register/verify-phone",
    "POST /api/v1/auth/login", "POST /api/v1/auth/otp/resend",
    "POST /api/v1/auth/refresh", "POST /api/v1/auth/login/refresh",
    "POST /api/v1/auth/logout", "POST /api/v1/auth/forgot-password",
    "POST /api/v1/auth/reset-password", "POST /api/v1/payments/webhook",
    "GET /health",
}

MODULE_FOLDER = {
    "health": "Health", "auth": "Auth", "user": "User Profile",
    "vendors": "Vendors", "booking": "Bookings", "payment": "Payments",
    "quote": "Quotes & Negotiation", "review": "Reviews", "search": "Search",
    "admin": "Admin", "notification": "Notifications", "support": "Support",
    "coupon": "Coupons",
}

FOLDER_ORDER = ["Health", "Auth", "User Profile", "Vendors",
                "Facilities (unified)", "Marriage Halls", "Hotels",
                "Room Types", "Hall Packages", "Add-on Services",
                "Token Advance Rules", "Amenities", "Facility Policies",
                "Facility Pricing Rules", "Facility Media",
                "Favourites", "Bookings", "Payments", "Refunds",
                "Quotes & Negotiation", "Reviews", "Notifications",
                "Coupons", "Support", "Search", "Admin",
                "Cleanup (destructive)"]

# One folder per resource, as in the Java collection. A single "Facility
# Inventory" folder of 14 mixed requests is hard to navigate; these split on the
# path segment that identifies the resource. Order matters: /halls/{id}/packages
# is a package, not a hall, so the more specific patterns are tested first.
PATH_FOLDER = [
    ("/room-types", "Room Types"),
    ("/packages", "Hall Packages"),
    ("/addons", "Add-on Services"),
    ("/advance-rules", "Token Advance Rules"),
    ("/policies", "Facility Policies"),
    ("/pricing", "Facility Pricing Rules"),
    ("/amenities", "Amenities"),
    ("/images", "Facility Media"),
    ("/videos", "Facility Media"),
    ("/favourites", "Favourites"),
    ("/cancellation-policies", "Facility Policies"),
    ("/api/v1/admin/reviews", "Admin"),
    ("/api/v1/admin/support", "Admin"),
    ("/api/v1/bookings/quote", "Bookings"),
    # Acknowledge/decline act on a booking, so they must run after one exists -
    # the folder decides run order, and Notifications comes after Bookings.
    ("/acknowledge", "Notifications"),
    ("/api/v1/refunds", "Refunds"),
    ("/api/v1/halls", "Marriage Halls"),
    ("/api/v1/hotels", "Hotels"),
]

# Renaming a folder without updating FOLDER_TOKEN silently downgrades every
# request in it to the customer token, which shows up only as scattered 403s in
# a newman run. Fail the generator instead.
assert set(FOLDER_TOKEN) == set(FOLDER_ORDER), (
    "FOLDER_TOKEN and FOLDER_ORDER disagree: "
    f"{set(FOLDER_ORDER) ^ set(FOLDER_TOKEN)}")

# Requests that destroy something later folders depend on. Sorting DELETEs last
# within their own folder is not enough: "Delete hall" still ran in Facilities,
# long before Bookings and Quotes needed that hall, and every one of them then
# failed with INVALID_HALL. These move to a Cleanup folder at the very end, so
# running the whole collection top to bottom works.
DESTRUCTIVE = {
    # Cancelling the booking that Payments and Refunds then charge against
    # leaves both answering 409 INVALID_STATE - the booking is no longer
    # awaiting payment.
    "POST /api/v1/bookings/{id}/cancel",
    # Logout revokes every access token this user holds, not just the refresh
    # token, so running it mid-collection leaves all 22 folders below it
    # unauthenticated. It belongs with the other end-of-run requests.
    "POST /api/v1/auth/logout",
    "POST /api/v1/auth/logout/all-devices",
    "DELETE /api/v1/halls/{id}",
    "DELETE /api/v1/hotels/{id}",
    "DELETE /api/v1/users/me",
    "DELETE /api/v1/admin/users/{id}",
    "DELETE /api/v1/reviews/{id}",
    "DELETE /api/v1/hotels/{id}/room-types/{childId}",
    "DELETE /api/v1/halls/{id}/packages/{childId}",
    "DELETE /api/v1/halls/{id}/addons/{childId}",
    "DELETE /api/v1/facilities/{id}/policies/{childId}",
    "DELETE /api/v1/facilities/{id}/pricing/{childId}",
    "DELETE /api/v1/facilities/{id}/images/{childId}",
    "DELETE /api/v1/facilities/{id}/videos/{childId}",
    "DELETE /api/v1/facilities/{id}/amenities/{amenityId}",
}

# Requests are ordered by dependency, not alphabetically: register before verify,
# verify before anything that needs a token, create before read/update/delete.
# Sorting by name would put "Create Auth Register" ninth in its own folder, after
# every request that needs the account it creates.
REQUEST_ORDER = [
    "POST /api/v1/auth/register",
    "POST /api/v1/auth/register/vendor",
    "POST /api/v1/auth/register/verify-email",
    "POST /api/v1/auth/register/verify-phone",
    "POST /api/v1/auth/otp/resend",
    "POST /api/v1/auth/login",
    "GET /api/v1/auth/me",
    "POST /api/v1/auth/refresh",
    "POST /api/v1/auth/login/refresh",
    "POST /api/v1/auth/forgot-password",
    "POST /api/v1/auth/reset-password",

    "GET /api/v1/users/me",
    "PUT /api/v1/users/me",
    "POST /api/v1/users/me/favourites/{facilityId}",
    "GET /api/v1/users/me/favourites",
    "DELETE /api/v1/users/me/favourites/{facilityId}",

    "PUT /api/v1/vendors/me",
    "GET /api/v1/vendors/me",
    "PUT /api/v1/vendors/me/kyc",
    "PUT /api/v1/vendors/me/bank-account",
    "GET /api/v1/vendors/me/properties",
    "GET /api/v1/vendors/dashboard",

    "POST /api/v1/facilities",
    "GET /api/v1/facilities",
    "GET /api/v1/halls",
    "GET /api/v1/halls/{id}",
    "GET /api/v1/hotels/{id}",
    "PUT /api/v1/halls/{id}",
    "PUT /api/v1/hotels/{id}",
    "GET /api/v1/halls/my-halls",
    "GET /api/v1/hotels/my-hotels",
    "GET /api/v1/amenities",
    "POST /api/v1/facilities/{id}/amenities/{amenityId}",
    "DELETE /api/v1/facilities/{id}/amenities/{amenityId}",
    "DELETE /api/v1/halls/{id}",
    "DELETE /api/v1/hotels/{id}",

    "POST /api/v1/hotels/{id}/room-types",
    "GET /api/v1/hotels/{id}/room-types",
    "POST /api/v1/halls/{id}/packages",
    "GET /api/v1/halls/{id}/packages",
    "POST /api/v1/halls/{id}/addons",
    "GET /api/v1/halls/{id}/addons",
    "POST /api/v1/halls/{id}/advance-rules",
    "GET /api/v1/halls/{id}/advance-rules",
    "POST /api/v1/facilities/{id}/policies",
    "GET /api/v1/facilities/{id}/policies",
    "PUT /api/v1/facilities/{id}/policies/{childId}",
    "POST /api/v1/facilities/{id}/pricing",
    "GET /api/v1/facilities/{id}/pricing",
    "PUT /api/v1/facilities/{id}/pricing/{childId}",

    "POST /api/v1/facilities/{id}/images",
    "PUT /api/v1/facilities/{id}/images/{childId}/cover",
    "PUT /api/v1/facilities/{id}/images/reorder",
    "POST /api/v1/facilities/{id}/videos",
    "PUT /api/v1/facilities/{id}/videos/reorder",
    "POST /api/v1/facilities/{id}/block",
    "POST /api/v1/facilities/{id}/unblock",

    "POST /api/v1/bookings/halls",
    "POST /api/v1/bookings/hotels",
    "GET /api/v1/bookings",
    "GET /api/v1/bookings/{id}",
    "POST /api/v1/bookings/{id}/cancel",

    "POST /api/v1/payments/create",
    "POST /api/v1/payments/webhook",
    "POST /api/v1/refunds/{paymentId}",

    "POST /api/v1/quotes/request",
    "GET /api/v1/quotes/{id}",
    "GET /api/v1/quotes/my-requests",
    "GET /api/v1/quotes/owner",
    "GET /api/v1/quotes/owner/stats",
    "POST /api/v1/quotes/{id}/reply",
    "POST /api/v1/quotes/{id}/counter",
    "POST /api/v1/quotes/{id}/messages",
    "GET /api/v1/quotes/{id}/messages",
    "POST /api/v1/quotes/{id}/attachments",
    "GET /api/v1/quotes/{id}/attachments",
    "POST /api/v1/quotes/{id}/accept",
    "POST /api/v1/quotes/{id}/convert-to-booking",
    "POST /api/v1/quotes/{id}/reject",
    "POST /api/v1/quotes/{id}/cancel",

    "POST /api/v1/reviews",
    "GET /api/v1/reviews/facility/{facilityId}",
    "GET /api/v1/reviews/facility/{facilityId}/summary",
    "DELETE /api/v1/reviews/{id}",

    "GET /api/v1/search/venues",
    "GET /api/v1/search/autocomplete",
    "GET /api/v1/search/suggestions",
    "GET /api/v1/search/venues/{id}/similar",
    "POST /api/v1/search/venues/{id}/view",
    "GET /api/v1/search/recent",
    "GET /api/v1/search/recently-viewed",
    "GET /api/v1/search/trending",
    "GET /api/v1/search/popular-cities",

    "GET /api/v1/admin/dashboard",
    "POST /api/v1/admin/users",
    "GET /api/v1/admin/users",
    "PUT /api/v1/admin/users/{id}",
    "POST /api/v1/admin/block",
    "GET /api/v1/admin/vendors",
    "GET /api/v1/admin/vendors/{vendorId}",
    "POST /api/v1/admin/vendors/{vendorId}/kyc/approve",
    "POST /api/v1/admin/vendors/{vendorId}/kyc/reject",
    "GET /api/v1/admin/facilities",
    "POST /api/v1/admin/facilities/{id}/approve",
    "POST /api/v1/admin/fraud-reports",
    "POST /api/v1/admin/fraud-reports/{reportId}/resolve",
    "DELETE /api/v1/admin/users/{id}",

    # Cleanup folder: children first, parents last.
    "DELETE /api/v1/facilities/{id}/images/{childId}",
    "DELETE /api/v1/facilities/{id}/videos/{childId}",
    "DELETE /api/v1/facilities/{id}/policies/{childId}",
    "DELETE /api/v1/facilities/{id}/pricing/{childId}",
    "DELETE /api/v1/facilities/{id}/amenities/{amenityId}",
    "DELETE /api/v1/halls/{id}/packages/{childId}",
    "DELETE /api/v1/halls/{id}/addons/{childId}",
    "DELETE /api/v1/hotels/{id}/room-types/{childId}",
    "DELETE /api/v1/reviews/{id}",
    "DELETE /api/v1/halls/{id}",
    "DELETE /api/v1/hotels/{id}",
    "DELETE /api/v1/users/me",

    "POST /api/v1/bookings/{id}/cancel",
    # Dead last: logout revokes every access token the user holds, so anything
    # after it in the run is unauthenticated.
    "POST /api/v1/auth/logout",
    "POST /api/v1/auth/logout/all-devices",
]
ORDER_INDEX = {k: i for i, k in enumerate(REQUEST_ORDER)}

# Names are written out rather than derived: a generated name like
# "Create Auth Register Verify Email" reads worse than "Verify Email OTP", and
# these are what someone scans down the sidebar looking for.
NAMES = {
    "POST /api/v1/auth/register": "Register (customer)",
    "POST /api/v1/auth/register/vendor": "Register (venue owner)",
    "POST /api/v1/auth/register/verify-email": "Verify email OTP",
    "POST /api/v1/auth/register/verify-phone": "Verify phone OTP",
    "POST /api/v1/auth/otp/resend": "Resend OTP",
    "POST /api/v1/auth/login": "Login",
    "GET /api/v1/auth/me": "Me (from token)",
    "POST /api/v1/auth/refresh": "Refresh token",
    "POST /api/v1/auth/login/refresh": "Refresh token (alias)",
    "POST /api/v1/auth/forgot-password": "Forgot password",
    "POST /api/v1/auth/reset-password": "Reset password",
    "POST /api/v1/auth/logout": "Logout",
    "POST /api/v1/auth/logout/all-devices": "Logout all devices",

    "GET /api/v1/users/me": "Get my profile",
    "PUT /api/v1/users/me": "Update my profile",
    "DELETE /api/v1/users/me": "Delete my account",
    "POST /api/v1/users/me/favourites/{facilityId}": "Add favourite",
    "DELETE /api/v1/users/me/favourites/{facilityId}": "Remove favourite",
    "GET /api/v1/users/me/favourites": "List favourites",
    "POST /api/v1/users/me/favourites/hotels/{facilityId}": "Add favourite hotel (alias)",
    "DELETE /api/v1/users/me/favourites/hotels/{facilityId}": "Remove favourite hotel (alias)",
    "GET /api/v1/users/me/favourites/hotels": "List favourite hotels (alias)",
    "POST /api/v1/users/me/favourites/halls/{facilityId}": "Add favourite hall (alias)",
    "DELETE /api/v1/users/me/favourites/halls/{facilityId}": "Remove favourite hall (alias)",
    "GET /api/v1/users/me/favourites/halls": "List favourite halls (alias)",

    "PUT /api/v1/vendors/me": "Create / update business",
    "GET /api/v1/vendors/me": "Get my business",
    "PUT /api/v1/vendors/me/kyc": "Submit KYC document",
    "PUT /api/v1/vendors/me/bank-account": "Set bank account",
    "GET /api/v1/vendors/me/properties": "My properties",
    "GET /api/v1/vendors/dashboard": "Vendor dashboard",

    "POST /api/v1/facilities": "Create facility (hall or hotel)",
    "GET /api/v1/facilities": "List facilities",
    "GET /api/v1/halls": "List halls (public)",
    "GET /api/v1/halls/{id}": "Get hall",
    "GET /api/v1/hotels/{id}": "Get hotel",
    "PUT /api/v1/halls/{id}": "Update hall",
    "PUT /api/v1/hotels/{id}": "Update hotel",
    "DELETE /api/v1/halls/{id}": "Delete hall",
    "DELETE /api/v1/hotels/{id}": "Delete hotel",
    "GET /api/v1/halls/my-halls": "My halls",
    "GET /api/v1/hotels/my-hotels": "My hotels",
    "GET /api/v1/amenities": "List amenities",
    "POST /api/v1/facilities/{id}/amenities/{amenityId}": "Attach amenity",
    "DELETE /api/v1/facilities/{id}/amenities/{amenityId}": "Detach amenity",

    "POST /api/v1/hotels/{id}/room-types": "Create room type",
    "GET /api/v1/hotels/{id}/room-types": "List room types",
    "DELETE /api/v1/hotels/{id}/room-types/{childId}": "Delete room type",
    "POST /api/v1/halls/{id}/packages": "Create package",
    "GET /api/v1/halls/{id}/packages": "List packages",
    "DELETE /api/v1/halls/{id}/packages/{childId}": "Delete package",
    "POST /api/v1/halls/{id}/addons": "Create add-on",
    "GET /api/v1/halls/{id}/addons": "List add-ons",
    "DELETE /api/v1/halls/{id}/addons/{childId}": "Delete add-on",
    "POST /api/v1/halls/{id}/advance-rules": "Set advance rule",
    "GET /api/v1/halls/{id}/advance-rules": "Get advance rule",
    "POST /api/v1/facilities/{id}/policies": "Create policy",
    "GET /api/v1/facilities/{id}/policies": "List policies",
    "PUT /api/v1/facilities/{id}/policies/{childId}": "Update policy",
    "DELETE /api/v1/facilities/{id}/policies/{childId}": "Delete policy",
    "POST /api/v1/facilities/{id}/pricing": "Create pricing rule",
    "GET /api/v1/facilities/{id}/pricing": "List pricing rules",
    "PUT /api/v1/facilities/{id}/pricing/{childId}": "Update pricing rule",
    "DELETE /api/v1/facilities/{id}/pricing/{childId}": "Delete pricing rule",

    "POST /api/v1/facilities/{id}/images": "Add image",
    "DELETE /api/v1/facilities/{id}/images/{childId}": "Delete image",
    "PUT /api/v1/facilities/{id}/images/{childId}/cover": "Set cover image",
    "PUT /api/v1/facilities/{id}/images/reorder": "Reorder images",
    "POST /api/v1/facilities/{id}/videos": "Add video",
    "DELETE /api/v1/facilities/{id}/videos/{childId}": "Delete video",
    "PUT /api/v1/facilities/{id}/videos/reorder": "Reorder videos",
    "POST /api/v1/facilities/{id}/block": "Block facility",
    "POST /api/v1/facilities/{id}/unblock": "Unblock facility",

    "POST /api/v1/bookings/halls": "Book a hall",
    "POST /api/v1/bookings/hotels": "Book hotel rooms",
    "GET /api/v1/bookings": "My bookings",
    "GET /api/v1/bookings/{id}": "Get booking",
    "POST /api/v1/bookings/{id}/cancel": "Cancel booking",

    "POST /api/v1/bookings/quote": "Price preview (no booking)",
    "POST /api/v1/facilities/{id}/cancellation-policies": "Add cancellation tier",
    "GET /api/v1/facilities/{id}/cancellation-policies": "List cancellation tiers",
    "DELETE /api/v1/facilities/{id}/cancellation-policies/{childId}": "Delete cancellation tier",
    "POST /api/v1/admin/reviews": "Admin: add review",
    "PUT /api/v1/admin/reviews/{id}": "Admin: edit review",
    "DELETE /api/v1/admin/reviews/{id}": "Admin: delete review",
    "POST /api/v1/payments/create": "Create payment",
    "POST /api/v1/payments/webhook": "Gateway webhook (HMAC signed)",
    "POST /api/v1/refunds/{paymentId}": "Refund payment",

    "POST /api/v1/quotes/request": "Request a quote",
    "GET /api/v1/quotes/{id}": "Get quote + versions",
    "GET /api/v1/quotes/my-requests": "My quote requests",
    "GET /api/v1/quotes/owner": "Quotes for my venues",
    "GET /api/v1/quotes/owner/stats": "Quote stats",
    "POST /api/v1/quotes/{id}/reply": "Owner replies with a price",
    "POST /api/v1/quotes/{id}/counter": "Customer counters",
    "POST /api/v1/quotes/{id}/accept": "Accept quote",
    "POST /api/v1/quotes/{id}/reject": "Reject quote",
    "POST /api/v1/quotes/{id}/cancel": "Cancel quote",
    "POST /api/v1/quotes/{id}/messages": "Send message",
    "GET /api/v1/quotes/{id}/messages": "List messages",
    "POST /api/v1/quotes/{id}/attachments": "Add attachment",
    "GET /api/v1/quotes/{id}/attachments": "List attachments",
    "POST /api/v1/quotes/{id}/convert-to-booking": "Convert to booking",

    "POST /api/v1/reviews": "Write a review",
    "GET /api/v1/reviews/facility/{facilityId}": "Reviews for a venue",
    "GET /api/v1/reviews/facility/{facilityId}/summary": "Rating summary",
    "DELETE /api/v1/reviews/{id}": "Delete review",

    "GET /api/v1/search/venues": "Search venues",
    "GET /api/v1/search/autocomplete": "Autocomplete",
    "GET /api/v1/search/suggestions": "Suggestions",
    "GET /api/v1/search/venues/{id}/similar": "Similar venues",
    "POST /api/v1/search/venues/{id}/view": "Record a view",
    "GET /api/v1/search/recent": "My recent searches",
    "GET /api/v1/search/recently-viewed": "My recently viewed",
    "GET /api/v1/search/trending": "Trending searches",
    "GET /api/v1/search/popular-cities": "Popular cities",

    "GET /api/v1/admin/dashboard": "Dashboard",
    "GET /api/v1/admin/users": "List users",
    "POST /api/v1/admin/users": "Create user",
    "PUT /api/v1/admin/users/{id}": "Update user",
    "DELETE /api/v1/admin/users/{id}": "Delete user",
    "POST /api/v1/admin/block": "Block user",
    "GET /api/v1/admin/vendors": "List vendors",
    "GET /api/v1/admin/vendors/{vendorId}": "Get vendor",
    "POST /api/v1/admin/vendors/{vendorId}/kyc/approve": "Approve KYC",
    "POST /api/v1/admin/vendors/{vendorId}/kyc/reject": "Reject KYC",
    "GET /api/v1/admin/facilities": "List facilities",
    "POST /api/v1/admin/facilities/{id}/approve": "Approve / set facility status",
    "POST /api/v1/admin/fraud-reports": "Create fraud report",
    "POST /api/v1/admin/fraud-reports/{reportId}/resolve": "Resolve fraud report",

    "GET /health": "Health check",
}


def request_name(method, path):
    key = f"{method} {path}"
    if key in NAMES:
        return NAMES[key]
    # Fallback for a route added to the code but not yet named here.
    parts = [p for p in path.strip("/").split("/") if p not in ("api", "v1")]
    words = [p for p in parts if not p.startswith("{")]
    return f"{method} " + " ".join(w.replace("-", " ").title() for w in words)


def folder_for(module, method, path):
    """Pick the folder a route belongs in, most specific rule first."""
    for needle, name in PATH_FOLDER:
        if needle in path:
            return name
    # /facilities/{id}/block is media-adjacent listing control, but
    # /admin/block is an admin endpoint and needs an admin token - the bare
    # suffix match sent it to Facility Media, where it was issued the owner's
    # token and 403'd.
    if path.endswith(("/block", "/unblock")) and "/admin/" not in path:
        return "Facility Media"
    folder = MODULE_FOLDER.get(module)
    if folder is None:
        folder = "Facilities (unified)"
    return folder


# Not API endpoints: served files and the health probe's static handler have no
# JSON envelope, so including them makes the collection's shared test fail.
SKIP_ROUTES = {"GET /uploads/"}


def collect_routes():
    # PATCH included: the admin facility editor is a PATCH, and a method missing
    # from this list is dropped silently - the route simply never appears in the
    # collection and nobody notices until someone looks for it.
    pat = re.compile(r'(?:mux\.(?:HandleFunc|Handle)\("|get\(")(GET|POST|PUT|PATCH|DELETE) ([^"]+)"')
    seen, routes = set(), []
    for root, _, files in os.walk(os.path.join(ROOT, "internal")):
        for f in sorted(files):
            if not f.endswith(".go") or f.endswith("_test.go"):
                continue
            full = os.path.join(root, f)
            rel = os.path.relpath(full, ROOT)
            module = rel.split(os.sep)[1]
            src = open(full).read()
            for m in pat.finditer(src):
                method, path = m.group(1), m.group(2)
                key = f"{method} {path}"
                if key in seen or key in SKIP_ROUTES:
                    continue
                seen.add(key)
                if f"{method} {path}" in DESTRUCTIVE:
                    routes.append(("Cleanup (destructive)", method, path))
                    continue
                folder = folder_for(module, method, path)
                routes.append((folder, method, path))
    return routes


def path_var(path, name):
    """Map a placeholder to the collection variable that holds a real id."""
    if name == "id":
        # Most specific first: /admin/reviews/{id} is a review, not the
        # facility the generic fallback would bind.
        if path.startswith("/api/v1/admin/reviews"):
            return "adminReviewId"
        if path.startswith("/api/v1/halls") or "/halls/" in path:
            return "hallId"
        if path.startswith("/api/v1/hotels") or "/hotels/" in path:
            return "hotelId"
        if path.startswith("/api/v1/quotes"):
            return "quoteId"
        if path.startswith("/api/v1/bookings"):
            return "bookingId"
        if path.startswith("/api/v1/reviews"):
            return "reviewId"
        if path.startswith("/api/v1/admin/users"):
            return "userId"
        if path.startswith("/api/v1/admin/facilities") or path.startswith("/api/v1/facilities"):
            return "facilityId"
        if path.startswith("/api/v1/search"):
            return "hallId"
        return "facilityId"
    if name == "facilityId" and "/favourites" in path:
        # Favourites take a facility id; the run creates a hall, so point at it.
        return "hallId"
    if name == "childId" and "/cancellation-policies" in path:
        return "cancellationId"
    if name == "childId":
        # The sub-resource being addressed differs per route.
        for seg, var in (("/images", "imageId"), ("/videos", "videoId"),
                         ("/policies", "policyId"), ("/pricing", "ruleId"),
                         ("/room-types", "roomTypeId"), ("/packages", "packageId"),
                         ("/addons", "addonId")):
            if seg in path:
                return var
        return "childId"
    return name


def to_postman_path(path):
    """{id} -> :id, and return the segment list Postman wants."""
    segs = [s for s in path.strip("/").split("/")]
    out = []
    for s in segs:
        out.append(":" + s[1:-1] if s.startswith("{") else s)
    return out


# Per-request documentation. A bare name tells you which endpoint it is but not
# what it needs, what it returns, or why it is ordered where it is - the Java
# collection documented 105 of its 114 requests and was far easier to work
# through as a result. Anything not listed falls back to describe(), below.
DESCRIPTIONS = {
    "GET /health": "Liveness check. No auth. Also reports whether Postgres is reachable.",

    # --- auth ---
    "POST /api/v1/auth/register": "Step 1 of the customer signup. Creates the account in "
        "PENDING_VERIFICATION and sends an email OTP. No tokens yet - verify first.\n\n"
        "With LOG_OTP_CODES=true the code is written to logs/app.log instead of being "
        "emailed; `make otp` prints the most recent ones.",
    "POST /api/v1/auth/register/vendor": "Same as register, but the account also gets "
        "ROLE_HALL_OWNER. Use this one if you intend to list a property - a plain customer "
        "cannot create facilities.",
    "POST /api/v1/auth/register/verify-email": "Step 2, and where you actually get tokens - "
        "no separate login call is needed straight after. Captures accessToken, refreshToken "
        "and userId into collection variables; a vendor signup also fills ownerToken.",
    "POST /api/v1/auth/register/verify-phone": "Phone equivalent of verify-email. Same body "
        "shape, with the phone number as `target`.",
    "POST /api/v1/auth/otp/resend": "Sends a fresh OTP. Rate limited to 3 per 15 minutes per "
        "IP; the previous code stops working.",
    "POST /api/v1/auth/login": "For returning users. `identifier` is an email or a phone "
        "number - not a field called `email`. Rate limited to 10 per 15 minutes.\n\n"
        "Five wrong passwords lock the account for 15 minutes.",
    "GET /api/v1/auth/me": "The account behind the current access token: id, name, email, "
        "phone, status, roles. For the fuller profile (addresses, KYC, avatar) use "
        "GET /api/v1/users/me instead.",
    "POST /api/v1/auth/refresh": "Exchanges a refresh token for a new access token, since "
        "access tokens last 15 minutes.\n\n"
        "The refresh token ROTATES: the response carries a new one and the old is dead on "
        "arrival. Store both. Replaying a spent refresh token is treated as theft and "
        "revokes every session for that user.",
    "POST /api/v1/auth/logout": "Ends the session. Revokes the refresh token AND every access "
        "token issued to this user - so it logs them out on every device, not just this one.",
    "POST /api/v1/auth/logout/all-devices": "Same effect as logout, addressed by access token "
        "rather than by refresh token.",
    "POST /api/v1/auth/forgot-password": "Emails a reset link. Always answers 200 whether or "
        "not the account exists, so the endpoint cannot be used to discover who has one. "
        "The token is logged locally; `make otp` shows it.",
    "POST /api/v1/auth/reset-password": "Consumes the reset token and sets a new password. "
        "Ends every existing session and clears any lockout.",

    # --- profile ---
    "GET /api/v1/users/me": "The full profile: name, avatar, bio, KYC status, addresses, plus "
        "the account fields (email, phone, verification flags, member since, roles).",
    "PUT /api/v1/users/me": "Updates the profile. Returns the same shape as the GET.",
    "DELETE /api/v1/users/me": "Soft-deletes the account. Rows are kept because bookings and "
        "payments reference this user and have to stay auditable.",

    # --- vendors ---
    "PUT /api/v1/vendors/me": "Creates or updates the vendor business. This is what makes an "
        "account able to list properties - POST /api/v1/facilities returns 403 VENDOR_REQUIRED "
        "until it exists. Run it before the Facilities folder.",
    "GET /api/v1/vendors/me": "The vendor business, including KYC state, bank-account flag, "
        "and counts of hotels and halls owned.",
    "PUT /api/v1/vendors/me/kyc": "Submits KYC for review: document URL plus optional GST and "
        "PAN (both validated against the same patterns Java uses). Moves status to SUBMITTED - "
        "only an admin can approve.",
    "PUT /api/v1/vendors/me/bank-account": "Stores payout details. The account number is "
        "masked everywhere it is read back; only the last four digits are ever returned.",
    "GET /api/v1/vendors/me/properties": "Every facility this vendor owns, paged.",
    "GET /api/v1/vendors/dashboard": "Vendor summary: halls, hotels, KYC status, account "
        "status, plus booking and revenue counts.",

    # --- facilities ---
    "POST /api/v1/facilities": "Creates a hall or a hotel - `type` decides which, and which of "
        "the type-specific fields apply. Requires a vendor business (see PUT /api/v1/vendors/me).\n\n"
        "amenityIds takes amenity CODES (PARKING, AIR_CONDITIONING, CATERING...) rather than row "
        "ids - GET /api/v1/amenities lists them. An unknown code is 400 AMENITY_NOT_FOUND, and one "
        "scoped to the other facility type (a hotel breakfast on a hall) is 400 "
        "AMENITY_TYPE_MISMATCH, rather than being silently dropped.\n\n"
        "Also accepts multipart/form-data, which is how the listing form submits: a `data` part "
        "holding this JSON, `amenityIds` repeated once per checkbox, and `images` file parts. "
        "Uploads are stored and served by the API; the first becomes the cover.\n\n"
        "Captures facilityId, and hallId or hotelId, for the folders that follow.",
    "GET /api/v1/facilities": "Public browse, paged and filterable by type, city and free text.",
    "POST /api/v1/facilities/{id}/images": "Uploads an image file (form-data, drag and "
        "drop into the Body tab). Stored in S3 and served from there.\n\n**Compressed "
        "server-side**: the long edge is capped at 1920px and it is re-encoded as JPEG "
        "q=82, which takes a typical 3-8MB phone photo under ~300KB. Done here rather "
        "than in the browser because anything can POST to this endpoint - a client-side "
        "resize is a courtesy, not a guarantee.\n\nThe type is read from the file's "
        "bytes, not its name: a .png that is really HTML is rejected. JPEG, PNG, WebP "
        "and GIF up to 10MB (WebP and GIF are stored as-is - Go has no encoder for "
        "them).\n\nThe first image on a facility becomes the cover.\n\nThe same route "
        "still accepts JSON `{\"url\": \"...\"}` for a file hosted elsewhere.",
    "POST /api/v1/facilities/{id}/videos": "Uploads a video file (form-data). MP4 or MOV "
        "up to 200MB, stored in S3 unmodified - no transcoding.\n\nAlso accepts JSON "
        "`{\"url\": \"...\"}` for a file hosted elsewhere.",
    "DELETE /api/v1/facilities/{id}/images/{childId}": "Removes the image row and the "
        "stored file behind it. A row recorded by URL leaves the remote file alone - only "
        "objects this API uploaded are deleted.",
    "GET /api/v1/halls/{id}": "One hall, with amenities and images. `startingPrice` is its "
        "base price per day.\n\nThe detail fields also come grouped for the venue screen: "
        "`location` (city, state, country, fullAddress, latitude, longitude), `rating` "
        "(value, reviewCount), `price` (amount, currency, period - DAY for a hall) and "
        "`coordinates`. Each amenity carries an `icon` derived from its code. The flat "
        "fields are unchanged and still sent, so nothing built against them breaks.\n\n"
        "`price` and `coordinates` are null rather than zero when the venue has no price "
        "or was never geocoded - a (0,0) pin would land in the Atlantic.",
    "GET /api/v1/hotels/{id}": "One hotel, with amenities and images. `startingPrice` is the "
        "cheapest room type, so it is null until a room type exists.\n\nSame grouped blocks "
        "as the hall detail - `location`, `rating`, `price`, `coordinates` - alongside the "
        "unchanged flat fields. `price.period` is NIGHT here, and `price` stays null until "
        "a room type gives the hotel a price.",
    "GET /api/v1/halls": "Halls only - the same listing as /facilities?type=MARRIAGE_HALL.",
    "GET /api/v1/hotels": "Hotels only.",
    "GET /api/v1/halls/my-halls": "Halls owned by the caller, including ones still PENDING.",
    "GET /api/v1/hotels/my-hotels": "Hotels owned by the caller.",

    # --- inventory ---
    "POST /api/v1/hotels/{id}/room-types": "Defines a bookable room type: capacity, price per "
        "night, and how many rooms exist. Hotel bookings price and reserve against this, so a "
        "hotel with no room types cannot be booked.",
    "GET /api/v1/hotels/{id}/room-types": "Room types for a hotel.",
    "POST /api/v1/halls/{id}/packages": "A priced bundle (catering, decor...) a customer can "
        "add when booking the hall.",
    "POST /api/v1/facilities/{id}/policies": "Cancellation policy: how much is refunded, and "
        "how long before the event. Refund amounts are computed from this.",
    "POST /api/v1/facilities/{id}/pricing": "A date-range price override - weekends, peak "
        "season, specific dates. Takes precedence over the base price when a booking is priced.",
    "POST /api/v1/halls/{id}/advance-rules": "How much token advance is due to hold the hall, "
        "as a percentage or a flat amount.",

    # --- bookings ---
    "POST /api/v1/bookings/quote": "What the booking will cost, without creating it - the "
        "numbers behind the Review Booking screen: hall price, service fee, taxes, total, "
        "plus availability and the cancellation tiers.\n\nNothing is written and no slot is "
        "held, so availability is true as of this moment only.",
    "POST /api/v1/facilities/{id}/cancellation-policies": "A refund tier: how much is returned "
        "if the customer cancels at least this many days before the event. Refund amounts are "
        "computed from these, so a venue with no tiers refunds nothing.",
    "POST /api/v1/admin/reviews": "Adds a review as admin, skipping the booking requirement "
        "the public endpoint enforces - for seeding ratings imported from elsewhere. Re-posting "
        "for the same facility and user updates that review rather than failing.",
    "POST /api/v1/bookings/halls": "Books a hall for one date and slot. The price is computed "
        "server-side from the hall and any pricing rules - an amount in the body is ignored.\n\n"
        "The slot is claimed with a conditional insert, so of two simultaneous bookings exactly "
        "one wins and the other gets 409 SLOT_TAKEN. Send `idempotentKey` to make a retry "
        "return the original booking instead of creating a second one.",
    "POST /api/v1/bookings/hotels": "Books rooms for a date range. Priced per night per room "
        "type; inventory is decremented per night, so an oversell is rejected.",
    "GET /api/v1/bookings/my-bookings": "The caller's bookings, newest first.",
    "GET /api/v1/bookings/{id}": "One booking. Visible to the customer who made it and to the "
        "facility owner.",
    "POST /api/v1/bookings/{id}/cancel": "Cancels and releases the slot or room inventory. Any "
        "refund follows the facility's cancellation policy.",

    # --- payments ---
    "POST /api/v1/payments/create": "Opens a payment for the booking's outstanding balance and "
        "returns a gateway order id. The amount comes from the booking, never from the body.",
    "POST /api/v1/payments/webhook": "Where the gateway confirms or fails a payment. Requires a "
        "valid X-Signature (HMAC-SHA256 of the raw body, keyed with PAYMENT_WEBHOOK_SECRET) - "
        "without that check anyone reachable could mark any booking paid. Replays of an event "
        "already seen are ignored.",
    "POST /api/v1/refunds/{id}": "Refunds a successful payment, in full or in part.",

    # --- quotes ---
    "POST /api/v1/quotes/request": "A customer asks the owner for a price: event type, "
        "dates, guest count, budget range. Starts the negotiation and captures quoteId.\n\n"
        "Takes `startDate`/`endDate` and `startTime`/`endTime`, the same shape a booking "
        "takes - an accepted quote converts straight into one, so the range agreed here is "
        "the range that gets booked and charged. `endDate` may be omitted for a single-day "
        "event. The old single-date `eventDate` is still accepted, and is echoed back "
        "alongside `startDate` so existing clients keep working.",
    "GET /api/v1/quotes/{id}": "The whole quote: every version, the message thread and any "
        "attachments.",
    "POST /api/v1/quotes/{id}/reply": "The owner answers with an itemised price. Creates "
        "version 1 and moves the quote to REPLIED.",
    "POST /api/v1/quotes/{id}/counter": "Either side proposes different numbers. Each counter "
        "adds a version, so the full negotiation history is preserved.",
    "POST /api/v1/quotes/{id}/accept": "Accepts the current version. Only a quote that carries "
        "a price (REPLIED or COUNTERED) can be accepted.",
    "POST /api/v1/quotes/{id}/convert-to-booking": "Turns an accepted quote into a booking at "
        "the agreed price - the one path where a booking total comes from somewhere other than "
        "the facility's own pricing.\n\nThe body carries no dates: the booking takes the range "
        "and times from the quote itself, so a two-day quote becomes a two-day booking that "
        "holds both days.",
    "GET /api/v1/quotes/owner/stats": "Quote funnel for the owner: totals, pending, accepted, "
        "rejected, booked, and the conversion rate.",

    # --- reviews ---
    "POST /api/v1/reviews": "Leaves a review. Only allowed after a COMPLETED booking of that "
        "facility, and only once per booking.",
    "GET /api/v1/reviews/facility/{id}": "Reviews for a facility, newest first.",
    "GET /api/v1/reviews/facility/{id}/summary": "Average rating, review count, and the "
        "distribution across the five star levels.",

    # --- search ---
    "GET /api/v1/search/venues": "The main search: free text, city, capacity, price range, "
        "date availability, amenities. Returns a paged result plus a searchId for analytics.",
    "GET /api/v1/search/recent": "This user's recent searches, from Redis.",
    "GET /api/v1/search/trending": "Most-searched terms across all users.",
    "GET /api/v1/search/popular-cities": "Cities with the most searches.",

    # --- admin ---
    "GET /api/v1/admin/dashboard": "Platform totals: users, hotels, halls, bookings, revenue, "
        "plus pending KYC and open fraud reports.",
    "GET /api/v1/admin/users": "Every user, paged and searchable by name, email or phone.",
    "POST /api/v1/admin/block": "Blocks or unblocks, as Java's /block does - the same call "
        "again reverses it. `type` is \"user\", \"HOTEL\" or \"MARRIAGE_HALL\"; `id` is the user "
        "id or the facility id.\n\nBlocking a user also revokes their access tokens, so they are "
        "cut off immediately rather than when the current token expires.",
    "POST /api/v1/admin/facilities/{id}/approve": "Approves a listing. A facility is created "
        "PENDING and stays invisible in public search until this runs.",
}


def describe(method, path, folder):
    """Fallback description for anything not in DESCRIPTIONS."""
    resource = folder.rstrip("s").lower()
    if method == "GET":
        what = "Fetches" if "{" in path else "Lists"
        return f"{what} {resource} data. See the response fields for the full shape."
    if method == "POST":
        return f"Creates or acts on a {resource} record."
    if method == "PUT":
        return f"Updates the {resource} record. Send the full object, not a partial one."
    if method == "PATCH":
        return f"Updates the {resource} record. Send only the fields you are changing; anything omitted is left alone."
    if method == "DELETE":
        return f"Deletes the {resource} record. Soft delete where the row is still referenced."
    return ""


# What each folder is for, and what has to have run before it. Shown in
# Postman's sidebar when the folder is selected.
FOLDER_DESC = {
    "Health": "Liveness check. No auth, no setup - run it first to confirm the API is up.",
    "Auth": "Run this folder top to bottom once: register, read the OTP from `make otp`, "
            "verify. Verification is what hands you the tokens, and the test scripts store "
            "them, so everything after this folder is authenticated automatically.",
    "User Profile": "The signed-in user's profile. Needs a customer token from Auth.",
    "Vendors": "Vendor onboarding. PUT /vendors/me has to run before the Facilities folder - "
               "creating a facility without a vendor business is refused with VENDOR_REQUIRED.",
    "Facilities (unified)": "Creating and browsing listings of either type. The two create "
                            "requests here set hallId and hotelId for every folder below.",
    "Marriage Halls": "Hall-specific reads. Needs hallId from Facilities.",
    "Hotels": "Hotel-specific reads. Needs hotelId from Facilities.",
    "Room Types": "Bookable room inventory for a hotel. A hotel with no room types cannot be "
                  "booked and reports a null startingPrice, so run this before Bookings.",
    "Hall Packages": "Priced add-on bundles a customer can attach when booking a hall.",
    "Add-on Services": "Individually priced extras.",
    "Token Advance Rules": "How much deposit is required to hold a hall.",
    "Amenities": "Attaching and detaching amenities. The catalogue itself is seeded.",
    "Facility Policies": "Cancellation terms. Refund amounts are computed from these.",
    "Facility Pricing Rules": "Date-range price overrides - weekends, peak season - which "
                              "take precedence over the base price when a booking is priced.",
    "Facility Media": "Images and videos, plus block/unblock. Each accepts either a JSON "
                      "{url} for a file hosted elsewhere, or a multipart file upload that "
                      "is stored in S3 (local disk when AWS_S3_BUCKET is unset).",
    "Favourites": "The signed-in user's saved facilities.",
    "Bookings": "Creating and managing bookings. Needs hallId or hotelId, and a customer "
                "token. Prices are always computed server-side.",
    "Payments": "Payment creation and the gateway webhook. The webhook needs a valid "
                "X-Signature or it is rejected.",
    "Refunds": "Refunding a successful payment.",
    "Quotes & Negotiation": "The full negotiation: request, reply, counter, accept, and "
                            "convert to a booking. Every offer is kept as a version.",
    "Reviews": "Reviews and rating summaries. Reviewing requires a COMPLETED booking.",
    "Search": "Venue search plus the trending and recent-search endpoints backed by Redis.",
    "Admin": "Admin-only. Needs an account holding ROLE_ADMIN - the adminToken variable.",
    "Cleanup (destructive)": "Deletes, kept last on purpose. Running these earlier removes "
                             "records that the folders above still depend on.",
}


def build_request(folder, method, path):
    key = f"{method} {path}"
    segs = to_postman_path(path)
    query = QUERIES.get(key, "")
    raw = "{{baseUrl}}/" + "/".join(segs)
    if query:
        raw += "?" + query

    url = {"raw": raw, "host": ["{{baseUrl}}"], "path": segs}
    if query:
        url["query"] = [
            {"key": k, "value": v}
            for k, _, v in (p.partition("=") for p in query.split("&"))
        ]
    # A path variable must point at the collection variable the capture scripts
    # actually set. ":id" under /halls is hallId, under /hotels is hotelId, and
    # so on - binding it to a literal "{{id}}" leaves the URL as "/halls/" and
    # every such request 404s.
    variables = [{"key": s[1:], "value": "{{%s}}" % path_var(path, s[1:])}
                 for s in segs if s.startswith(":")]
    if variables:
        url["variable"] = variables

    headers = []
    body = BODIES.get(key)
    if body is not None:
        headers.append({"key": "Content-Type", "value": "application/json"})
    if key == "POST /api/v1/payments/webhook":
        headers.append({"key": "X-Signature", "value": "{{webhookSignature}}",
                        "description": "HMAC-SHA256 of the raw body, keyed with "
                                       "PAYMENT_WEBHOOK_SECRET from .env"})

    doc = DESCRIPTIONS.get(key) or describe(method, path, folder)
    req = {"method": method, "header": headers, "url": url, "description": doc}
    if key not in NO_AUTH:
        token = REQUEST_TOKEN.get(key, FOLDER_TOKEN.get(folder, "accessToken"))
        if token:
            req["auth"] = {"type": "bearer",
                           "bearer": [{"key": "token", "value": "{{%s}}" % token, "type": "string"}]}
    else:
        req["auth"] = {"type": "noauth"}

    if body is not None:
        req["body"] = {"mode": "raw", "raw": json.dumps(body, indent=2),
                       "options": {"raw": {"language": "json"}}}
    return req


# Scripts that capture ids so the collection chains without manual copy-paste.
CAPTURE = {
    "POST /api/v1/admin/reviews": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("adminReviewId", d.id);""",
    "POST /api/v1/facilities/{id}/cancellation-policies": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("cancellationId", d.id);""",
    "POST /api/v1/auth/login": """const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("accessToken", d.accessToken);
  pm.collectionVariables.set("refreshToken", d.refreshToken);
  pm.collectionVariables.set("userId", d.user.id);
}""",
    "POST /api/v1/auth/register/verify-email": """const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("accessToken", d.accessToken);
  pm.collectionVariables.set("refreshToken", d.refreshToken);
  pm.collectionVariables.set("userId", d.user.id);
  // A vendor signup lands here too; keep an owner token for the venue folders.
  if ((d.user.roles || []).includes("ROLE_HALL_OWNER")) {
    pm.collectionVariables.set("ownerToken", d.accessToken);
  }
  if ((d.user.roles || []).includes("ROLE_ADMIN")) {
    pm.collectionVariables.set("adminToken", d.accessToken);
  }
}""",
    "POST /api/v1/auth/refresh": """const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("accessToken", d.accessToken);
  pm.collectionVariables.set("refreshToken", d.refreshToken);
}""",
    "POST /api/v1/facilities": """const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("facilityId", d.id);
  if (d.type === "MARRIAGE_HALL") pm.collectionVariables.set("hallId", d.id);
  else pm.collectionVariables.set("hotelId", d.id);
}""",
    "POST /api/v1/halls/{id}/packages": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("packageId", d.id);""",
    "POST /api/v1/halls/{id}/addons": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("addonId", d.id);""",
    "POST /api/v1/hotels/{id}/room-types": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("roomTypeId", d.id);""",
    "POST /api/v1/facilities/{id}/images": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("imageId", d.id);""",
    "POST /api/v1/facilities/{id}/videos": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("videoId", d.id);""",
    "POST /api/v1/bookings/halls": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("bookingId", d.id);""",
    "POST /api/v1/bookings/hotels": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("bookingId", d.id);""",
    "POST /api/v1/payments/create": """const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("paymentId", d.id);
  pm.collectionVariables.set("gatewayOrderId", d.gatewayOrderId);
}""",
    "POST /api/v1/quotes/request": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("quoteId", d.id);""",
    "GET /api/v1/search/venues": """const d = pm.response.json().data;
if (d && d.searchId) pm.collectionVariables.set("searchId", d.searchId);""",
    "GET /api/v1/vendors/me": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("vendorId", d.id);""",
    "GET /api/v1/users/me": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("userId", d.id);""",
    "GET /api/v1/amenities": """// Grab an id so Attach/Detach amenity have something to point at.
const d = pm.response.json().data;
if (d && d.length) pm.collectionVariables.set("amenityId", d[0].id);""",
    "GET /api/v1/admin/vendors": """const d = pm.response.json().data;
if (d && d.content && d.content.length) pm.collectionVariables.set("vendorId", d.content[0].id);""",
    "GET /api/v1/reviews/facility/{facilityId}": """const d = pm.response.json().data;
if (d && d.content && d.content.length) pm.collectionVariables.set("reviewId", d.content[0].id);""",
    "GET /api/v1/facilities/{id}/policies": """const d = pm.response.json().data;
if (d && d.length) pm.collectionVariables.set("policyId", d[0].id);""",
    "GET /api/v1/facilities/{id}/pricing": """const d = pm.response.json().data;
if (d && d.length) pm.collectionVariables.set("ruleId", d[0].id);""",
    "POST /api/v1/admin/fraud-reports": """const d = pm.response.json().data;
if (d) pm.collectionVariables.set("reportId", d.id);""",
}

# Almost every response uses the same envelope, so one test covers the whole
# collection. The exception is the one-tap acknowledge/decline links, which
# render an HTML page: they are opened in a phone browser from an SMS, not
# called by the app, so asserting JSON on them fails for the wrong reason.
COMMON_TEST = """const ct = pm.response.headers.get("Content-Type") || "";
if (ct.indexOf("text/html") !== -1) {
    pm.test("renders a page", function () {
        pm.expect(pm.response.text()).to.include("<html");
    });
} else {
    pm.test("returns the ApiResponse envelope", function () {
        const b = pm.response.json();
        pm.expect(b).to.have.property("success");
        pm.expect(b).to.have.property("message");
        pm.expect(b).to.have.property("timestamp");
    });
}"""


def main():
    routes = collect_routes()
    folders = {}
    for folder, method, path in routes:
        folders.setdefault(folder, []).append((method, path))

    items = []
    for folder in FOLDER_ORDER:
        if folder not in folders:
            continue
        # Unlisted routes (aliases, rarely-used variants) sort after the
        # curated sequence rather than disappearing. DELETEs sort last within
        # their folder regardless: running the whole collection top to bottom
        # must not destroy a record that later folders still need - deleting the
        # hall mid-run left every booking and quote request with an empty id.
        # Inside Cleanup everything is a DELETE, so the "deletes last" rule
        # would flatten the child-before-parent order it needs.
        deletes_last = folder != "Cleanup (destructive)"
        entries = sorted(
            folders[folder],
            key=lambda x: (deletes_last and x[0] == "DELETE",
                           ORDER_INDEX.get(f"{x[0]} {x[1]}", 10_000), x[1], x[0]))
        sub = []
        for method, path in entries:
            key = f"{method} {path}"
            item = {
                "name": request_name(method, path),
                "request": build_request(folder, method, path),
                "response": [],
            }
            events = []
            script = COMMON_TEST
            if key in CAPTURE:
                script = CAPTURE[key] + "\n\n" + COMMON_TEST
            events.append({"listen": "test",
                           "script": {"type": "text/javascript",
                                      "exec": script.split("\n")}})
            item["event"] = events
            sub.append(item)
        # The sample create body makes a MARRIAGE_HALL; add a second request
        # that makes a HOTEL, otherwise hotelId is never set and every
        # /hotels/{id} request in the run 404s on an empty path segment.
        if folder == "Facilities (unified)":
            hotel = {
                "name": "Create hotel",
                "request": build_request(folder, "POST", "/api/v1/facilities"),
                "response": [],
                "event": [{"listen": "test", "script": {"type": "text/javascript",
                    "exec": (CAPTURE["POST /api/v1/facilities"] + "\n\n" + COMMON_TEST).split("\n")}}],
            }
            hotel["request"]["body"]["raw"] = json.dumps(
                BODIES["POST /api/v1/facilities#hotel"], indent=2)
            hotel["request"]["description"] = (
                "Same endpoint as above with type=HOTEL, so that hotelId is set for the "
                "Hotels and Room Types folders. Without it every /hotels/{id} request in "
                "the run addresses an empty id and 404s.")
            sub.insert(1, hotel)

        # /admin/block is a toggle, so a single request leaves the account in
        # whichever state it was not in before - blocked on one run, unblocked
        # on the next. Pairing block with unblock makes the run idempotent and
        # proves the toggle actually reverses. The unblock also has to happen
        # before Cleanup: a blocked user's access token is revoked, which would
        # leave logout unauthenticated.
        # Upload requests carry a file part, which a collection cannot hold -
        # `src` is null and you pick a file (drag and drop works) in the Body
        # tab before sending.
        #
        # They sit alongside the JSON {url} requests rather than replacing
        # them: with no file attached an upload is a 400, so making it the
        # primary request left imageId/videoId unset and broke every later
        # media request in the automated run.
        if folder == "Facility Media":
            for suffix, field, name, desc in (
                ("image", "image", "Upload image (drag & drop a file)",
                 "Uploads an actual image file. Stored in S3 and served from there - "
                 "unlike the JSON request above, which only records a URL somebody else "
                 "is hosting.\n\n**Compressed server-side**: the long edge is capped at "
                 "1920px and it is re-encoded as JPEG q=82. A 3MB phone photo lands at "
                 "well under half that; measured 3.0MB -> 1.27MB on a 4000x3000 source. "
                 "Done here, not in the browser, because anything can POST to this "
                 "endpoint - a client-side resize is a courtesy, not a guarantee.\n\n"
                 "The type is read from the file's bytes, not its name: a .png that is "
                 "really HTML is rejected. JPEG, PNG, WebP and GIF up to 10MB (WebP and "
                 "GIF are stored as uploaded - Go has no encoder for them).\n\n"
                 "**Pick a file in the Body tab before sending.**"),
                ("video", "video", "Upload video (drag & drop a file)",
                 "Uploads an actual video file to S3. MP4 or MOV up to 200MB, stored "
                 "unmodified - no transcoding.\n\n**Pick a file in the Body tab before "
                 "sending.**"),
            ):
                path = "/api/v1/facilities/{id}/" + suffix + "s"
                v = {
                    "name": name,
                    "request": build_request(folder, "POST", path),
                    "response": [],
                    "event": [{"listen": "test", "script": {"type": "text/javascript",
                        "exec": COMMON_TEST.split("\n")}}],
                }
                v["request"]["body"] = {"mode": "formdata", "formdata": [
                    {"key": field, "type": "file", "src": None,
                     "description": "Pick a file - drag and drop works"},
                ]}
                v["request"]["header"] = [
                    hdr for hdr in v["request"].get("header", [])
                    if hdr.get("key") != "Content-Type"
                ]
                v["request"]["description"] = desc
                at = next((i for i, x in enumerate(sub)
                           if x["name"] == "Add " + suffix), len(sub) - 1)
                sub.insert(at + 1, v)

        if folder == "Admin":
            base = "POST /api/v1/admin/block"
            at = next(i for i, x in enumerate(sub) if x["name"] == "Block user")
            for off, (suffix, name, desc) in enumerate((
                ("#unblock", "Unblock user (same call again)",
                 "The same endpoint with the same id: it toggles, so this reverses the "
                 "block above and restores the account's login."),
                ("#facility", "Block facility listing",
                 "type=MARRIAGE_HALL blocks the listing instead of a user, which hides it "
                 "from public search. Sending it again restores the listing."),
            ), start=1):
                v = {
                    "name": name,
                    "request": build_request(folder, "POST", "/api/v1/admin/block"),
                    "response": [],
                    "event": [{"listen": "test", "script": {"type": "text/javascript",
                        "exec": COMMON_TEST.split("\n")}}],
                }
                v["request"]["body"]["raw"] = json.dumps(BODIES[base + suffix], indent=2)
                v["request"]["description"] = desc
                sub.insert(at + off, v)
        folder_item = {"name": f"{folder} ({len(sub)})", "item": sub}
        if folder in FOLDER_DESC:
            folder_item["description"] = FOLDER_DESC[folder]
        items.append(folder_item)

    total = sum(len(f["item"]) for f in items)
    collection = {
        "info": {
            "name": "Marriage Hall & Hotel Booking (Go)",
            "description": (
                f"{total} endpoints, generated from the routes registered in the Go "
                "source by scripts/gen_postman.py.\n\n"
                "Getting started:\n"
                "1. Start the API: `make start`\n"
                "2. Auth > Create Auth Register Vendor  (creates a venue owner)\n"
                "3. There is no mail service, so read the OTP from the log:\n"
                "   `make otp`  ->  set it as the `otp` collection variable\n"
                "4. Auth > Create Auth Register Verify Email\n"
                "   This stores accessToken / ownerToken automatically.\n"
                "5. Facilities > Create Facilities  (stores hallId)\n\n"
                "Tokens and ids are captured by test scripts, so most requests work "
                "without copying anything by hand. Access tokens last 15 minutes; "
                "run Auth > Create Auth Refresh when you get a 401.\n\n"
                "The payment webhook needs an HMAC signature over the raw body, keyed "
                "with PAYMENT_WEBHOOK_SECRET from .env - see README."
            ),
            "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
        },
        # Runs before every request, but only does anything on the first: it
        # mints one identity per run so the collection can be run repeatedly.
        # Without this the second run hits 409 EMAIL_EXISTS immediately and
        # nothing downstream ever gets a token.
        "event": [{
            "listen": "prerequest",
            "script": {"type": "text/javascript", "exec": [
                "// An environment value wins: scripts/postman_run.py passes a",
                "// pre-verified identity in that way, and regenerating one here",
                "// would leave the tokens it also passed pointing at a different",
                "// account.",
                "const injected = pm.environment.get('runId');",
                "if (injected) {",
                "  ['runId','customerEmail','customerPhone','ownerEmail','ownerPhone',",
                "   'accessToken','refreshToken','ownerToken','adminToken','userId'].forEach(k => {",
                "    const v = pm.environment.get(k);",
                "    if (v) pm.collectionVariables.set(k, v);",
                "  });",
                "} else if (!pm.collectionVariables.get('runId')) {",
                "  const id = Date.now().toString().slice(-9);",
                "  pm.collectionVariables.set('runId', id);",
                "  pm.collectionVariables.set('customerEmail', `priya+${id}@example.com`);",
                "  pm.collectionVariables.set('ownerEmail',    `rajesh+${id}@example.com`);",
                "  // +91 then 10 digits, to satisfy the phone format check.",
                "  pm.collectionVariables.set('customerPhone', `+91${id}0`);",
                "  pm.collectionVariables.set('ownerPhone',    `+91${id}1`);",
                "  console.log('run identity:', pm.collectionVariables.get('customerEmail'));",
                "}",
            ]},
        }],
        "item": items,
        "variable": [
            {"key": "baseUrl", "value": "http://localhost:8080"},
            {"key": "runId", "value": "",
             "description": "Set once per run by the collection pre-request script. "
                            "Clear it to force a brand-new identity."},
            {"key": "customerEmail", "value": ""},
            {"key": "customerPhone", "value": ""},
            {"key": "ownerEmail", "value": ""},
            {"key": "ownerPhone", "value": ""},
            {"key": "accessToken", "value": ""},
            {"key": "refreshToken", "value": ""},
            {"key": "ownerToken", "value": ""},
            {"key": "adminToken", "value": ""},
            {"key": "otp", "value": "000000",
             "description": "Prefilled because OTP_FIXED_CODE=000000 in .env makes "
                            "every OTP this value. Remove that setting and codes go "
                            "back to random - read them with `make otp` and paste here."},
            {"key": "resetToken", "value": "", "description": "From `make otp`"},
            {"key": "userId", "value": ""},
            {"key": "facilityId", "value": ""},
            {"key": "hallId", "value": ""},
            {"key": "hotelId", "value": ""},
            {"key": "roomTypeId", "value": ""},
            {"key": "packageId", "value": ""},
            {"key": "addonId", "value": ""},
            {"key": "imageId", "value": ""},
            {"key": "imageId2", "value": ""},
            {"key": "videoId", "value": ""},
            {"key": "videoId2", "value": ""},
            {"key": "bookingId", "value": ""},
            {"key": "paymentId", "value": ""},
            {"key": "gatewayOrderId", "value": ""},
            {"key": "quoteId", "value": ""},
            {"key": "vendorId", "value": ""},
            {"key": "reportId", "value": ""},
            {"key": "cancellationId", "value": ""},
            {"key": "adminReviewId", "value": ""},
            {"key": "searchId", "value": ""},
            {"key": "amenityId", "value": ""},
            {"key": "policyId", "value": ""},
            {"key": "ruleId", "value": ""},
            {"key": "childId", "value": ""},
            {"key": "id", "value": ""},
            {"key": "webhookSignature", "value": ""},
        ],
    }
    json.dump(collection, sys.stdout, indent=2)
    sys.stdout.write("\n")
    print(f"generated {total} requests in {len(items)} folders", file=sys.stderr)


if __name__ == "__main__":
    main()
