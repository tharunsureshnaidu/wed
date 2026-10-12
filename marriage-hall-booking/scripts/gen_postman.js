const fs = require("fs");
const path = require("path");

const ROOT = path.resolve(__dirname, "..");

// Example bodies matching Go request structs
const BODIES = {
  "POST /api/v1/feedback": {
    rating: 4,
    feedback: "Booking flow was smooth. UPI autopay would help.",
    appVersion: "1.4.2",
    platform: "ANDROID",
  },
  "PUT /api/v1/admin/feedback/{id}": {
    status: "RESOLVED",
    adminNote: "Passed to the payments team; UPI autopay is on the roadmap.",
  },
  "POST /api/v1/help/messages": {
    message: "I cannot see the invoice for my booking last week.",
  },
  "POST /api/v1/users/me/favourites": {
    entityId: "{{hallId}}",
    type: "HALL",
    favorite: true,
  },
  "POST /api/v1/admin/privacy-policy": {
    title: "Privacy Policy",
    content: "We collect only what a booking needs: your name, contact and stay dates.",
  },
  "PUT /api/v1/admin/privacy-policy": {
    title: "Privacy Policy",
    content: "Updated: we now also record the device you signed in from.",
  },
  "POST /api/feedback": {
    rating: 4,
    feedback: "Booking flow was smooth. UPI autopay would help.",
    appVersion: "1.4.2",
    platform: "ANDROID",
  },
  "PUT /api/admin/feedback/{id}": {
    status: "RESOLVED",
    adminNote: "Passed to the payments team; UPI autopay is on the roadmap.",
  },
  "POST /api/admin/privacy-policy": {
    title: "Privacy Policy",
    content: "We collect only what a booking needs: your name, contact and stay dates.",
  },
  "PUT /api/admin/privacy-policy": {
    title: "Privacy Policy",
    content: "Updated: we now also record the device you signed in from.",
  },
  "POST /api/v1/facilities/compare": {
    type: "hall",
    venue_ids: ["{{hallId}}", "{{hallId2}}"],
    lat: 12.9716,
    lng: 77.5946,
  },
  "POST /api/v1/venues/compare": {
    type: "hall",
    venue_ids: ["{{hallId}}", "{{hallId2}}"],
    lat: 12.9716,
    lng: 77.5946,
  },
  "POST /api/v1/bookings/{id}/status": {
    type: "confirm",
    reason: "Confirmed - our team will call to coordinate layout.",
  },
  "PUT /api/v1/bookings/{id}/status": {
    type: "confirm",
    reason: "Confirmed - our team will call to coordinate layout.",
  },
  "PATCH /api/v1/bookings/{id}/status": {
    type: "confirm",
    reason: "Confirmed - our team will call to coordinate layout.",
  },
  "POST /api/v1/bookings/{id}/confirm": {
    reason: "Confirmed - we will call you to plan the layout.",
  },
  "POST /api/v1/bookings/{id}/reject": {
    reason: "Date already held for a prior function",
  },
  "POST /api/v1/auth/register": {
    fullName: "Priya Sharma",
    email: "{{customerEmail}}",
    phoneNumber: "{{customerPhone}}",
    password: "SecurePass@123",
    // Optional: a one-line string, or an object whose every part is optional.
    address: { street: "12 MG Road", city: "Bengaluru", state: "Karnataka", zipCode: "560001", country: "India" },
  },
  "POST /api/v1/auth/register/vendor": {
    fullName: "Rajesh Kumar",
    email: "{{ownerEmail}}",
    phoneNumber: "{{ownerPhone}}",
    password: "SecurePass@123",
    // Also seeds the vendor's business_address.
    address: { street: "45 Brigade Road", city: "Bengaluru", state: "Karnataka", zipCode: "560025", country: "India" },
  },
  "POST /api/v1/auth/register/verify-email": {
    target: "{{customerEmail}}",
    otpCode: "{{otp}}",
  },
  "POST /api/v1/auth/register/verify-phone": {
    target: "{{customerPhone}}",
    otpCode: "{{otp}}",
  },
  "POST /api/v1/auth/login": {
    identifier: "{{customerEmail}}",
    password: "SecurePass@123",
  },
  "POST /api/v1/auth/otp/resend": {
    target: "{{customerEmail}}",
    otpType: "EMAIL_VERIFICATION",
  },
  "POST /api/v1/auth/refresh": { refreshToken: "{{refreshToken}}" },
  "POST /api/v1/auth/login/refresh": { refreshToken: "{{refreshToken}}" },
  "POST /api/v1/auth/logout": { refreshToken: "{{refreshToken}}" },
  "POST /api/v1/auth/forgot-password": { identifier: "{{customerEmail}}" },
  "POST /api/v1/auth/reset-password": {
    identifier: "{{customerEmail}}",
    token: "{{resetToken}}",
    newPassword: "NewSecurePass@123",
  },
  "PUT /api/v1/users/me": {
    firstName: "Priya",
    lastName: "Sharma",
    bio: "Wedding planner looking for the perfect venue",
  },
  "PUT /api/v1/vendors/me": {
    businessName: "Kumar Weddings & Events",
    businessAddress: "42 MG Road, Bengaluru 560001",
    businessDescription: "Premium wedding venues since 1998",
    businessType: "VENUE",
  },
  "PUT /api/v1/vendors/me/kyc": {
    documentUrl: "https://files.example.com/kyc/rajesh-pan.pdf",
  },
  "PUT /api/v1/vendors/me/bank-account": {
    accountNumber: "1234567890123456",
    ifsc: "HDFC0001234",
    holderName: "Rajesh Kumar",
  },
  "POST /api/v1/facilities#hotel": {
    name: "Grand Residency",
    type: "HOTEL",
    description: "4-star business hotel near the venue",
    city: "Bengaluru",
    fullAddress: "9 Residency Road",
    state: "Karnataka",
    zipcode: "560025",
    country: "India",
    lat: 12.9716,
    lng: 77.5946,
    amenityIds: ["PARKING", "AIR_CONDITIONING", "BREAKFAST_INCLUDED"],
    starRating: 4,
    checkInTime: "14:00",
    checkOutTime: "11:00",
  },
  "POST /api/v1/facilities#hall2": {
    name: "Kumar Royal Garden",
    type: "HALL",
    description: "Spacious open lawn venue for royal weddings",
    city: "Bengaluru",
    fullAddress: "88 Outer Ring Road",
    state: "Karnataka",
    zipcode: "560103",
    country: "India",
    lat: 12.9780,
    lng: 77.6400,
    amenityIds: ["PARKING", "AIR_CONDITIONING", "CATERING"],
    capacityPax: 1200,
    areaSqft: 20000,
    basePricePerDay: 200000,
    seatingCapacity: 800,
    floatingCapacity: 1200,
    minBookingSize: 150,
  },
  "POST /api/v1/facilities": {
    name: "Kumar Grand Palace",
    type: "HALL",
    description: "Premium wedding venue with lawn and banquet hall",
    city: "Bengaluru",
    fullAddress: "42 MG Road",
    state: "Karnataka",
    zipcode: "560001",
    country: "India",
    lat: 12.9716,
    lng: 77.5946,
    amenityIds: ["PARKING", "AIR_CONDITIONING", "CATERING"],
    capacityPax: 800,
    areaSqft: 12000,
    basePricePerDay: 150000,
    seatingCapacity: 600,
    floatingCapacity: 800,
    minBookingSize: 100,
  },
  "PUT /api/v1/halls/{id}": {
    name: "Kumar Grand Palace (Renovated)",
    type: "HALL",
    city: "Bengaluru",
    capacityPax: 900,
    basePricePerDay: 175000,
  },
  "PUT /api/v1/hotels/{id}": {
    name: "Grand Residency",
    type: "HOTEL",
    city: "Bengaluru",
    starRating: 4,
    checkInTime: "14:00",
    checkOutTime: "11:00",
  },
  "POST /api/v1/hotels/{id}/room-types": {
    name: "Deluxe Room",
    description: "King bed with city view",
    capacityAdults: 2,
    capacityChildren: 1,
    basePricePerNight: 4500,
    totalRooms: 10,
  },
  "POST /api/v1/halls/{id}/packages": {
    name: "Premium Wedding Package",
    description: "Full hall rental with decoration and catering",
    price: 350000,
    guestCapacity: 500,
    includesCatering: true,
    includedServices: "Decoration, Catering, DJ, Photography",
    excludedServices: "Alcohol, Fireworks",
  },
  "POST /api/v1/halls/{id}/addons": {
    name: "Live Music Band",
    description: "3-hour live performance",
    price: 25000,
    serviceType: "BAND",
    unit: "EVENT",
  },
  "POST /api/v1/halls/{id}/advance-rules": {
    advancePercentage: 25,
    minAdvanceAmount: 50000,
    balanceDueDaysBefore: 15,
    autoReminderEnabled: true,
  },
  "POST /api/v1/facilities/{id}/policies": {
    policyType: "ALCOHOL",
    description: "Alcohol permitted only with prior written approval.",
  },
  "PUT /api/v1/facilities/{id}/policies/{childId}": {
    policyType: "ALCOHOL",
    description: "Alcohol permitted with prior approval, until 11pm.",
  },
  "POST /api/v1/facilities/{id}/pricing": {
    eventType: "Wedding",
    dayType: "WEEKEND",
    season: "Peak Season",
    minGuests: 300,
    maxGuests: 700,
    price: 180000,
    validFrom: "2027-10-01",
    validTo: "2028-03-31",
  },
  "PUT /api/v1/facilities/{id}/pricing/{childId}": {
    eventType: "Wedding",
    dayType: "WEEKEND",
    season: "Peak Season",
    minGuests: 300,
    maxGuests: 700,
    price: 200000,
    validFrom: "2027-10-01",
    validTo: "2028-03-31",
  },
  "POST /api/v1/facilities/{id}/images": {
    url: "https://cdn.example.com/venues/hall-main.jpg",
    isCover: true,
    sortOrder: 0,
  },
  "POST /api/v1/facilities/{id}/videos": {
    url: "https://cdn.example.com/venues/hall-tour.mp4",
    thumbnailUrl: "https://cdn.example.com/venues/hall-tour.jpg",
    durationSeconds: 90,
    sortOrder: 0,
  },
  "PUT /api/v1/facilities/{id}/images/reorder": { order: ["{{imageId}}"] },
  "PUT /api/v1/facilities/{id}/videos/reorder": { order: ["{{videoId}}"] },
  "POST /api/v1/bookings/halls": {
    hallId: "{{hallId}}",
    startDate: "2027-06-25",
    endDate: "2027-06-26",
    startTime: "18:00",
    endTime: "23:00",
    guestCount: 400,
    eventType: "WEDDING",
    roomCount: 12,
    idempotentKey: "hall-{{runId}}",
  },
  "PATCH /api/v1/admin/facilities/{id}": {
    name: "Royal Grand Palace",
    contactPhone: "+919876543210",
    basePricePerDay: 125000,
    discountPercent: 15,
    discountLabel: "Monsoon offer",
  },
  "PUT /api/v1/admin/facilities/{id}/rating": {
    avgRating: 4.3,
    reviewCount: 218,
  },
  "PUT /api/v1/admin/support": {
    supportEmail: "support@tripfactory.travel",
    supportPhone: "+91 80 4000 0000",
    supportWhatsapp: "",
    supportHours: "Mon-Sat, 9:00 AM - 7:00 PM",
    supportAddress: "",
  },
  "POST /api/v1/devices": {
    token: "fcm-device-token-{{runId}}",
    platform: "ANDROID",
  },
  "PUT /api/v1/users/me/location": {
    lat: 12.9716,
    lng: 77.5946,
    source: "DEVICE",
  },
  "PUT /api/v1/users/me/geo-notifications": {
    enabled: true,
  },
  "POST /api/v1/coupons": {
    code: "WED{{runId}}",
    discountType: "PERCENT",
    discountValue: 15,
    maxDiscount: 10000,
    minBookingAmount: 50000,
    facilityId: "{{hallId}}",
  },
  "PUT /api/v1/coupons/{id}": {
    code: "WED{{runId}}",
    discountType: "PERCENT",
    discountValue: 20,
    maxDiscount: 12000,
    isActive: true,
  },
  "POST /api/v1/coupons/validate": {
    code: "WED{{runId}}",
    amount: 200000,
    facilityId: "{{hallId}}",
  },
  "POST /api/v1/admin/coupons": {
    code: "ALLHALLS{{runId}}",
    description: "10% off at any venue",
    appliesTo: "ALL",
    discountType: "PERCENT",
    discountValue: 10,
    maxDiscount: 5000,
    usageLimit: 100,
  },
  "PUT /api/v1/admin/coupons/{id}": {
    code: "ALLHALLS{{runId}}",
    discountType: "PERCENT",
    discountValue: 12,
    maxDiscount: 6000,
    usageLimit: 100,
    isActive: true,
  },
  "POST /api/v1/bookings/quote": {
    hallId: "{{hallId}}",
    startDate: "2027-06-25",
    endDate: "2027-06-26",
    startTime: "18:00",
    endTime: "23:00",
    guestCount: 400,
  },
  "POST /api/v1/facilities/{id}/cancellation-policies": {
    policyType: "FLEXIBLE",
    daysBeforeEvent: 30,
    refundPercentage: 100,
  },
  "POST /api/v1/admin/reviews": {
    facilityId: "{{hallId}}",
    rating: 5,
    title: "Excellent venue",
    comment: "Beautiful hall, great service.",
  },
  "PUT /api/v1/admin/reviews/{id}": {
    rating: 4,
    title: "Very good",
    comment: "Updated by admin.",
  },
  "POST /api/v1/bookings/hotels": {
    hotelId: "{{hotelId}}",
    checkIn: "2027-05-01",
    checkOut: "2027-05-03",
    rooms: [{ roomTypeId: "{{roomTypeId}}", quantity: 2 }],
    idempotentKey: "hotel-{{runId}}",
    guestName: "Priya Sharma",
    guestEmail: "{{customerEmail}}",
    guestPhone: "{{customerPhone}}",
  },
  "POST /api/v1/payments/create": { bookingId: "{{bookingId}}" },
  "POST /api/v1/payments/webhook": {
    eventId: "evt-001",
    orderId: "{{gatewayOrderId}}",
    paymentId: "pay_abc123",
    status: "SUCCESS",
    amount: 150000,
  },
  "POST /api/v1/quotes/request": {
    facilityId: "{{hallId}}",
    eventType: "Wedding Reception",
    startDate: "2027-01-15",
    endDate: "2027-01-16",
    startTime: "18:00",
    endTime: "23:00",
    guestCount: 450,
    budgetMin: 250000,
    budgetMax: 400000,
    specialRequirements: "Vegetarian catering and a stage for the sangeet",
    preferredContact: "PHONE",
  },
  "POST /api/v1/quotes/{id}/reply": {
    items: [
      { name: "Hall Rental (Full Day)", quantity: 1, unitPrice: 150000 },
      { name: "Vegetarian Catering (per plate)", quantity: 450, unitPrice: 600 },
      { name: "Stage & Sangeet Decoration", quantity: 1, unitPrice: 45000 },
    ],
    discount: 10000,
    tax: 18000,
    serviceCharge: 5000,
    notes: "Valid for 14 days",
    validUntil: "2027-01-01",
  },
  "POST /api/v1/quotes/{id}/counter": {
    items: [
      { name: "Hall Rental (Full Day)", quantity: 1, unitPrice: 150000 },
      { name: "Vegetarian Catering (per plate)", quantity: 450, unitPrice: 500 },
      { name: "Stage & Sangeet Decoration", quantity: 1, unitPrice: 35000 },
    ],
    discount: 15000,
    tax: 15000,
    serviceCharge: 5000,
  },
  "POST /api/v1/quotes/{id}/reject": { reason: "Not available on that date" },
  "POST /api/v1/quotes/{id}/cancel": { reason: "Changed my mind" },
  "POST /api/v1/quotes/{id}/messages": {
    messageType: "TEXT",
    content: "This looks great, thank you!",
  },
  "POST /api/v1/quotes/{id}/attachments": {
    fileName: "floor-plan.pdf",
    fileUrl: "https://files.example.com/quotes/floor-plan.pdf",
    contentType: "application/pdf",
    sizeBytes: 248000,
  },
  "POST /api/v1/quotes/{id}/convert-to-booking": {
    idempotentKey: "quote-{{runId}}",
  },
  "POST /api/v1/reviews": {
    facilityId: "{{hallId}}",
    bookingId: "{{bookingId}}",
    rating: 5,
    title: "Excellent venue",
    comment: "Beautiful venue and excellent service.",
  },
  "POST /api/v1/facilities/{id}/faqs": {
    question: "Is outside catering allowed?",
    answer: "Yes, with a royalty fee of Rs 50 per plate.",
    sortOrder: 1,
  },
  "PUT /api/v1/facilities/{id}/faqs/{childId}": {
    answer: "Yes, with a royalty fee of Rs 75 per plate.",
  },
  "PUT /api/v1/facilities/{id}/events": {
    events: ["WEDDING", "RECEPTION", "BIRTHDAY", "CORPORATE_EVENT"],
  },
  "POST /api/v1/admin/users": {
    fullName: "Staff Member",
    email: "staff@example.com",
    password: "StaffPass@123",
    role: "ROLE_STAFF",
  },
  "PUT /api/v1/admin/users/{id}": { fullName: "Updated Name", status: "ACTIVE" },
  "POST /api/v1/admin/block": {
    id: "{{userId}}",
    type: "user",
    reason: "Suspicious activity",
  },
  "POST /api/v1/admin/block#unblock": {
    id: "{{userId}}",
    type: "user",
    reason: "Appeal upheld",
  },
  "POST /api/v1/admin/block#facility": {
    id: "{{hallId}}",
    type: "HALL",
    reason: "Listing under review",
  },
};

