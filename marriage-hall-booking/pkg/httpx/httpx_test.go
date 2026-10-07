package httpx

import "testing"

func TestOffset(t *testing.T) {
	tests := []struct {
		page int
		size int
		want int
	}{
		{page: -1, size: 20, want: 0},
		{page: 0, size: 20, want: 0},
		{page: 1, size: 20, want: 0},
		{page: 2, size: 20, want: 20},
		{page: 3, size: 20, want: 40},
		{page: 1, size: 10, want: 0},
		{page: 2, size: 10, want: 10},
		{page: 0, size: 10, want: 0},
	}

	for _, tc := range tests {
		got := Offset(tc.page, tc.size)
		if got != tc.want {
			t.Errorf("Offset(%d, %d) = %d, want %d", tc.page, tc.size, got, tc.want)
		}
	}
}
