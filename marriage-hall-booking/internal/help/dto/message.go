package dto

import (
	"time"
)

// CreateHelpMessageRequest represents the client payload for submitting a support message.
// User identity details (user_id, name, email, phone) are strictly omitted from the request
// body and automatically extracted from the authenticated user context.
type CreateHelpMessageRequest struct {
	Message string `json:"message"`
}

// HelpMessageResponse represents the Help Center message returned to clients and administrators.
type HelpMessageResponse struct {
	ID        string    `json:"id"`
	UserID    *int64    `json:"userId,omitempty"`
	UserName  string    `json:"userName"`
	UserEmail *string   `json:"userEmail,omitempty"`
	UserPhone *string   `json:"userPhone,omitempty"`
	Message   string    `json:"message"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// HelpMessageFilter contains query and filtering criteria for the Super Admin management API.
type HelpMessageFilter struct {
	Search    *string    `json:"search"`
	Status    *string    `json:"status"`
	UserID    *int64     `json:"userId"`
	FromDate  *time.Time `json:"fromDate"`
	ToDate    *time.Time `json:"toDate"`
	Page      int        `json:"page"`
	Limit     int        `json:"limit"`
	SortBy    string     `json:"sortBy"`
	SortOrder string     `json:"sortOrder"`
}
