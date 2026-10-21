package dto

import "time"

// CreateFAQRequest defines the admin payload for creating a new FAQ.
type CreateFAQRequest struct {
	Question     string `json:"question"`
	Answer       string `json:"answer"`
	DisplayOrder *int   `json:"displayOrder"`
	IsActive     *bool  `json:"isActive"`
}

// UpdateFAQRequest defines the admin payload for updating an existing FAQ.
type UpdateFAQRequest struct {
	Question     string `json:"question"`
	Answer       string `json:"answer"`
	DisplayOrder *int   `json:"displayOrder"`
	IsActive     *bool  `json:"isActive"`
}

// FAQResponse represents the full FAQ entity returned to administrative clients.
type FAQResponse struct {
	ID           string    `json:"id"`
	Question     string    `json:"question"`
	Answer       string    `json:"answer"`
	DisplayOrder int       `json:"displayOrder"`
	IsActive     bool      `json:"isActive"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// PublicFAQResponse represents the sanitised FAQ model exposed to end users and mobile clients.
// Administrative metadata (timestamps, isActive flag) is omitted.
type PublicFAQResponse struct {
	ID           string `json:"id"`
	Question     string `json:"question"`
	Answer       string `json:"answer"`
	DisplayOrder int    `json:"displayOrder"`
}