// Query strings worth pre-filling
const QUERIES = {
  "GET /api/v1/admin/analytics/decisions": "entity=&from=&until=",
  "GET /api/v1/users/me/dashboard": "lat=12.9716&lng=77.5946",
  "GET /api/v1/facilities": "type=HALL&search=&city=&page=0&size=20&lat=12.9716&lng=77.5946",
  "GET /api/v1/venues": "type=&eventType=&search=&city=&page=0&size=20&lat=12.9716&lng=77.5946",
  "GET /api/v1/halls": "search=&page=0&size=20&lat=12.9716&lng=77.5946",
  "GET /api/v1/halls/my-halls": "page=0&size=20",
  "GET /api/v1/hotels/my-hotels": "page=0&size=20",
  "GET /api/v1/amenities": "",
  "GET /api/v1/bookings": "page=0&size=20",
  "GET /api/v1/bookings/owner": "status=&page=0&size=20",
  "GET /api/v1/coupons/available": "lat=12.9716&lng=77.5946&radiusKm=50",
  "GET /api/v1/coupons/offers": "lat=12.9716&lng=77.5946&radiusKm=50",
  "GET /api/v1/notifications": "limit=20&unreadOnly=&before=",
  "GET /api/v1/facilities/compare": "type=hall&ids={{hallId}},{{hallId2}}&lat=12.9716&lng=77.5946",
  "GET /api/v1/venues/compare": "type=hall&ids={{hallId}},{{hallId2}}&lat=12.9716&lng=77.5946",
  "GET /api/v1/users/me/favourites": "",
  "GET /api/v1/vendors/me/properties": "page=0&size=20",
  "GET /api/v1/quotes/my-requests": "status=&page=0&size=20",
  "GET /api/v1/quotes/owner": "status=&page=0&size=20",
  "GET /api/v1/reviews/facility/{facilityId}": "page=0&size=20",
  "GET /api/v1/admin/reviews": "status=PENDING&page=0&size=20",
  "GET /api/v1/admin/users": "search=&page=0&size=20",
  "GET /api/v1/admin/vendors": "status=PENDING&page=0&size=20",
  "GET /api/v1/admin/facilities": "type=&search=&page=0&size=20",
  "GET /api/v1/admin/dashboard": "",
  "GET /api/v1/search/venues": "city=Bengaluru&venueType=HALL&minCapacity=100&maxCapacity=1000&minBudget=100000&maxBudget=500000&amenities=Parking&sort=PRICE_LOW_TO_HIGH&page=0&size=20",
  "GET /api/v1/search/autocomplete": "q=Kum",
  "GET /api/v1/search/suggestions": "q=Beng",
  "GET /api/v1/search/venues/{id}/similar": "limit=10",
  "POST /api/v1/search/venues/{id}/view": "searchId={{searchId}}",
  "GET /api/v1/search/popular-cities": "limit=10",
  "POST /api/v1/refunds/{paymentId}": "amount=50000&reason=Change of plans",
  "POST /api/v1/admin/fraud-reports": "targetType=VENDOR&targetId={{vendorId}}&reason=Suspicious activity pattern",
  "POST /api/v1/admin/fraud-reports/{reportId}/resolve": "status=RESOLVED_CLEARED&note=",
  "POST /api/v1/admin/vendors/{vendorId}/kyc/reject": "reason=Document unreadable",
  "POST /api/v1/admin/facilities/{id}/approve": "status=APPROVED",
  "GET /api/v1/super-admin/help/messages": "search=&status=&page=0&limit=20",
  "GET /api/v1/public/recommendations": "lat=12.9716&lng=77.5946&type=ALL&radiusKm=50",
};

