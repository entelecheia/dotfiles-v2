package cli

import (
	"os"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/gsync"
)

func TestParseIntervalFlag(t *testing.T) {
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
		got, err := parseIntervalFlag(tc.raw)
		if (err != nil) != tc.wantErr {
			t.Fatalf("parseIntervalFlag(%q) err=%v wantErr=%v", tc.raw, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("parseIntervalFlag(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestParseAutomaticModeFlag(t *testing.T) {
	for _, raw := range []string{"clean", "force"} {
		if _, err := parseAutomaticModeFlag(raw); err != nil {
			t.Fatalf("parseAutomaticModeFlag(%q): %v", raw, err)
		}
	}
	for _, raw := range []string{"manual", "bogus"} {
		if _, err := parseAutomaticModeFlag(raw); err == nil {
			t.Fatalf("parseAutomaticModeFlag(%q) should fail", raw)
		}
	}
}

func TestParseFilterMode(t *testing.T) {
	for _, raw := range []string{"include", "exclude", "INCLUDE"} {
		if _, err := gsync.ParseFilterMode(raw); err != nil {
			t.Fatalf("ParseFilterMode(%q): %v", raw, err)
		}
	}
	if _, err := gsync.ParseFilterMode("legacy"); err == nil {
		t.Fatal("ParseFilterMode(legacy) should fail")
	}
}

func TestSetLocalSchedule_DryRunDoesNotPersist(t *testing.T) {
	paths := gsync.ResolveLocalPaths(t.TempDir())
	cfg := &gsync.Config{LocalPaths: paths}

	if err := setLocalSchedule(cfg, 600, 900, gsync.ModeClean, gsync.ModeForce, true); err != nil {
		t.Fatalf("setLocalSchedule dry-run: %v", err)
	}
	if cfg.Interval != 600 || cfg.PullInterval != 900 || cfg.PushMode != gsync.ModeClean || cfg.PullMode != gsync.ModeForce {
		t.Fatalf("dry-run should still update runtime cfg for planning, got %+v", cfg)
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write local config; stat err=%v", err)
	}
}
