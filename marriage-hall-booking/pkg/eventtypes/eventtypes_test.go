package eventtypes

import "testing"

func TestCatalogueCodesAreStableAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Catalogue {
		if seen[e.Code] {
			t.Fatalf("duplicate code %q - ByCode would silently drop one", e.Code)
		}
		seen[e.Code] = true
		if e.Code == "" || e.Name == "" || e.Category == "" {
			t.Fatalf("incomplete entry: %+v", e)
		}
		if e.Code != upper(e.Code) {
			t.Fatalf("code %q is not upper case; stored values would not match", e.Code)
		}
	}
	// These are written into facility_events and bookings.event_type. Renaming
	// one orphans every row that used it, so the test pins them.
	for _, must := range []string{"WEDDING", "RECEPTION", "ENGAGEMENT", "BIRTHDAY", "OTHER"} {
		if !ValidEventCode(must) {
			t.Fatalf("%q vanished from the catalogue - existing bookings carry it", must)
		}
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

func TestValidEventCodeIsForgivingAboutCase(t *testing.T) {
	for _, in := range []string{"WEDDING", "wedding", " Wedding "} {
		if !ValidEventCode(in) {
			t.Errorf("ValidEventCode(%q) = false, want true", in)
		}
	}
	if ValidEventCode("NOT_A_REAL_EVENT") {
		t.Error("an unknown code was accepted")
	}
	if ValidEventCode("") {
		t.Error("the empty string was accepted as an event")
	}
}

// A venue may hold a code from an older release. Showing the raw code beats
// showing a blank cell.
func TestViewsKeepsUnknownCodes(t *testing.T) {
	out := Views([]string{"WEDDING", "LEGACY_CODE"})
	if len(out) != 2 {
		t.Fatalf("got %d views, want 2 - an unknown code was dropped", len(out))
	}
	var found bool
	for _, v := range out {
		if v.Code == "LEGACY_CODE" && v.Name == "LEGACY_CODE" {
			found = true
		}
	}
	if !found {
		t.Error("an unknown code lost its fallback name")
	}
	// Sorted by display name, or the picker reshuffles between calls.
	for i := 1; i < len(out); i++ {
		if out[i-1].Name > out[i].Name {
			t.Fatalf("not sorted: %q before %q", out[i-1].Name, out[i].Name)
		}
	}
}

func TestEventNameFallsBackToCode(t *testing.T) {
	if got := EventName("WEDDING"); got != "Wedding" {
		t.Errorf("EventName(WEDDING) = %q, want Wedding", got)
	}
	if got := EventName("MYSTERY"); got != "MYSTERY" {
		t.Errorf("EventName(MYSTERY) = %q, want the code back", got)
	}
}
