package domain

import (
	"time"
)

// HelpCenterStatus represents the workflow state of a customer support message.
type HelpCenterStatus string

const (
	StatusNew  HelpCenterStatus = "NEW"
	StatusRead HelpCenterStatus = "READ"
)

// Valid checks if the status is supported.
func (s HelpCenterStatus) Valid() bool {
	switch s {
	case StatusNew, StatusRead:
		return true
	default:
		return false
	}
}

// HelpCenterMessage represents a customer inquiry or support message in the domain.
type HelpCenterMessage struct {
	ID        string           `json:"id"`
	UserID    *int64           `json:"userId,omitempty"`
	UserName  string           `json:"userName"`
	UserEmail *string          `json:"userEmail,omitempty"`
	UserPhone *string          `json:"userPhone,omitempty"`
	Message   string           `json:"message"`
	Status    HelpCenterStatus `json:"status"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	DeletedAt *time.Time       `json:"deletedAt,omitempty"`
	IsDeleted bool             `json:"isDeleted"`
}

// UserProfile holds the essential contact details fetched from the authenticated account.
type UserProfile struct {
	ID    int64
	Name  string
	Email *string
	Phone *string
}
