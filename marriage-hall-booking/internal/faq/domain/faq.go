package domain

import "time"

// FAQ represents a platform Frequently Asked Question entity.
type FAQ struct {
	ID           string     `json:"id"`
	Question     string     `json:"question"`
	Answer       string     `json:"answer"`
	DisplayOrder int        `json:"displayOrder"`
	IsActive     bool       `json:"isActive"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	DeletedAt    *time.Time `json:"deletedAt,omitempty"`
	IsDeleted    bool       `json:"isDeleted"`
}