// Which token each folder acts as
const FOLDER_TOKEN = {
  "Health": null,
  "Auth": "accessToken",
  "User Profile": "accessToken",
  "Vendors": "ownerToken",
  "Facilities (unified)": "ownerToken",
  "Compare Venues": null,
  "Recommendations": null,
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
  "Notifications": "accessToken",
  "Coupons": "ownerToken",
  "Support": null,
  "Help Center": "accessToken",
  "Privacy Policy": "accessToken",
  "Feedback": "accessToken",
  "Search": "accessToken",
  "Admin": "adminToken",
  "Cleanup (destructive)": "ownerToken",
};

// Requests that need a different token than folder default
const REQUEST_TOKEN = {
  "POST /api/v1/quotes/{id}/reply": "ownerToken",
  "GET /api/v1/quotes/owner": "ownerToken",
  "GET /api/v1/quotes/owner/stats": "ownerToken",
  "POST /api/v1/refunds/{paymentId}": "ownerToken",
  "GET /api/v1/admin/analytics/decisions": "adminToken",
  "GET /api/v1/bookings/owner": "ownerToken",
  "POST /api/v1/bookings/{id}/status": "ownerToken",
  "PUT /api/v1/bookings/{id}/status": "ownerToken",
  "PATCH /api/v1/bookings/{id}/status": "ownerToken",
  "POST /api/v1/bookings/{id}/confirm": "ownerToken",
  "POST /api/v1/bookings/{id}/reject": "ownerToken",
  "POST /api/v1/admin/coupons": "adminToken",
  "GET /api/v1/admin/coupons": "adminToken",
  "PUT /api/v1/admin/coupons/{id}": "adminToken",
  "DELETE /api/v1/admin/coupons/{id}": "adminToken",
  "GET /api/v1/super-admin/help/messages": "adminToken",
  "GET /api/v1/super-admin/help/messages/{messageId}": "adminToken",
  "DELETE /api/v1/super-admin/help/messages/{messageId}": "adminToken",
  "POST /api/v1/admin/privacy-policy": "adminToken",
  "POST /api/admin/privacy-policy": "adminToken",
  "GET /api/v1/admin/privacy-policy": "adminToken",
  "GET /api/admin/privacy-policy": "adminToken",
  "PUT /api/v1/admin/privacy-policy": "adminToken",
  "PUT /api/admin/privacy-policy": "adminToken",
  "DELETE /api/v1/admin/privacy-policy": "adminToken",
  "DELETE /api/admin/privacy-policy": "adminToken",
  "DELETE /api/v1/admin/users/{id}": "adminToken",
  "DELETE /api/v1/admin/reviews/{id}": "adminToken",
  "DELETE /api/v1/admin/coupons/{id}": "adminToken",
  "DELETE /api/v1/users/me": "accessToken",
  "POST /api/v1/bookings/{id}/cancel": "accessToken",
  "POST /api/v1/auth/logout/all-devices": "accessToken",
  "GET /api/v1/admin/feedback": "adminToken",
  "GET /api/admin/feedback": "adminToken",
  "GET /api/v1/admin/feedback/summary": "adminToken",
  "GET /api/admin/feedback/summary": "adminToken",
  "GET /api/v1/admin/feedback/{id}": "adminToken",
  "GET /api/admin/feedback/{id}": "adminToken",
  "PUT /api/v1/admin/feedback/{id}": "adminToken",
  "PUT /api/admin/feedback/{id}": "adminToken",
};

// Endpoints that are completely public (No Bearer token required)
const NO_AUTH = new Set([
  "POST /api/v1/auth/register",
  "POST /api/v1/auth/register/vendor",
  "POST /api/v1/auth/register/verify-email",
  "POST /api/v1/auth/register/verify-phone",
  "POST /api/v1/auth/login",
  "POST /api/v1/auth/otp/resend",
  "POST /api/v1/auth/refresh",
  "POST /api/v1/auth/login/refresh",
  "POST /api/v1/auth/logout",
  "POST /api/v1/auth/forgot-password",
  "POST /api/v1/auth/reset-password",
  "POST /api/v1/payments/webhook",
  "GET /health",
  "GET /livez",
  "GET /readyz",
  "GET /ack/{token}",
  "GET /decline/{token}",
  "GET /api/v1/coupons/available",
  "GET /api/v1/coupons/offers",
  "GET /api/v1/facilities",
  "GET /api/v1/facilities/compare",
  "POST /api/v1/facilities/compare",
  "GET /api/v1/venues/compare",
  "POST /api/v1/venues/compare",
  "GET /api/v1/events",
  "GET /api/v1/facilities/{id}/events",
  "GET /api/v1/facilities/{id}/faqs",
  "GET /api/v1/amenities",
  "GET /api/v1/venues",
  "GET /api/v1/halls",
  "GET /api/v1/hotels/{id}",
  "GET /api/v1/halls/{id}",
  "GET /api/v1/hotels/{id}/room-types",
  "GET /api/v1/halls/{id}/packages",
  "GET /api/v1/halls/{id}/addons",
  "GET /api/v1/halls/{id}/advance-rules",
  "GET /api/v1/facilities/{id}/policies",
  "GET /api/v1/facilities/{id}/pricing",
  "GET /api/v1/facilities/{id}/cancellation-policies",
  "GET /api/v1/search/venues",
  "GET /api/v1/search/autocomplete",
  "GET /api/v1/search/suggestions",
  "GET /api/v1/search/venues/{id}/similar",
  "POST /api/v1/search/venues/{id}/view",
  "GET /api/v1/search/trending",
  "GET /api/v1/search/popular-cities",
  "GET /api/v1/reviews/facility/{facilityId}",
  "GET /api/v1/reviews/facility/{facilityId}/summary",
  "GET /api/v1/support",
  "GET /api/privacy-policy",
  "GET /api/v1/privacy-policy",
  "GET /api/v1/public/recommendations",
]);

const MODULE_FOLDER = {
  health: "Health",
  auth: "Auth",
  user: "User Profile",
  vendors: "Vendors",
  booking: "Bookings",
  payment: "Payments",
  quote: "Quotes & Negotiation",
  review: "Reviews",
  search: "Search",
  admin: "Admin",
  notification: "Notifications",
  support: "Support",
  coupon: "Coupons",
  feedback: "Feedback",
  help: "Help Center",
  privacypolicy: "Privacy Policy",
  recommendation: "Recommendations",
};

const FOLDER_ORDER = [
  "Health",
  "Auth",
  "User Profile",
  "Vendors",
  "Facilities (unified)",
  "Compare Venues",
  "Recommendations",
  "Marriage Halls",
  "Hotels",
  "Room Types",
  "Hall Packages",
  "Add-on Services",
  "Token Advance Rules",
  "Amenities",
  "Facility Policies",
  "Facility Pricing Rules",
  "Facility Media",
  "Favourites",
  "Bookings",
  "Payments",
  "Refunds",
  "Quotes & Negotiation",
  "Reviews",
  "Notifications",
  "Coupons",
  "Support",
  "Help Center",
  "Feedback",
  "Privacy Policy",
  "Search",
  "Admin",
  "Cleanup (destructive)",
];

