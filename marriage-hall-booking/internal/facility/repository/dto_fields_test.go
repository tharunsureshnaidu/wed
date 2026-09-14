package repository

import (
	"encoding/json"
	"testing"
)

// The facility body is a client contract carried over from Java's
// MarriageHallDTO/HotelDTO. Fields have gone missing twice: once because the
// query never selected columns that existed (lat/lng), and once because
// omitempty dropped a nil pointer instead of sending null.
func TestFacilityJSONHasEveryJavaField(t *testing.T) {
	b, err := json.Marshal(Facility{})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	// Union of MarriageHallDTO and HotelDTO.
	for _, f := range []string{
		"id", "ownerId", "ownerPhoneNumber", "vendorId", "name", "description",
		"city", "fullAddress", "state", "zipcode", "lat", "lng", "status",
		"avgRating", "reviewCount", "amenities", "images",
		"capacityPax", "areaSqft", "basePricePerDay", "seatingCapacity",
		"floatingCapacity", "minBookingSize",
		"starRating", "checkInTime", "checkOutTime", "startingPrice",
	} {
		if _, ok := got[f]; !ok {
			t.Errorf("facility body is missing %q, which Java's DTO returns", f)
		}
	}
}
