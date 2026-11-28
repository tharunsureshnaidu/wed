package dto

import (
	"time"
)

// CreateContactRequest defines the public payload for submitting a contact inquiry.
type CreateContactRequest struct {
	Name    string  `json:"name"`
	Email   string  `json:"email"`
	Phone   *string `json:"phone,omitempty"`
	Subject string  `json:"subject"`
	Message string  `json:"message"`
}

// UpdateContactRequest defines the administrator payload for updating status or admin response.
type UpdateContactRequest struct {
	Status        *string `json:"status,omitempty"`
	AdminResponse *string `json:"adminResponse,omitempty"`
}

// ContactResponse represents the standardized Contact Us record returned to clients.
type ContactResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Email         string    `json:"email"`
	Phone         *string   `json:"phone,omitempty"`
	Subject       string    `json:"subject"`
	Message       string    `json:"message"`
	Status        string    `json:"status"`
	AdminResponse *string   `json:"adminResponse,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ContactFilter specifies pagination and status filtering for admin listing.
type ContactFilter struct {
	Status *string `json:"status"`
	Page   int     `json:"page"`
	Size   int     `json:"size"`
}