const PATH_FOLDER = [
  ["/recommendations", "Recommendations"],
  ["/compare", "Compare Venues"],
  ["/super-admin/help", "Help Center"],
  ["/help/messages", "Help Center"],
  ["/privacy-policy", "Privacy Policy"],
  ["/room-types", "Room Types"],
  ["/packages", "Hall Packages"],
  ["/addons", "Add-on Services"],
  ["/advance-rules", "Token Advance Rules"],
  ["/policies", "Facility Policies"],
  ["/pricing", "Facility Pricing Rules"],
  ["/amenities", "Amenities"],
  ["/images", "Facility Media"],
  ["/videos", "Facility Media"],
  ["/favourites", "Favourites"],
  ["/cancellation-policies", "Facility Policies"],
  ["/api/v1/admin/reviews", "Admin"],
  ["/api/v1/admin/support", "Admin"],
  ["/api/v1/admin/feedback", "Admin"],
  ["/api/admin/feedback", "Admin"],
  ["/api/v1/bookings/quote", "Bookings"],
  ["/acknowledge", "Notifications"],
  ["/api/v1/refunds", "Refunds"],
  ["/api/v1/halls", "Marriage Halls"],
  ["/api/v1/hotels", "Hotels"],
];

const DESTRUCTIVE = new Set([
  "POST /api/v1/bookings/{id}/cancel",
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
  "DELETE /api/v1/super-admin/help/messages/{messageId}",
  "DELETE /api/v1/admin/privacy-policy",
  "DELETE /api/admin/privacy-policy",
]);

const REQUEST_ORDER = [
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
  "POST /api/v1/users/me/favourites",
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
  "POST /api/v1/facilities/compare",
  "GET /api/v1/facilities/compare",
  "POST /api/v1/venues/compare",
  "GET /api/v1/venues/compare",
  "GET /api/v1/venues",
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
  "GET /api/v1/bookings/owner",
  "POST /api/v1/bookings/{id}/status",
  "PUT /api/v1/bookings/{id}/status",
  "PATCH /api/v1/bookings/{id}/status",
  "POST /api/v1/bookings/{id}/confirm",
  "POST /api/v1/bookings/{id}/reject",
  "POST /api/v1/bookings/quote",
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
  "GET /api/v1/reviews/my-reviews",
  "DELETE /api/v1/reviews/{id}",

  "POST /api/v1/help/messages",
  "GET /api/v1/super-admin/help/messages",
  "GET /api/v1/super-admin/help/messages/{messageId}",

  "POST /api/v1/feedback",
  "GET /api/v1/feedback/my-feedback",
  "GET /api/v1/admin/feedback",
  "PUT /api/v1/admin/feedback/{id}",

  "GET /api/v1/notifications",
  "GET /api/v1/notifications/unread-count",
  "PUT /api/v1/notifications/{id}/read",
  "PUT /api/v1/notifications/read-all",

  "GET /api/privacy-policy",
  "GET /api/v1/privacy-policy",
  "POST /api/v1/admin/privacy-policy",
  "GET /api/v1/admin/privacy-policy",
  "PUT /api/v1/admin/privacy-policy",
  "DELETE /api/v1/admin/privacy-policy",

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
  "GET /api/v1/admin/reviews",
  "POST /api/v1/admin/reviews",
  "PUT /api/v1/admin/reviews/{id}",
  "PATCH /api/v1/admin/reviews/{id}/approve",
  "PATCH /api/v1/admin/reviews/{id}/reject",
  "DELETE /api/v1/admin/reviews/{id}",
  "DELETE /api/v1/admin/users/{id}",

  "POST /api/v1/coupons",
  "GET /api/v1/coupons",
  "PUT /api/v1/coupons/{id}",
  "POST /api/v1/coupons/validate",
  "POST /api/v1/admin/coupons",
  "GET /api/v1/admin/coupons",
  "PUT /api/v1/admin/coupons/{id}",
  "GET /api/v1/coupons/available",
  "GET /api/v1/coupons/offers",
  "DELETE /api/v1/admin/coupons/{id}",
  "DELETE /api/v1/coupons/{id}",

  "DELETE /api/v1/facilities/{id}/images/{childId}",
  "DELETE /api/v1/facilities/{id}/videos/{childId}",
  "DELETE /api/v1/facilities/{id}/policies/{childId}",
  "DELETE /api/v1/facilities/{id}/pricing/{childId}",
  "DELETE /api/v1/facilities/{id}/amenities/{amenityId}",
  "DELETE /api/v1/halls/{id}/packages/{childId}",
  "DELETE /api/v1/halls/{id}/addons/{childId}",
  "DELETE /api/v1/hotels/{id}/room-types/{childId}",
  "DELETE /api/v1/super-admin/help/messages/{messageId}",
  "DELETE /api/v1/reviews/{id}",
  "DELETE /api/v1/halls/{id}",
  "DELETE /api/v1/hotels/{id}",
  "DELETE /api/v1/users/me",
  "POST /api/v1/bookings/{id}/cancel",
  "POST /api/v1/auth/logout",
  "POST /api/v1/auth/logout/all-devices",
];

const ORDER_INDEX = new Map(REQUEST_ORDER.map((k, i) => [k, i]));

const NAMES = {
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
  "POST /api/v1/users/me/favourites": "Toggle favourite",
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
  "GET /api/v1/facilities": "List facilities (public)",
  "POST /api/v1/facilities/compare": "Compare 2 venues (POST - hall vs hall)",
  "GET /api/v1/facilities/compare": "Compare 2 venues (GET query)",
  "POST /api/v1/venues/compare": "Compare venues (POST alias)",
  "GET /api/v1/venues/compare": "Compare venues (GET alias)",
  "GET /api/v1/venues": "List venues (public)",
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
  "GET /api/v1/bookings/owner": "Owner: list bookings",
  "POST /api/v1/bookings/{id}/status": "Owner: update booking status (confirm/reject)",
  "PUT /api/v1/bookings/{id}/status": "Owner: update booking status (PUT alias)",
  "PATCH /api/v1/bookings/{id}/status": "Owner: update booking status (PATCH alias)",
  "POST /api/v1/bookings/{id}/confirm": "Owner: confirm booking",
  "POST /api/v1/bookings/{id}/reject": "Owner: reject booking",
  "POST /api/v1/bookings/{id}/cancel": "Cancel booking",

  "POST /api/v1/bookings/quote": "Price preview (no booking)",
  "POST /api/v1/facilities/{id}/cancellation-policies": "Add cancellation tier",
  "GET /api/v1/facilities/{id}/cancellation-policies": "List cancellation tiers",
  "DELETE /api/v1/facilities/{id}/cancellation-policies/{childId}": "Delete cancellation tier",
  "GET /api/v1/admin/reviews": "Admin: list reviews",
  "POST /api/v1/admin/reviews": "Admin: add review",
  "PUT /api/v1/admin/reviews/{id}": "Admin: edit review",
  "PATCH /api/v1/admin/reviews/{id}/approve": "Admin: approve review",
  "PATCH /api/v1/admin/reviews/{id}/reject": "Admin: reject review",
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
  "GET /api/v1/reviews/my-reviews": "My reviews",
  "DELETE /api/v1/reviews/{id}": "Delete review",

  "POST /api/v1/help/messages": "Customer: submit help message",
  "GET /api/v1/super-admin/help/messages": "Super Admin: list support messages",
  "GET /api/v1/super-admin/help/messages/{messageId}": "Super Admin: get support message",
  "DELETE /api/v1/super-admin/help/messages/{messageId}": "Super Admin: delete support message",

  "GET /api/privacy-policy": "Get active privacy policy (public)",
  "GET /api/v1/privacy-policy": "Get active privacy policy (public alias)",
  "POST /api/v1/admin/privacy-policy": "Admin: publish privacy policy",
  "POST /api/admin/privacy-policy": "Admin: publish privacy policy (alias)",
  "GET /api/v1/admin/privacy-policy": "Admin: get privacy policy audit",
  "GET /api/admin/privacy-policy": "Admin: get privacy policy audit (alias)",
  "PUT /api/v1/admin/privacy-policy": "Admin: update privacy policy",
  "PUT /api/admin/privacy-policy": "Admin: update privacy policy (alias)",
  "DELETE /api/v1/admin/privacy-policy": "Admin: deactivate privacy policy",
  "DELETE /api/admin/privacy-policy": "Admin: deactivate privacy policy (alias)",

  "POST /api/v1/feedback": "Submit feedback",
  "POST /api/feedback": "Submit feedback (alias)",
  "GET /api/v1/feedback/my-feedback": "My feedback",
  "GET /api/v1/admin/feedback": "Admin: list feedback",
  "GET /api/admin/feedback": "Admin: list feedback (alias)",
  "GET /api/v1/admin/feedback/summary": "Admin: feedback summary stats",
  "GET /api/admin/feedback/summary": "Admin: feedback summary stats (alias)",
  "GET /api/v1/admin/feedback/{id}": "Admin: get feedback ticket",
  "GET /api/admin/feedback/{id}": "Admin: get feedback ticket (alias)",
  "PUT /api/v1/admin/feedback/{id}": "Admin: update feedback status",
  "PUT /api/admin/feedback/{id}": "Admin: update feedback status (alias)",

  "GET /api/v1/search/venues": "Search venues",
  "GET /api/v1/search/autocomplete": "Autocomplete",
  "GET /api/v1/search/suggestions": "Suggestions",
  "GET /api/v1/search/venues/{id}/similar": "Similar venues",
  "POST /api/v1/search/venues/{id}/view": "Record a view",
  "GET /api/v1/search/recent": "My recent searches",
  "GET /api/v1/search/recently-viewed": "My recently viewed",
  "GET /api/v1/search/trending": "Trending searches",
  "GET /api/v1/search/popular-cities": "Popular cities",
  "DELETE /api/v1/search/recent": "Clear my search history",

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
  "GET /livez": "Liveness probe",
  "GET /readyz": "Readiness probe",
  "GET /ack/{token}": "One-click booking acknowledgement",
  "GET /decline/{token}": "One-click booking decline",
  "GET /api/v1/coupons/offers": "Available coupon offers",
  "GET /api/v1/public/recommendations": "Get recommendations (ALL - Hall + Hotel)",
};

