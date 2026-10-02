package dto

import (
	"time"
)

// CreateFeedbackRequest represents payload submitted by end-users.
type CreateFeedbackRequest struct {
	Rating     *int    `json:"rating"`
	Feedback   *string `json:"feedback"`
	Message    *string `json:"message"` // alias for feedback for backward-compatibility
	AppVersion *string `json:"appVersion,omitempty"`
	Platform   *string `json:"platform,omitempty"`
}

// GetContent returns the text feedback regardless of whether "feedback" or "message" was used.
func (r *CreateFeedbackRequest) GetContent() string {
	if r.Feedback != nil && *r.Feedback != "" {
		return *r.Feedback
	}
	if r.Message != nil && *r.Message != "" {
		return *r.Message
	}
	return ""
}

// UpdateFeedbackRequest represents status and notes updated by administrators.
type UpdateFeedbackRequest struct {
	Status    *string `json:"status"`
	AdminNote *string `json:"adminNote"`
}

// FeedbackResponse represents the client/admin view of a feedback record.
type FeedbackResponse struct {
	ID            string     `json:"id"`
	UserID        *int64     `json:"userId,omitempty"`
	UserName      *string    `json:"userName,omitempty"`
	UserEmail     *string    `json:"userEmail,omitempty"`
	Rating        *int       `json:"rating"`
	Feedback      string     `json:"feedback"`
	Message       string     `json:"message"` // duplicate alias to satisfy legacy clients
	AttachmentURL *string    `json:"attachmentUrl,omitempty"`
	AppVersion    *string    `json:"appVersion,omitempty"`
	Platform      *string    `json:"platform,omitempty"`
	Status        string     `json:"status"`
	AdminNote     *string    `json:"adminNote,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	ResolvedAt    *time.Time `json:"resolvedAt,omitempty"`
}

// AdminFeedbackSummaryResponse represents aggregate rating metrics.
type AdminFeedbackSummaryResponse struct {
	TotalReviews  int64   `json:"totalReviews"`
	AverageRating float64 `json:"averageRating"`
	FiveStar      int64   `json:"fiveStar"`
	FourStar      int64   `json:"fourStar"`
	ThreeStar     int64   `json:"threeStar"`
	TwoStar       int64   `json:"twoStar"`
	OneStar       int64   `json:"oneStar"`
}

// FeedbackFilter contains query parameters for listing feedback in the admin portal.
type FeedbackFilter struct {
	Rating *int    `json:"rating"`
	Status *string `json:"status"`
	Search *string `json:"search"`
	Sort   string  `json:"sort"` // "newest" or "oldest"
	Page   int     `json:"page"`
	Size   int     `json:"size"`
}
