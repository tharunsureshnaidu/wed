package handler

import (
	"encoding/json"
	"testing"
)

// An unknown ?status= must be a 400, not an empty page: a typo that returns
// "no bookings" sends an owner looking for lost data.
func TestValidBookingStatus(t *testing.T) {
	for _, s := range bookingStatuses {
		if !validBookingStatus(s) {
			t.Errorf("validBookingStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "pending", "NOPE", "DELETED"} {
		if validBookingStatus(s) {
			t.Errorf("validBookingStatus(%q) = true, want false", s)
		}
	}
}

// REJECTED must be a status an owner can filter by. It is a distinct outcome
// from CANCELLED - a refusal the customer did not ask for - and the decision
// analytics counts them separately.
func TestRejectedIsAFilterableStatus(t *testing.T) {
	if !validBookingStatus("REJECTED") {
		t.Error("REJECTED is not filterable, so an owner cannot list what they turned down")
	}
	for _, s := range []string{"PENDING", "CONFIRMED", "CANCELLED"} {
		if !validBookingStatus(s) {
			t.Errorf("%s missing from the filter list", s)
		}
	}
}

func TestParseDecisionType(t *testing.T) {
	tests := []struct {
		typeVal     string
		actionVal   string
		statusVal   string
		wantConfirm bool
		wantOK      bool
	}{
		{typeVal: "confirm", wantConfirm: true, wantOK: true},
		{typeVal: "CONFIRM", wantConfirm: true, wantOK: true},
		{typeVal: "confirmed", wantConfirm: true, wantOK: true},
		{typeVal: "reject", wantConfirm: false, wantOK: true},
		{typeVal: "REJECT", wantConfirm: false, wantOK: true},
		{typeVal: "rejected", wantConfirm: false, wantOK: true},
		{actionVal: "confirm", wantConfirm: true, wantOK: true},
		{statusVal: "REJECTED", wantConfirm: false, wantOK: true},
		{typeVal: "invalid", wantConfirm: false, wantOK: false},
		{typeVal: "", wantConfirm: false, wantOK: false},
	}

	for _, tc := range tests {
		gotConfirm, gotOK := parseDecisionType(tc.typeVal, tc.actionVal, tc.statusVal)
		if gotOK != tc.wantOK {
			t.Errorf("parseDecisionType(%q, %q, %q) gotOK=%v, wantOK=%v",
				tc.typeVal, tc.actionVal, tc.statusVal, gotOK, tc.wantOK)
		}
		if tc.wantOK && gotConfirm != tc.wantConfirm {
			t.Errorf("parseDecisionType(%q, %q, %q) gotConfirm=%v, wantConfirm=%v",
				tc.typeVal, tc.actionVal, tc.statusVal, gotConfirm, tc.wantConfirm)
		}
	}
}

func TestDecideReqReasonAndRejectionReason(t *testing.T) {
	for _, tc := range []struct {
		jsonBody   string
		wantType   string
		wantReason string
	}{
		{
			jsonBody:   `{"status":"REJECTED","reason":"Date already reserved for offline annual conference."}`,
			wantType:   "REJECTED",
			wantReason: "Date already reserved for offline annual conference.",
		},
		{
			jsonBody:   `{"status":"REJECTED","rejectionReason":"Hall is unavailable on the selected date."}`,
			wantType:   "REJECTED",
			wantReason: "Hall is unavailable on the selected date.",
		},
		{
			jsonBody:   `{"type":"reject","reason":"Selected slot is already booked."}`,
			wantType:   "reject",
			wantReason: "Selected slot is already booked.",
		},
	} {
		var req decideReq
		if err := json.Unmarshal([]byte(tc.jsonBody), &req); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		confirm, ok := parseDecisionType(req.Type, req.Action, req.Status)
		if !ok || confirm {
			t.Errorf("expected rejection, got confirm=%v, ok=%v", confirm, ok)
		}
		reason := req.Reason
		if reason == "" && req.RejectionReason != "" {
			reason = req.RejectionReason
		}
		if reason != tc.wantReason {
			t.Errorf("got reason %q, want %q", reason, tc.wantReason)
		}
	}
}