const CAPTURE = {
  "GET /api/v1/notifications": `const d = pm.response.json().data;
if (d && d.items && d.items.length) {
  pm.collectionVariables.set("notificationId", d.items[0].id);
} else {
  pm.collectionVariables.set("notificationId", "00000000-0000-0000-0000-000000000000");
}`,
  "POST /api/v1/facilities/{id}/faqs": `const d = pm.response.json().data;
if (d && d.id) pm.collectionVariables.set("faqId", d.id);`,
  "POST /api/v1/feedback": `const d = pm.response.json().data;
if (d && d.id) pm.collectionVariables.set("feedbackId", d.id);`,
  "POST /api/feedback": `const d = pm.response.json().data;
if (d && d.id) pm.collectionVariables.set("feedbackId", d.id);`,
  "POST /api/v1/coupons": `const d = pm.response.json().data;
if (d && d.id) pm.collectionVariables.set("couponId", d.id);`,
  "POST /api/v1/admin/coupons": `const d = pm.response.json().data;
if (d && d.id) pm.collectionVariables.set("adminCouponId", d.id);`,
  "POST /api/v1/admin/reviews": `const d = pm.response.json().data;
if (d) pm.collectionVariables.set("adminReviewId", d.id);`,
  "GET /api/v1/admin/reviews": `const d = pm.response.json().data;
const list = d && d.content ? d.content : (Array.isArray(d) ? d : []);
if (list.length > 0 && list[0].id) {
  pm.collectionVariables.set("adminReviewId", list[0].id);
}`,
  "POST /api/v1/facilities/{id}/cancellation-policies": `const d = pm.response.json().data;
if (d) pm.collectionVariables.set("cancellationId", d.id);`,
  "POST /api/v1/auth/login": `const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("accessToken", d.accessToken);
  pm.environment.set("accessToken", d.accessToken);
  pm.collectionVariables.set("refreshToken", d.refreshToken);
  pm.environment.set("refreshToken", d.refreshToken);
  pm.collectionVariables.set("userId", d.user.id);
  pm.environment.set("userId", d.user.id);
}`,
  "POST /api/v1/auth/register/verify-email": `const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("accessToken", d.accessToken);
  pm.environment.set("accessToken", d.accessToken);
  pm.collectionVariables.set("refreshToken", d.refreshToken);
  pm.environment.set("refreshToken", d.refreshToken);
  pm.collectionVariables.set("userId", d.user.id);
  pm.environment.set("userId", d.user.id);
  if ((d.user.roles || []).includes("ROLE_HALL_OWNER")) {
    pm.collectionVariables.set("ownerToken", d.accessToken);
    pm.environment.set("ownerToken", d.accessToken);
  }
  if ((d.user.roles || []).includes("ROLE_ADMIN") || (d.user.roles || []).includes("ROLE_SUPER_ADMIN")) {
    pm.collectionVariables.set("adminToken", d.accessToken);
    pm.environment.set("adminToken", d.accessToken);
  }
}`,
  "POST /api/v1/auth/refresh": `const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("accessToken", d.accessToken);
  pm.collectionVariables.set("refreshToken", d.refreshToken);
  pm.environment.set("accessToken", d.accessToken);
  pm.environment.set("refreshToken", d.refreshToken);
}`,
  "POST /api/v1/auth/login/refresh": `const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("accessToken", d.accessToken);
  pm.collectionVariables.set("refreshToken", d.refreshToken);
  pm.environment.set("accessToken", d.accessToken);
  pm.environment.set("refreshToken", d.refreshToken);
}`,
  "POST /api/v1/facilities": `const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("facilityId", d.id);
  if (d.type === "HALL" || d.type === "MARRIAGE_HALL") {
    if (!pm.collectionVariables.get("hallId")) {
      pm.collectionVariables.set("hallId", d.id);
    } else {
      pm.collectionVariables.set("hallId2", d.id);
    }
  } else {
    pm.collectionVariables.set("hotelId", d.id);
  }
}`,
  "POST /api/v1/halls/{id}/packages": `const d = pm.response.json().data;
if (d) pm.collectionVariables.set("packageId", d.id);`,
  "POST /api/v1/halls/{id}/addons": `const d = pm.response.json().data;
if (d) pm.collectionVariables.set("addonId", d.id);`,
  "POST /api/v1/hotels/{id}/room-types": `const d = pm.response.json().data;
if (d) pm.collectionVariables.set("roomTypeId", d.id);`,
  "POST /api/v1/facilities/{id}/images": `const d = pm.response.json().data;
if (d) pm.collectionVariables.set("imageId", d.id);`,
  "POST /api/v1/facilities/{id}/videos": `const d = pm.response.json().data;
if (d) pm.collectionVariables.set("videoId", d.id);`,
  "POST /api/v1/bookings/halls": `const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("bookingId", d.id);
  pm.environment.set("bookingId", d.id);
  pm.collectionVariables.set("hallBookingId", d.id);
  pm.environment.set("hallBookingId", d.id);
}`,
  "POST /api/v1/bookings/hotels": `const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("hotelBookingId", d.id);
  pm.environment.set("hotelBookingId", d.id);
}`,
  "POST /api/v1/facilities/compare": `if (pm.response.code === 200) {
    pm.test("Comparison contains venues", function () {
        const b = pm.response.json();
        pm.expect(b.success).to.be.true;
        pm.expect(b.data).to.have.property("venues");
        pm.expect(b.data.venues.length).to.be.at.least(2);
    });
}`,
  "POST /api/v1/venues/compare": `if (pm.response.code === 200) {
    pm.test("Comparison contains venues", function () {
        const b = pm.response.json();
        pm.expect(b.success).to.be.true;
        pm.expect(b.data).to.have.property("venues");
        pm.expect(b.data.venues.length).to.be.at.least(2);
    });
}`,
  "GET /api/v1/facilities/compare": `if (pm.response.code === 200) {
    pm.test("Comparison contains venues", function () {
        const b = pm.response.json();
        pm.expect(b.success).to.be.true;
        pm.expect(b.data).to.have.property("venues");
        pm.expect(b.data.venues.length).to.be.at.least(2);
    });
}`,
  "GET /api/v1/venues/compare": `if (pm.response.code === 200) {
    pm.test("Comparison contains venues", function () {
        const b = pm.response.json();
        pm.expect(b.success).to.be.true;
        pm.expect(b.data).to.have.property("venues");
        pm.expect(b.data.venues.length).to.be.at.least(2);
    });
}`,
  "POST /api/v1/payments/create": `const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("paymentId", d.id);
  pm.collectionVariables.set("gatewayOrderId", d.gatewayOrderId);
}`,
  "POST /api/v1/quotes/request": `const d = pm.response.json().data;
if (d) pm.collectionVariables.set("quoteId", d.id);`,
  "GET /api/v1/search/venues": `const d = pm.response.json().data;
if (d && d.searchId) pm.collectionVariables.set("searchId", d.searchId);`,
  "GET /api/v1/vendors/me": `const d = pm.response.json().data;
if (d) pm.collectionVariables.set("vendorId", d.id);`,
  "GET /api/v1/users/me": `const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("userId", d.id);
  pm.environment.set("userId", d.id);
}`,
  "GET /api/v1/amenities": `const d = pm.response.json().data;
if (d && d.length) pm.collectionVariables.set("amenityId", d[0].id);`,
  "GET /api/v1/admin/vendors": `const d = pm.response.json().data;
if (d && d.content && d.content.length) pm.collectionVariables.set("vendorId", d.content[0].id);`,
  "GET /api/v1/reviews/facility/{facilityId}": `const d = pm.response.json().data;
if (d && d.content && d.content.length) pm.collectionVariables.set("reviewId", d.content[0].id);`,
  "GET /api/v1/facilities/{id}/policies": `const d = pm.response.json().data;
if (d && d.length) pm.collectionVariables.set("policyId", d[0].id);`,
  "GET /api/v1/facilities/{id}/pricing": `const d = pm.response.json().data;
if (d && d.length) pm.collectionVariables.set("ruleId", d[0].id);`,
  "POST /api/v1/admin/fraud-reports": `const d = pm.response.json().data;
if (d) pm.collectionVariables.set("reportId", d.id);`,
  "POST /api/v1/help/messages": `const d = pm.response.json().data;
if (d && d.id) pm.collectionVariables.set("messageId", d.id);`,
  "GET /api/v1/super-admin/help/messages": `const d = pm.response.json().data;
const list = d && d.messages ? d.messages : (d && d.content ? d.content : (Array.isArray(d) ? d : []));
if (list.length > 0 && list[0].id) {
  pm.collectionVariables.set("messageId", list[0].id);
}`,
};

const COMMON_TEST = `if (pm.response.code === 201) {
    pm.test("201 carries a Location header", function () {
        pm.expect(pm.response.headers.get("Location")).to.be.a("string").and.not.empty;
    });
}
const ct = pm.response.headers.get("Content-Type") || "";
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
}`;

function toPostmanPath(p) {
  return p
    .replace(/^\/+|\/+$/g, "")
    .split("/")
    .map((s) => (s.startsWith("{") ? ":" + s.slice(1, -1) : s));
}

