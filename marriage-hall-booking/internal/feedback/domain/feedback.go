package domain

import (
	"time"
)

// FeedbackStatus represents the workflow state of app feedback.
type FeedbackStatus string

const (
	StatusNew       FeedbackStatus = "NEW"
	StatusReviewing FeedbackStatus = "REVIEWING"
	StatusResolved  FeedbackStatus = "RESOLVED"
	StatusClosed    FeedbackStatus = "CLOSED"
)

func (s FeedbackStatus) Valid() bool {
	switch s {
	case StatusNew, StatusReviewing, StatusResolved, StatusClosed:
		return true
	default:
		return false
	}
}

// Feedback represents the domain entity for user feedback about the application.
type Feedback struct {
	ID            string         `json:"id"`
	UserID        *int64         `json:"userId"`
	Rating        *int           `json:"rating"`
	Message       string         `json:"message"`
	AttachmentURL *string        `json:"attachmentUrl,omitempty"`
	AppVersion    *string        `json:"appVersion,omitempty"`
	Platform      *string        `json:"platform,omitempty"`
	Status        FeedbackStatus `json:"status"`
	AdminNote     *string        `json:"adminNote,omitempty"`
	ResolvedBy    *int64         `json:"resolvedBy,omitempty"`
	ResolvedAt    *time.Time     `json:"resolvedAt,omitempty"`
	IsDeleted     bool           `json:"isDeleted"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

// FeedbackWithUser combines the feedback entity with basic user profile data.
type FeedbackWithUser struct {
	Feedback
	UserName  *string `json:"userName,omitempty"`
	UserEmail *string `json:"userEmail,omitempty"`
}

// FeedbackSummary aggregates ratings for the admin dashboard.
type FeedbackSummary struct {
	TotalReviews  int64   `json:"totalReviews"`
	AverageRating float64 `json:"averageRating"`
	FiveStar      int64   `json:"fiveStar"`
	FourStar      int64   `json:"fourStar"`
	ThreeStar     int64   `json:"threeStar"`
	TwoStar       int64   `json:"twoStar"`
	OneStar       int64   `json:"oneStar"`
}
