package config

import "testing"

func TestParseOSRelease(t *testing.T) {
	values := parseOSRelease(`
# comment
NAME="Example Linux"
ID=manjaro
ID_LIKE="arch linux"
EMPTY=
`)

	if values["ID"] != "manjaro" {
		t.Fatalf("ID = %q, want manjaro", values["ID"])
	}
	if values["ID_LIKE"] != "arch linux" {
		t.Fatalf("ID_LIKE = %q, want arch linux", values["ID_LIKE"])
	}
	if values["EMPTY"] != "" {
		t.Fatalf("EMPTY = %q, want empty", values["EMPTY"])
	}
}

func TestSystemInfoIsArchLinux(t *testing.T) {
	tests := []struct {
		name string
		info *SystemInfo
		want bool
	}{
		{name: "arch", info: &SystemInfo{OS: "linux", DistroID: "arch"}, want: true},
		{name: "arch derived", info: &SystemInfo{OS: "linux", DistroID: "manjaro", DistroLike: []string{"arch"}}, want: true},
		{name: "ubuntu", info: &SystemInfo{OS: "linux", DistroID: "ubuntu", DistroLike: []string{"debian"}}, want: false},
		{name: "darwin", info: &SystemInfo{OS: "darwin", DistroID: "arch"}, want: false},
		{name: "nil", info: nil, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.info.IsArchLinux(); got != tc.want {
				t.Fatalf("IsArchLinux() = %v, want %v", got, tc.want)
			}
		})
	}
}
