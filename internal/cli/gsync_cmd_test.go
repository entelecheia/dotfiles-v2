package cli

import (
	"bytes"
	"os"
	"strings"
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

func TestRootRegistersGsyncPrimaryAndLegacyAlias(t *testing.T) {
	root := NewRootCmd("dev", "test")
	known := knownSubcommands(root)
	for _, name := range []string{"gsync", "gdrive-sync"} {
		if !known[name] {
			t.Fatalf("knownSubcommands missing %q", name)
		}
	}

	cmd, _, err := root.Find([]string{"gsync"})
	if err != nil {
		t.Fatalf("Find(gsync): %v", err)
	}
	if cmd.Name() != "gsync" {
		t.Fatalf("Find(gsync) = %q, want gsync", cmd.Name())
	}

	legacy, _, err := root.Find([]string{"gdrive-sync"})
	if err != nil {
		t.Fatalf("Find(gdrive-sync): %v", err)
	}
	if legacy.Name() != "gsync" {
		t.Fatalf("Find(gdrive-sync) = %q, want gsync", legacy.Name())
	}
}

func TestBareGsyncPrintsHelp(t *testing.T) {
	cmd := newGsyncCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bare gsync execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Run without a subcommand to print this help.",
		"Legacy alias: 'dot gdrive-sync' continues to work.",
		"dot gsync push",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("bare gsync help missing %q\n--- got ---\n%s", want, got)
		}
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
