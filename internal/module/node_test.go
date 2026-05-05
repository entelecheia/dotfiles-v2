package module

import "testing"

func TestParseFnmHasVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"none-text", "No installed Node versions\n", false},
		{"single-version", "* v20.11.0 default\n", true},
		{"plain-version", "v22.5.1\n", true},
		{"multiple-versions", "  v18.19.0\n* v20.11.0 default\n  v22.5.1\n", true},
		{"system-only", "* system\n", false},
		{"non-numeric-suffix", "vX.Y.Z\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseFnmHasVersion(tc.in); got != tc.want {
				t.Fatalf("parseFnmHasVersion(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
