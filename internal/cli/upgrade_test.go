package cli

import "testing"

func TestParseSemver(t *testing.T) {
	tests := []struct {
		in              string
		wantMajor       int
		wantMinor       int
		wantPatch       int
		wantOK          bool
	}{
		{"1.2.3", 1, 2, 3, true},
		{"0.1.0", 0, 1, 0, true},
		{"10.20.30", 10, 20, 30, true},
		{"1.2.3-beta.1", 1, 2, 3, true},
		{"1.2.3+build", 1, 2, 3, true},
		{"1.2", 0, 0, 0, false},
		{"1.2.3.4", 0, 0, 0, false},
		{"dev", 0, 0, 0, false},
		{"", 0, 0, 0, false},
		{"abc.def.ghi", 0, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			major, minor, patch, ok := parseSemver(tt.in)
			if ok != tt.wantOK {
				t.Errorf("ok=%v, want %v", ok, tt.wantOK)
			}
			if ok {
				if major != tt.wantMajor || minor != tt.wantMinor || patch != tt.wantPatch {
					t.Errorf("got %d.%d.%d, want %d.%d.%d",
						major, minor, patch, tt.wantMajor, tt.wantMinor, tt.wantPatch)
				}
			}
		})
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.2.0", "1.10.0", -1},  // numeric compare, not string
		{"1.10.0", "1.9.0", 1},   // critical: string compare would fail
		{"2.0.0", "1.99.99", 1},
		{"0.14.0", "0.13.0", 1},
		{"dev", "1.0.0", -1},     // dev is older than any release
		{"1.0.0", "dev", 1},
		{"dev", "dev", 0},
		{"1.0.0-beta", "1.0.0", 0}, // pre-release stripped, equal
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := compareSemver(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
