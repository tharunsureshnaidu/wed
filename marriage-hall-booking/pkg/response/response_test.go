package response

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"
)

func TestUnencodableBodyIsA500NotAnEmpty200(t *testing.T) {
	w := httptest.NewRecorder()
	OK(w, "x", map[string]any{"distanceKm": math.NaN()})
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var env Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.ErrorCode != "INTERNAL_ERROR" {
		t.Fatalf("body %q, err %v", w.Body.String(), err)
	}
}
