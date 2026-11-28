package domain

import (
	"time"
)

// Status represents the lifecycle state of a Contact Us request.
type Status string

const (
	StatusOpen       Status = "OPEN"
	StatusInProgress Status = "IN_PROGRESS"
	StatusResolved   Status = "RESOLVED"
	StatusClosed     Status = "CLOSED"
)

// Valid checks if the given status is an accepted value.
func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusInProgress, StatusResolved, StatusClosed:
		return true
	default:
		return false
	}
}

// CanTransitionTo enforces valid workflow state transitions.
func (s Status) CanTransitionTo(next Status) bool {
	if s == next {
		return true
	}
	switch s {
	case StatusOpen:
		return next == StatusInProgress || next == StatusResolved || next == StatusClosed
	case StatusInProgress:
		return next == StatusOpen || next == StatusResolved || next == StatusClosed
	case StatusResolved:
		return next == StatusClosed || next == StatusInProgress || next == StatusOpen
	case StatusClosed:
		// A closed inquiry can be reopened to OPEN or IN_PROGRESS.
		return next == StatusOpen || next == StatusInProgress
	default:
		return false
	}
}

// ContactUs represents a Contact Us inquiry entity.
type ContactUs struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Email         string     `json:"email"`
	Phone         *string    `json:"phone,omitempty"`
	Subject       string     `json:"subject"`
	Message       string     `json:"message"`
	Status        Status     `json:"status"`
	AdminResponse *string    `json:"adminResponse,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	DeletedAt     *time.Time `json:"deletedAt,omitempty"`
	IsDeleted     bool       `json:"isDeleted"`
}

