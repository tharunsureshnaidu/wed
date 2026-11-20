package service

import "testing"

func TestHourlyDuration(t *testing.T) {
	tests := []struct {
		name      string
		startTime string
		endTime   string
		want      int
		wantErr   bool
	}{
		{"2 hours standard", "14:00", "16:00", 2, false},
		{"4 hours standard", "10:00", "14:00", 4, false},
		{"6 hours standard", "12:00", "18:00", 6, false},
		{"with minutes rounding to whole hours", "14:15", "16:15", 2, false},
		{"end before start", "16:00", "14:00", 0, true},
		{"same start and end", "14:00", "14:00", 0, true},
		{"invalid start format", "invalid", "16:00", 0, true},
		{"invalid end format", "14:00", "invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := HourlyDuration(tt.startTime, tt.endTime)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HourlyDuration(%q, %q) error = %v, wantErr %v", tt.startTime, tt.endTime, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("HourlyDuration(%q, %q) = %d, want %d", tt.startTime, tt.endTime, got, tt.want)
			}
		})
	}
}

