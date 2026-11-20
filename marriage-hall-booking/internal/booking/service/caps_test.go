package service

import (
	"context"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/repository"
)

// The caps fire before any database work, so a zero Service is enough.
func TestRequestSizeCaps(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	start := Today().AddDate(0, 0, 30)

	_, err := s.CreateHallBooking(ctx, 1, HallBookingRequest{
		EventDate: start, EndDate: start.AddDate(0, 0, maxHallDays),
	})
	if code(err) != "VALIDATION_ERROR" {
		t.Fatalf("15-day hall booking: want VALIDATION_ERROR, got %v", err)
	}
	_, err = s.CreateHallBooking(ctx, 1, HallBookingRequest{
		EventDate: start, PackageIDs: make([]string, maxLines+1),
	})
	if code(err) != "VALIDATION_ERROR" {
		t.Fatalf("21 packages: want VALIDATION_ERROR, got %v", err)
	}
	_, err = s.CreateHotelBooking(ctx, 1, HotelBookingRequest{
		CheckIn: start, CheckOut: start.AddDate(0, 0, maxHotelNight+1),
		Rooms: make([]repository.RoomLine, 1),
	})
	if code(err) != "VALIDATION_ERROR" {
		t.Fatalf("31-night stay: want VALIDATION_ERROR, got %v", err)
	}
}

func TestTodayIsTheIndianDate(t *testing.T) {
	d := Today()
	if d.Location() != time.UTC || d.Hour() != 0 || d.Minute() != 0 {
		t.Fatalf("Today() must be a UTC-midnight date, got %v", d)
	}
	y, m, day := time.Now().In(ist).Date()
	if d.Year() != y || d.Month() != m || d.Day() != day {
		t.Fatalf("Today() = %v, want the IST date %d-%02d-%02d", d, y, m, day)
	}
}
