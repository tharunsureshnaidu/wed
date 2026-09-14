package repository

import (
	"encoding/json"
	"testing"
)

func ptr[T any](v T) *T { return &v }

// The venue detail screen reads amenity.icon, and the column is NULL for every
// row, so the value has to be derived or clients get null for all 40.
func TestAmenityIconDerivedFromCode(t *testing.T) {
	for _, tc := range []struct {
		code, name, want string
	}{
		{"AIR_CONDITIONING", "Air Conditioning", "air-conditioning"},
		{"RESTAURANT", "Restaurant", "restaurant"},
		{"24_HOUR_FRONT_DESK", "24-Hours Front Desk", "24-hour-front-desk"},
		{"", "Valet Parking", "valet-parking"}, // no code: fall back to the name
	} {
		a := Amenity{Name: tc.name}
		if tc.code != "" {
			a.Code = ptr(tc.code)
		}
		var got map[string]any
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if got["icon"] != tc.want {
			t.Errorf("code %q: icon = %v, want %q", tc.code, got["icon"], tc.want)
		}
		// The flat fields must survive the custom marshaller.
		if got["name"] != tc.name {
			t.Errorf("code %q: name lost, got %v", tc.code, got["name"])
		}
	}
}

func TestFacilityNestedBlocks(t *testing.T) {
	f := Facility{
		ID: "f1", Type: "HOTEL", Name: "The Aston Vill",
		City: ptr("Vuem Point"), State: ptr("Michigan"), Country: ptr("USA"),
		FullAddress: ptr("1 Main St"),
		Lat:         ptr(42.3314), Lng: ptr(-83.0458),
		AvgRating: 4.6, ReviewCount: 532,
		StartingPrice: ptr(1420.0),
	}
	var got map[string]any
	b, _ := json.Marshal(f)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}

	// Flat fields stay put: callers built against them must not break.
	for _, k := range []string{"city", "avgRating", "startingPrice", "fullAddress"} {
		if _, ok := got[k]; !ok {
			t.Errorf("flat field %q disappeared", k)
		}
	}

	loc := got["location"].(map[string]any)
	if loc["city"] != "Vuem Point" || loc["latitude"] != 42.3314 {
		t.Errorf("location = %v", loc)
	}
	if r := got["rating"].(map[string]any); r["value"] != 4.6 || r["reviewCount"] != float64(532) {
		t.Errorf("rating = %v", r)
	}
	p := got["price"].(map[string]any)
	if p["amount"] != 1420.0 || p["currency"] != "INR" || p["period"] != "NIGHT" {
		t.Errorf("price = %v", p)
	}
	if c := got["coordinates"].(map[string]any); c["latitude"] != 42.3314 {
		t.Errorf("coordinates = %v", c)
	}
}

// A hall prices per day, and a venue that was never geocoded must not report
// a (0,0) pin or a zero price.
func TestFacilityNullsRatherThanZeros(t *testing.T) {
	var got map[string]any
	b, _ := json.Marshal(Facility{ID: "f2", Type: "MARRIAGE_HALL"})
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["coordinates"] != nil {
		t.Errorf("coordinates = %v, want null when lat/lng missing", got["coordinates"])
	}
	if got["price"] != nil {
		t.Errorf("price = %v, want null when there is no price", got["price"])
	}

	hall := Facility{ID: "f3", Type: "MARRIAGE_HALL", StartingPrice: ptr(150000.0)}
	b, _ = json.Marshal(hall)
	_ = json.Unmarshal(b, &got)
	if p := got["price"].(map[string]any); p["period"] != "DAY" {
		t.Errorf("hall period = %v, want DAY", p["period"])
	}
}
