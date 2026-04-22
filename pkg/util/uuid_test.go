package util

import "testing"

func TestIsUUIDv7(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid v7 lowercase", "0196634d-5d66-75c4-b778-e317f49dab7a", true},
		{"valid v7 uppercase", "0196634D-5D66-75C4-B778-E317F49DAB7A", true},
		{"valid v7 variant 9", "0196634d-5d66-75c4-9778-e317f49dab7a", true},
		{"valid v7 variant a", "0196634d-5d66-75c4-a778-e317f49dab7a", true},
		{"valid v7 variant b", "0196634d-5d66-75c4-b778-e317f49dab7a", true},
		{"v4 rejected (wrong version)", "0196634d-5d66-45c4-b778-e317f49dab7a", false},
		{"bad variant nibble", "0196634d-5d66-75c4-c778-e317f49dab7a", false},
		{"missing dashes", "0196634d5d6675c4b778e317f49dab7a", false},
		{"too short", "0196634d-5d66-75c4-b778-e317f49dab7", false},
		{"non-hex", "0196634d-5d66-75c4-b778-e317f49dab7z", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUUIDv7(tc.in); got != tc.want {
				t.Errorf("IsUUIDv7(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