function pathVar(p, name) {
  if (name === "id") {
    if (p.startsWith("/api/v1/admin/reviews")) return "adminReviewId";
    if (p.startsWith("/api/v1/admin/coupons")) return "adminCouponId";
    if (p.startsWith("/api/v1/coupons")) return "couponId";
    if (p.startsWith("/api/v1/halls") || p.includes("/halls/")) return "hallId";
    if (p.startsWith("/api/v1/hotels") || p.includes("/hotels/")) return "hotelId";
    if (p.startsWith("/api/v1/quotes")) return "quoteId";
    if (p.startsWith("/api/v1/bookings")) {
      if (p.includes("/reject")) return "hotelBookingId";
      return "bookingId";
    }
    if (p.startsWith("/api/v1/reviews")) return "reviewId";
    if (p.startsWith("/api/v1/admin/users")) return "userId";
    if (p.startsWith("/api/v1/admin/facilities") || p.startsWith("/api/v1/facilities")) return "facilityId";
    if (p.startsWith("/api/v1/search")) return "hallId";
    if (p.startsWith("/api/v1/admin/feedback") || p.startsWith("/api/admin/feedback")) return "feedbackId";
    if (p.startsWith("/api/v1/notifications")) return "notificationId";
    return "facilityId";
  }
  if (name === "facilityId" && p.includes("/favourites")) return "hallId";
  if (name === "childId" && p.includes("/faqs")) return "faqId";
  if (name === "childId" && p.includes("/cancellation-policies")) return "cancellationId";
  if (name === "childId") {
    for (const [seg, v] of [
      ["/images", "imageId"],
      ["/videos", "videoId"],
      ["/policies", "policyId"],
      ["/pricing", "ruleId"],
      ["/room-types", "roomTypeId"],
      ["/packages", "packageId"],
      ["/addons", "addonId"],
    ]) {
      if (p.includes(seg)) return v;
    }
    return "childId";
  }
  if (name === "messageId") return "messageId";
  return name;
}

function requestName(method, p) {
  const key = `${method} ${p}`;
  if (NAMES[key]) return NAMES[key];
  const parts = p
    .replace(/^\/+|\/+$/g, "")
    .split("/")
    .filter((x) => !["api", "v1"].includes(x));
  const words = parts.filter((x) => !x.startsWith("{"));
  return `${method} ` + words.map((w) => w.replace(/-/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())).join(" ");
}

function describe(method, p, folder) {
  const resource = folder.replace(/s$/i, "").toLowerCase();
  if (method === "GET") {
    const what = p.includes("{") ? "Fetches" : "Lists";
    return `${what} ${resource} data. See response for shape.`;
  }
  if (method === "POST") return `Creates or acts on a ${resource} record.`;
  if (method === "PUT") return `Updates ${resource}. Send full object.`;
  if (method === "PATCH") return `Partially updates ${resource}. Omitted fields remain untouched.`;
  if (method === "DELETE") return `Deletes ${resource} record.`;
  return "";
}

function buildRequest(folder, method, p) {
  const key = `${method} ${p}`;
  const segs = toPostmanPath(p);
  const query = QUERIES[key] || "";
  let raw = "{{baseUrl}}/" + segs.join("/");
  if (query) raw += "?" + query;

  const url = { raw, host: ["{{baseUrl}}"], path: segs };
  if (query) {
    url.query = query.split("&").map((param) => {
      const idx = param.indexOf("=");
      return idx >= 0
        ? { key: param.slice(0, idx), value: param.slice(idx + 1) }
        : { key: param, value: "" };
    });
  }

  const variables = segs
    .filter((s) => s.startsWith(":"))
    .map((s) => ({
      key: s.slice(1),
      value: `{{${pathVar(p, s.slice(1))}}}`,
    }));
  if (variables.length > 0) url.variable = variables;

  const headers = [];
  const body = BODIES[key];
  if (body !== undefined) {
    headers.push({ key: "Content-Type", value: "application/json" });
  }
  if (key === "POST /api/v1/payments/webhook") {
    headers.push({
      key: "X-Signature",
      value: "{{webhookSignature}}",
      description: "HMAC-SHA256 of raw body, keyed with PAYMENT_WEBHOOK_SECRET",
    });
  }

  const doc = describe(method, p, folder);
  const req = { method, header: headers, url, description: doc };

  if (NO_AUTH.has(key)) {
    req.auth = { type: "noauth" };
  } else {
    const token = REQUEST_TOKEN[key] || FOLDER_TOKEN[folder] || "accessToken";
    if (token) {
      req.auth = {
        type: "bearer",
        bearer: [{ key: "token", value: `{{${token}}}`, type: "string" }],
      };
    } else {
      req.auth = { type: "noauth" };
    }
  }

  if (body !== undefined) {
    req.body = {
      mode: "raw",
      raw: JSON.stringify(body, null, 2),
      options: { raw: { language: "json" } },
    };
  }

  return req;
}

function folderFor(moduleName, method, p) {
  for (const [needle, name] of PATH_FOLDER) {
    if (p.includes(needle)) return name;
  }
  if ((p.endsWith("/block") || p.endsWith("/unblock")) && !p.includes("/admin/")) {
    return "Facility Media";
  }
  const folder = MODULE_FOLDER[moduleName];
  return folder || "Facilities (unified)";
}

