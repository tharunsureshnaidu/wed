# Feedback API Endpoints & Testing Guide

## Overview
The **App Rating & Feedback** feature introduces a set of secure REST endpoints for collecting user feedback while ensuring that:
- Only authenticated users can submit feedback.
- Each user can submit **only one** rating/feedback record.
- Only admin users can view or manage feedback.

---

## Endpoints Summary

| Method | Path | Description | Access Role | Request Body | Response |
|--------|------|-------------|-------------|--------------|----------|
| `POST` | `/api/v1/feedback` | Submit rating & optional feedback (1 per user) | User (Authenticated) | `{"rating": 5, "feedback": "Great app!"}` | `201 Created` |
| `GET` | `/api/v1/feedback/mine` | List feedback submitted by the current user | User (Authenticated) | None | `200 OK` |
| `GET` | `/api/v1/feedback` | List all feedback (with filters) | Admin | Query params: `rating`, `status`, `page`, `size` | `200 OK` |
| `GET` | `/api/v1/feedback/{id}` | Get single feedback details | Admin | None | `200 OK` |
| `PATCH` | `/api/v1/feedback/{id}/status` | Update feedback status & admin note | Admin | `{"status": "RESOLVED", "admin_note": "Addressed"}` | `200 OK` |

---

## Testing Flow (Step-by-Step)

### Step 1: Submit Feedback (User Role)
**Request:**
```http
POST /api/v1/feedback
Authorization: Bearer <USER_TOKEN>
Content-Type: application/json

{
  "rating": 5,
  "feedback": "The app is very easy to use and the booking process is smooth.",
  "platform": "WEB",
  "app_version": "1.0.0"
}
```

**Response (`201 Created`):**
```json
{
  "success": true,
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "user_id": 102,
    "rating": 5,
    "feedback": "The app is very easy to use and the booking process is smooth.",
    "platform": "WEB",
    "app_version": "1.0.0",
    "status": "NEW",
    "created_at": "2026-09-30T13:00:00Z"
  }
}
```

---

### Step 2: Validate Single-Submission Constraint (Duplicate Submission)
Attempt to submit feedback again using the **same user token**.

**Request:**
```http
POST /api/v1/feedback
Authorization: Bearer <USER_TOKEN>
Content-Type: application/json

{
  "rating": 4,
  "feedback": "Another feedback attempt."
}
```

**Response (`409 Conflict`):**
```json
{
  "error": {
    "code": "ALREADY_EXISTS",
    "message": "Feedback already submitted"
  }
}
```

---

### Step 3: Validate Request Inputs

#### A. Out-of-Range Rating
```http
POST /api/v1/feedback
Authorization: Bearer <USER_TOKEN_2>

{
  "rating": 6,
  "feedback": "Invalid rating test"
}
```
**Response (`400 Bad Request`):**
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Rating must be between 1 and 5"
  }
}
```

#### B. Empty Submission
```http
POST /api/v1/feedback
Authorization: Bearer <USER_TOKEN_3>

{
  "rating": null,
  "feedback": ""
}
```
**Response (`400 Bad Request`):**
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Either a rating or feedback message is required"
  }
}
```

---

### Step 4: Admin Endpoints Access Control

#### Non-Admin User Attempting Admin List API
```http
GET /api/v1/feedback
Authorization: Bearer <USER_TOKEN>
```
**Response (`403 Forbidden`):**
```json
{
  "error": {
    "code": "FORBIDDEN",
    "message": "Access denied"
  }
}
```

---

### Step 5: Admin Operations

#### A. List All Feedback (Admin Role)
```http
GET /api/v1/feedback?page=1&size=10&status=NEW
Authorization: Bearer <ADMIN_TOKEN>
```
**Response (`200 OK`):**
```json
{
  "content": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "user_id": 102,
      "rating": 5,
      "feedback": "The app is very easy to use and the booking process is smooth.",
      "status": "NEW",
      "created_at": "2026-09-30T13:00:00Z"
    }
  ],
  "page": 1,
  "size": 10,
  "total_elements": 1,
  "total_pages": 1
}
```

#### B. Update Feedback Status (Admin Role)
```http
PATCH /api/v1/feedback/550e8400-e29b-41d4-a716-446655440000/status
Authorization: Bearer <ADMIN_TOKEN>
Content-Type: application/json

{
  "status": "RESOLVED",
  "admin_note": "Reviewed and verified user feedback."
}
```
**Response (`200 OK`):**
```json
{
  "success": true,
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "status": "RESOLVED",
    "admin_note": "Reviewed and verified user feedback.",
    "updated_at": "2026-09-30T13:05:00Z"
  }
}
```
