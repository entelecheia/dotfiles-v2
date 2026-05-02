package cli

import "testing"

func TestParsePullIntervalFlag(t *testing.T) {
	cases := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{"0", 0, false},
		{"15m", 900, false},
		{"900", 900, false},
		{"1h", 3600, false},
		{"5s", 0, true},
		{"10abc", 0, true},
		{"900abc", 0, true},
	}
	for _, tc := range cases {
		got, err := parsePullIntervalFlag(tc.raw)
		if (err != nil) != tc.wantErr {
			t.Fatalf("parsePullIntervalFlag(%q) err=%v wantErr=%v", tc.raw, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("parsePullIntervalFlag(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}
