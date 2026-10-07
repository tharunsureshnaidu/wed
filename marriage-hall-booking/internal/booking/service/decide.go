package service

import (
	"context"
	"errors"
	"net/http"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
)

// OwnerBookings is the venue owner's inbox: the bookings made at their
// facilities. Customers have GET /bookings for their own; an owner had no
// equivalent, so a booking request arrived by notification and then could not
// be found anywhere in the API.
func (s *Service) OwnerBookings(ctx context.Context, ownerID int64, status string, page, size int) ([]repository.Booking, int64, error) {
	return s.repo.ListForOwner(ctx, ownerID, status, page, size)
}

// Decide is the owner accepting or rejecting a booking request.
//
// CONFIRMED means "the venue accepted", not "the money arrived". The two are
// deliberately separate: a hall agrees an advance offline and the balance is
// paid later, so requiring full payment would make the owner's decision
// unusable. Payment still confirms a booking on its own, as it always has.
func (s *Service) Decide(ctx context.Context, bookingID string, actorID int64, isAdmin bool, confirm bool, reason string) (*repository.Booking, error) {
	b, err := s.repo.Get(ctx, bookingID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, "BOOKING_NOT_FOUND", "Booking not found")
	}
	if err != nil {
		return nil, err
	}

	// The owner of the venue decides, or an admin acting for them. A customer
	// cannot confirm their own booking - they already have cancel.
	if !isAdmin {
		ownerID, err := s.repo.FacilityOwner(ctx, b.TargetID)
		if err != nil {
			return nil, err
		}
		if ownerID != actorID {
			return nil, apperr.Forbidden("NOT_FACILITY_OWNER",
				"Only the venue owner can decide on this booking")
		}
	}

	to, from := "CONFIRMED", []string{"PENDING"}
	if !confirm {
		to = "REJECTED"
	}
	if reason == "" {
		reason = "Confirmed by venue"
		if !confirm {
			reason = "Rejected by venue"
		}
	}

	// Only a PENDING booking can be decided. An already-confirmed one is a
	// no-op the owner should be told about rather than silently re-confirmed,
	// and a cancelled one must never come back to life.
	if err := s.repo.SetStatus(ctx, bookingID, to, reason, from...); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.Conflict("INVALID_STATE",
				"Only a pending booking can be confirmed or rejected (this one is "+b.Status+")")
		}
		return nil, err
	}

	// A rejected booking must give its dates back, or the venue stays blocked
	// by a booking it just turned down.
	if !confirm {
		if err := s.repo.ReleaseInventory(ctx, bookingID); err != nil {
			return nil, err
		}
	}

	b, err = s.repo.Get(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	_ = s.repo.EnrichUsers(ctx, []*repository.Booking{b})
	if s.OnBookingDecided != nil {
		s.OnBookingDecided(ctx, b, confirm, reason)
	}
	return b, nil
}