function collectRoutes() {
  const pat = /(?:mux\.(?:HandleFunc|Handle)\("|get\(")(GET|POST|PUT|PATCH|DELETE) ([^"]+)"/g;
  const seen = new Set();
  const routes = [];
  const skipRoutes = new Set([
    "GET /uploads/",
    // Same handler as POST /api/v1/auth/refresh under the old spec path. Still
    // served, but a second refresh in the run only adds a chance to replay a
    // rotated token, which revokes every session.
    "POST /api/v1/auth/login/refresh",
  ]);

  function walk(dir) {
    for (const f of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, f.name);
      if (f.isDirectory()) {
        walk(full);
      } else if (f.name.endsWith(".go") && !f.name.endsWith("_test.go")) {
        const rel = path.relative(ROOT, full);
        const parts = rel.split(path.sep);
        const moduleName = parts.length > 1 ? parts[1] : "";
        const src = fs.readFileSync(full, "utf8");
        let m;
        while ((m = pat.exec(src)) !== null) {
          const method = m[1];
          const p = m[2];
          const key = `${method} ${p}`;
          if (seen.has(key) || skipRoutes.has(key)) continue;
          seen.add(key);

          if (DESTRUCTIVE.has(key)) {
            routes.push(["Cleanup (destructive)", method, p]);
          } else {
            const folder = folderFor(moduleName, method, p);
            routes.push([folder, method, p]);
          }
        }
      }
    }
  }

  walk(path.join(ROOT, "internal"));
  return routes;
}

function main() {
  const routes = collectRoutes();
  const folders = {};
  for (const [folder, method, p] of routes) {
    if (!folders[folder]) folders[folder] = [];
    folders[folder].push([method, p]);
  }

  const items = [];
  for (const folder of FOLDER_ORDER) {
    if (!folders[folder]) continue;

    const deletesLast = folder !== "Cleanup (destructive)";
    const entries = folders[folder].slice().sort((a, b) => {
      const aDel = deletesLast && a[0] === "DELETE" ? 1 : 0;
      const bDel = deletesLast && b[0] === "DELETE" ? 1 : 0;
      if (aDel !== bDel) return aDel - bDel;

      const aOrder = ORDER_INDEX.get(`${a[0]} ${a[1]}`) ?? 10000;
      const bOrder = ORDER_INDEX.get(`${b[0]} ${b[1]}`) ?? 10000;
      if (aOrder !== bOrder) return aOrder - bOrder;

      if (a[1] !== b[1]) return a[1].localeCompare(b[1]);
      return a[0].localeCompare(b[0]);
    });

    const sub = [];
    for (const [method, p] of entries) {
      const key = `${method} ${p}`;
      const item = {
        name: requestName(method, p),
        request: buildRequest(folder, method, p),
        response: [],
      };

      let script = COMMON_TEST;
      if (CAPTURE[key]) {
        script = CAPTURE[key] + "\n\n" + COMMON_TEST;
      }
      item.event = [
        {
          listen: "test",
          script: { type: "text/javascript", exec: script.split("\n") },
        },
      ];
      sub.push(item);
    }


    if (folder === "Auth") {
      const adminLogin = {
        name: "Login as Admin (pre-seeded)",
        request: {
          method: "POST",
          header: [{ key: "Content-Type", value: "application/json" }],
          url: {
            raw: "{{baseUrl}}/api/v1/auth/login",
            host: ["{{baseUrl}}"],
            path: ["api", "v1", "auth", "login"]
          },
          description: "Logs in with the seeded development admin credentials (admin@example.com / Admin@123). Stores adminToken automatically.",
          auth: { type: "noauth" },
          body: {
            mode: "raw",
            raw: JSON.stringify({ identifier: "admin@example.com", password: "Admin@123" }, null, 2),
            options: { raw: { language: "json" } }
          }
        },
        response: [],
        event: [
          {
            listen: "test",
            script: {
              type: "text/javascript",
              exec: [
                "const d = pm.response.json().data;",
                "if (d) {",
                "  pm.collectionVariables.set('adminToken', d.accessToken);",
                "  pm.environment.set('adminToken', d.accessToken);",
                "  console.log('adminToken saved successfully!');",
                "}",
                "",
                ...COMMON_TEST.split("\n")
              ]
            }
          }
        ]
      };
      const at = sub.findIndex(x => x.name.toLowerCase() === "login");
      sub.splice(at >= 0 ? at + 1 : sub.length, 0, adminLogin);
    }


    if (folder === "Recommendations") {
      sub.length = 0; // Replace with complete curated test suite matching specs
      const testCases = [
        {
          name: "Test 1 - Recommendations (ALL: Hall + Hotel)",
          query: "lat=12.9716&lng=77.5946&type=ALL&radiusKm=50",
          desc: "Returns both Hall and Hotel recommendations within 50 km sorted by rating DESC, then distance ASC.",
          expectedStatus: 200,
        },
        {
          name: "Test 2 - Recommendations (HALL only)",
          query: "lat=12.9716&lng=77.5946&type=HALL&radiusKm=50",
          desc: "Returns only Hall recommendations within 50 km.",
          expectedStatus: 200,
        },
        {
          name: "Test 3 - Recommendations (HOTEL only)",
          query: "lat=12.9716&lng=77.5946&type=HOTEL&radiusKm=50",
          desc: "Returns only Hotel recommendations within 50 km.",
          expectedStatus: 200,
        },
        {
          name: "Test 4 - Radius Filter (5 km)",
          query: "lat=12.9716&lng=77.5946&type=ALL&radiusKm=5",
          desc: "Only returns venues located within 5 km from user coordinates.",
          expectedStatus: 200,
        },
        {
          name: "Test 5 - Pagination (page=1, size=20)",
          query: "lat=12.9716&lng=77.5946&type=ALL&radiusKm=50&page=1&size=20",
          desc: "Verifies standard pagination parameters.",
          expectedStatus: 200,
        },
        {
          name: "Test 6 - Validation Error: Invalid Type",
          query: "lat=12.9716&lng=77.5946&type=RESTAURANT&radiusKm=50",
          desc: "Rejects invalid venue types with 400 Bad Request and validation error.",
          expectedStatus: 400,
        },
        {
          name: "Test 7 - Validation Error: Invalid Coordinates",
          query: "lat=999&lng=999&type=ALL",
          desc: "Rejects invalid latitude/longitude with 400 Bad Request and validation error.",
          expectedStatus: 400,
        },
        {
          name: "Test 8 - Public Access (No Token)",
          query: "lat=12.9716&lng=77.5946&type=ALL",
          desc: "Public access test without sending any Authorization header.",
          expectedStatus: 200,
        },
        {
          name: "Test 9 - Event Type Filter (eventType=WEDDING)",
          query: "lat=12.9716&lng=77.5946&type=ALL&radiusKm=50&eventType=WEDDING",
          desc: "Filters recommendations for WEDDING event type (or venues without configured events).",
          expectedStatus: 200,
        },
      ];

      for (const tc of testCases) {
        const segs = ["api", "v1", "public", "recommendations"];
        const queryParams = tc.query.split("&").map(p => {
          const idx = p.indexOf("=");
          return idx >= 0 ? { key: p.slice(0, idx), value: p.slice(idx + 1) } : { key: p, value: "" };
        });
        const req = {
          method: "GET",
          header: [],
          url: {
            raw: "{{baseUrl}}/api/v1/public/recommendations?" + tc.query,
            host: ["{{baseUrl}}"],
            path: segs,
            query: queryParams,
          },
          description: tc.desc,
          auth: { type: "noauth" }
        };

        let testScript = "";
        if (tc.expectedStatus === 400) {
          testScript = [
            `pm.test("Status code is 400", function () {`,
            `    pm.response.to.have.status(400);`,
            `});`,
            `pm.test("returns error envelope", function () {`,
            `    const b = pm.response.json();`,
            `    pm.expect(b.success).to.be.false;`,
            `    pm.expect(b.errorCode).to.eql("VALIDATION_ERROR");`,
            `});`
          ].join("\n");
        } else if (tc.name.includes("ALL")) {
          testScript = [
            `pm.test("Status code is 200", function () {`,
            `    pm.response.to.have.status(200);`,
            `});`,
            `pm.test("returns recommendations list with venue format", function () {`,
            `    const b = pm.response.json();`,
            `    pm.expect(b.success).to.be.true;`,
            `    pm.expect(b.data).to.have.property("content");`,
            `    pm.expect(b.data.content).to.be.an("array");`,
            `    if (b.data.content.length > 0) {`,
            `        const v = b.data.content[0];`,
            `        pm.expect(v).to.have.property("id");`,
            `        pm.expect(v).to.have.property("name");`,
            `        pm.expect(v).to.have.property("type");`,
            `        pm.expect(v).to.have.property("distanceKm");`,
            `    }`,
            `});`,
            COMMON_TEST
          ].join("\n");
        } else if (tc.name.includes("HALL only")) {
          testScript = [
            `pm.test("Status code is 200", function () {`,
            `    pm.response.to.have.status(200);`,
            `});`,
            `pm.test("all items are HALL", function () {`,
            `    const b = pm.response.json();`,
            `    pm.expect(b.success).to.be.true;`,
            `    pm.expect(b.data.content.every(x => x.type === "HALL")).to.be.true;`,
            `});`,
            COMMON_TEST
          ].join("\n");
        } else if (tc.name.includes("HOTEL only")) {
          testScript = [
            `pm.test("Status code is 200", function () {`,
            `    pm.response.to.have.status(200);`,
            `});`,
            `pm.test("all items are HOTEL", function () {`,
            `    const b = pm.response.json();`,
            `    pm.expect(b.success).to.be.true;`,
            `    pm.expect(b.data.content.every(x => x.type === "HOTEL")).to.be.true;`,
            `});`,
            COMMON_TEST
          ].join("\n");
        } else if (tc.name.includes("Radius Filter")) {
          testScript = [
            `pm.test("Status code is 200", function () {`,
            `    pm.response.to.have.status(200);`,
            `});`,
            `pm.test("all items are within 5 km", function () {`,
            `    const b = pm.response.json();`,
            `    pm.expect(b.success).to.be.true;`,
            `    pm.expect(b.data.content.every(x => x.distanceKm <= 5)).to.be.true;`,
            `});`,
            COMMON_TEST
          ].join("\n");
        } else if (tc.name.includes("Pagination")) {
          testScript = [
            `pm.test("Status code is 200", function () {`,
            `    pm.response.to.have.status(200);`,
            `});`,
            `pm.test("pagination metadata is correct", function () {`,
            `    const b = pm.response.json();`,
            `    pm.expect(b.success).to.be.true;`,
            `    pm.expect(b.data.page).to.eql(1);`,
            `    pm.expect(b.data.size).to.eql(20);`,
            `});`,
            COMMON_TEST
          ].join("\n");
        } else if (tc.name.includes("Event Type Filter")) {
          testScript = [
            `pm.test("Status code is 200", function () {`,
            `    pm.response.to.have.status(200);`,
            `});`,
            `pm.test("returns recommendations matching event type filter", function () {`,
            `    const b = pm.response.json();`,
            `    pm.expect(b.success).to.be.true;`,
            `    pm.expect(b.data).to.have.property("content");`,
            `    pm.expect(b.data.content).to.be.an("array");`,
            `});`,
            COMMON_TEST
          ].join("\n");
        } else {
          testScript = COMMON_TEST;
        }

        sub.push({
          name: tc.name,
          request: req,
          response: [],
          event: [{
            listen: "test",
            script: { type: "text/javascript", exec: testScript.split("\n") }
          }]
        });
      }
    }

    // Add second hall and hotel in Facilities for realistic comparison & hotel testing
    if (folder === "Facilities (unified)") {
      const hotel = {
        name: "Create hotel (Grand Residency)",
        request: buildRequest(folder, "POST", "/api/v1/facilities"),
        response: [],
        event: [
          {
            listen: "test",
            script: {
              type: "text/javascript",
              exec: (CAPTURE["POST /api/v1/facilities"] + "\n\n" + COMMON_TEST).split("\n"),
            },
          },
        ],
      };
      hotel.request.body.raw = JSON.stringify(BODIES["POST /api/v1/facilities#hotel"], null, 2);
      hotel.request.description =
        "Creates a HOTEL facility so hotelId is captured for hotel booking and room types testing.";
      sub.splice(1, 0, hotel);

      const captureHall2 = `const d = pm.response.json().data;
if (d) {
  pm.collectionVariables.set("facilityId", d.id);
  pm.collectionVariables.set("hallId2", d.id);
  pm.environment.set("hallId2", d.id);
  console.log("hallId2 captured:", d.id);
}`;
      const hall2 = {
        name: "Create second hall (Kumar Royal Garden - for compare)",
        request: buildRequest(folder, "POST", "/api/v1/facilities"),
        response: [],
        event: [
          {
            listen: "test",
            script: {
              type: "text/javascript",
              exec: (captureHall2 + "\n\n" + COMMON_TEST).split("\n"),
            },
          },
        ],
      };
      hall2.request.body.raw = JSON.stringify(BODIES["POST /api/v1/facilities#hall2"], null, 2);
      hall2.request.description =
        "Creates a second HALL facility so hallId2 is captured for side-by-side venue comparison.";
      sub.splice(2, 0, hall2);

      // Event filtering test requests
      const venueEventTests = [
        {
          name: "List venues - filter by eventType=WEDDING & type=HOTEL",
          query: "type=HOTEL&eventType=WEDDING&search=&city=&page=0&size=20",
          desc: "Filters hotels by WEDDING. Returns hotels supporting WEDDING plus hotels with no configured events.",
        },
        {
          name: "List venues - filter by eventType=WEDDING & type=HALL",
          query: "type=HALL&eventType=WEDDING&search=&city=&page=0&size=20",
          desc: "Filters halls by WEDDING. Returns halls supporting WEDDING plus halls with no configured events.",
        },
        {
          name: "List venues - filter by eventType=BIRTHDAY",
          query: "eventType=BIRTHDAY&page=0&size=20",
          desc: "Filters all venues by BIRTHDAY. Venues configured exclusively for other events are excluded.",
        },
      ];
      for (const vet of venueEventTests) {
        const segs = ["api", "v1", "venues"];
        const queryParams = vet.query.split("&").map((p) => {
          const idx = p.indexOf("=");
          return idx >= 0 ? { key: p.slice(0, idx), value: p.slice(idx + 1) } : { key: p, value: "" };
        });
        sub.push({
          name: vet.name,
          request: {
            method: "GET",
            header: [],
            url: {
              raw: "{{baseUrl}}/api/v1/venues?" + vet.query,
              host: ["{{baseUrl}}"],
              path: segs,
              query: queryParams,
            },
            description: vet.desc,
            auth: { type: "noauth" },
          },
          response: [],
          event: [
            {
              listen: "test",
              script: { type: "text/javascript", exec: COMMON_TEST.split("\n") },
            },
          ],
        });
      }
    }

    // Add multipart media uploads
    if (folder === "Facility Media") {
      for (const [suffix, field, name, desc] of [
        [
          "image",
          "image",
          "Upload image (multipart file)",
          "Uploads image file directly to server storage / S3.",
        ],
        [
          "video",
          "video",
          "Upload video (multipart file)",
          "Uploads video file directly to server storage / S3.",
        ],
      ]) {
        const p = `/api/v1/facilities/{id}/${suffix}s`;
        const v = {
          name,
          request: buildRequest(folder, "POST", p),
          response: [],
          event: [
            {
              listen: "test",
              script: { type: "text/javascript", exec: COMMON_TEST.split("\n") },
            },
          ],
        };
        v.request.body = {
          mode: "formdata",
          formdata: [
            {
              key: field,
              type: "file",
              src: null,
              description: "Select file from disk",
            },
          ],
        };
        v.request.header = (v.request.header || []).filter((h) => h.key !== "Content-Type");
        v.request.description = desc;
        const at = sub.findIndex((x) => x.name.toLowerCase().includes("add " + suffix));
        sub.splice(at >= 0 ? at + 1 : sub.length, 0, v);
      }
    }

    // Add unblock user and block facility in Admin
    if (folder === "Admin") {
      const at = sub.findIndex((x) => x.name.toLowerCase().includes("block user"));
      if (at >= 0) {
        for (const [offset, suffix, name, desc] of [
          [
            1,
            "#unblock",
            "Unblock user (reversal toggle)",
            "Toggles block status back to unblocked so account can log in again.",
          ],
          [
            2,
            "#facility",
            "Block facility listing",
            "Hides venue listing from public search results.",
          ],
        ]) {
          const v = {
            name,
            request: buildRequest(folder, "POST", "/api/v1/admin/block"),
            response: [],
            event: [
              {
                listen: "test",
                script: { type: "text/javascript", exec: COMMON_TEST.split("\n") },
              },
            ],
          };
          v.request.body.raw = JSON.stringify(BODIES["POST /api/v1/admin/block" + suffix], null, 2);
          v.request.description = desc;
          sub.splice(at + offset, 0, v);
        }
      }
    }

const FOLDER_DESC = {
  Health: "Liveness check. No auth, no setup - run it first to confirm the API is up.",
  Auth: "User authentication, registration, OTP verification, and JWT session handling.",
  "User Profile": "Customer personal profile, favourites, and booking activity dashboard.",
  Vendors: "Vendor onboarding, business details, KYC verification, and properties management.",
  "Facilities (unified)": "Unified venue listings (marriage halls and hotels) creation, browsing, and FAQs.",
  "Compare Venues": "Side-by-side venue comparison (2-3 halls or hotels) with capacity, pricing, and amenities matrix.",
  Recommendations: "Location-based recommendation engine for venues ranking by proximity, rating, and filters.",
  "Marriage Halls": "Hall-specific listing details, capacities, and vendor management.",
  Hotels: "Hotel-specific listing details and vendor properties.",
  "Room Types": "Bookable room inventory, pricing per night, and capacity tiers for hotels.",
  "Hall Packages": "Priced packages (catering, decor) attached to marriage halls.",
  "Add-on Services": "Individual add-on services and amenities for halls.",
  "Token Advance Rules": "Token advance and deposit payment rules to hold dates.",
  Amenities: "Venue amenities catalogue, attachment, and detachment.",
  "Facility Policies": "Cancellation policies and refund percentage tiers.",
  "Facility Pricing Rules": "Seasonal and peak date-range price overrides.",
  "Facility Media": "Photo and video galleries, cover image setting, and media upload.",
  Favourites: "Customer saved wishlist of favorite halls and hotels.",
  Bookings: "Hall and hotel reservations, quotes, hold windows, and owner decision workflow.",
  Payments: "Payment creation, checkout sessions, and webhook processing.",
  Refunds: "Payment refunds according to cancellation policy tiers.",
  "Quotes & Negotiation": "Interactive price quote negotiation, counters, messages, and booking conversion.",
  Reviews: "Customer verified reviews, ratings distribution, and summaries.",
  Notifications: "In-app notification feed, read state tracking, and push device registration.",
  Coupons: "Discount coupons, validation, and geo-targeted promo cards.",
  Support: "Public platform support contact channels.",
  "Help Center": "Customer support messages, inquiry submission, and admin message handling.",
  Feedback: "Customer app feedback, ratings, and issue reporting.",
  "Privacy Policy": "Public privacy policy reading and admin version publishing.",
  Search: "Advanced multi-criteria search, autocomplete, trending searches, and geolocation.",
  Admin: "Platform administration, KYC approvals, user moderation, review approval, and analytics.",
  "Cleanup (destructive)": "Deletes and cancellations kept last to maintain test isolation and idempotency.",
};

    const folderItem = {
      name: `${folder} (${sub.length})`,
      item: sub,
    };
    if (FOLDER_DESC[folder]) {
      folderItem.description = FOLDER_DESC[folder];
    }
    items.push(folderItem);
  }

  const total = items.reduce((acc, f) => acc + f.item.length, 0);

  const collection = {
    info: {
      name: "Marriage Hall & Hotel Booking Platform API",
      description: `Comprehensive API testing collection (${total} endpoints) for Marriage Hall & Hotel Booking.\n\nIncludes complete coverage for Authentication, Public Catalog, Side-by-side Compare, Bookings, Payments, Quotes, Reviews, Help Center, Admin Moderation, and Push Notifications.`,
      schema: "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
    },
    event: [
      {
        listen: "prerequest",
        script: {
          type: "text/javascript",
          exec: [
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
            "  pm.collectionVariables.set('customerPhone', `+91${id}0`);",
            "  pm.collectionVariables.set('ownerPhone',    `+91${id}1`);",
            "  console.log('Test identity initialized:', pm.collectionVariables.get('customerEmail'));",
            "}",
          ],
        },
      },
    ],
    item: items,
    variable: [
      { key: "baseUrl", value: "http://localhost:8080" },
      { key: "runId", value: "", description: "Dynamic test execution run ID" },
      { key: "customerEmail", value: "" },
      { key: "customerPhone", value: "" },
      { key: "ownerEmail", value: "" },
      { key: "ownerPhone", value: "" },
      { key: "accessToken", value: "", description: "Customer JWT access token" },
      { key: "refreshToken", value: "", description: "JWT refresh token" },
      { key: "ownerToken", value: "", description: "Venue Owner JWT access token" },
      { key: "adminToken", value: "", description: "Platform Admin / Super Admin JWT access token" },
      { key: "otp", value: "000000", description: "Default dev OTP code (or read from logs)" },
      { key: "resetToken", value: "" },
      { key: "userId", value: "" },
      { key: "facilityId", value: "" },
      { key: "hallId", value: "", description: "First Marriage Hall UUID" },
      { key: "hallId2", value: "", description: "Second Marriage Hall UUID for comparison" },
      { key: "hotelId", value: "", description: "Hotel UUID" },
      { key: "roomTypeId", value: "" },
      { key: "packageId", value: "" },
      { key: "addonId", value: "" },
      { key: "imageId", value: "" },
      { key: "imageId2", value: "" },
      { key: "videoId", value: "" },
      { key: "videoId2", value: "" },
      { key: "bookingId", value: "" },
      { key: "hallBookingId", value: "", description: "Captured from POST /bookings/halls" },
      { key: "hotelBookingId", value: "", description: "Captured from POST /bookings/hotels" },
      { key: "faqId", value: "" },
      { key: "feedbackId", value: "" },
      { key: "messageId", value: "", description: "Help Center message UUID" },
      { key: "couponId", value: "00000000-0000-0000-0000-000000000000" },
      { key: "adminCouponId", value: "00000000-0000-0000-0000-000000000000" },
      { key: "notificationId", value: "" },
      { key: "paymentId", value: "" },
      { key: "gatewayOrderId", value: "" },
      { key: "quoteId", value: "" },
      { key: "vendorId", value: "" },
      { key: "reportId", value: "" },
      { key: "cancellationId", value: "" },
      { key: "adminReviewId", value: "" },
      { key: "searchId", value: "" },
      { key: "amenityId", value: "" },
      { key: "policyId", value: "" },
      { key: "ruleId", value: "" },
      { key: "childId", value: "" },
      { key: "id", value: "" },
      { key: "webhookSignature", value: "" },
    ],
  };

  const outPath = path.join(ROOT, "postman_collection.json");
  fs.writeFileSync(outPath, JSON.stringify(collection, null, 2) + "\n", "utf8");
  console.error(`Generated ${total} requests in ${items.length} folders -> ${outPath}`);
}

main();
