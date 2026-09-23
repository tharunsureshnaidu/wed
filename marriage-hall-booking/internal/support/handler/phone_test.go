package handler

import "testing"

// A helpline is displayed to humans, so it keeps the formatting an admin types.
// validate.Phone enforces E.164 for a user's login number and would reject
// every realistic helpline - this checks digits instead, without loosening the
// shared validator that guards signup.
func TestValidPhoneDisplay(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"+91 98765 43210", true},
		{"+918041234567", true},
		{"080-4123-4567", true},
		{"(080) 4123 4567", true},
		{"1800 123 4567", true},
		// Too few digits to be a phone number.
		{"123", false},
		{"", false},
		// Not a phone number at all.
		{"abc", false},
		{"call us", false},
		{"+91 98765 43210 ext 5", false},
		// Too many digits - a pasted account number, not a helpline.
		{"1234567890123456", false},
	} {
		if got := validPhoneDisplay(tc.in); got != tc.want {
			t.Errorf("validPhoneDisplay(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// Every field the PUT accepts must map to a settings key, or an edit silently
// writes to the empty key and the value disappears.
func TestEverySupportFieldHasAKey(t *testing.T) {
	s := "x"
	req := supportReq{
		SupportEmail: &s, SupportPhone: &s, SupportWhatsapp: &s,
		SupportHours: &s, SupportAddress: &s,
	}
	fields := req.fields()
	if len(fields) != len(settingKeys) {
		t.Fatalf("mapped %d fields, but settingKeys has %d", len(fields), len(settingKeys))
	}
	if _, bad := fields[""]; bad {
		t.Error("a field mapped to the empty key - its settingKeys entry is missing")
	}
	for _, key := range settingKeys {
		if _, ok := fields[key]; !ok {
			t.Errorf("settings key %q is never written by the PUT", key)
		}
	}
}
