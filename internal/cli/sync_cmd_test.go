package cli

import (
	"bytes"
	"io"
	"os"
	osexec "os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
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
		if _, err := syncer.ParseFilterMode(raw); err != nil {
			t.Fatalf("ParseFilterMode(%q): %v", raw, err)
		}
	}
	if _, err := syncer.ParseFilterMode("legacy"); err == nil {
		t.Fatal("ParseFilterMode(legacy) should fail")
	}
}

func TestRootRegistersSyncPrimaryAndLegacyAlias(t *testing.T) {
	root := NewRootCmd("dev", "test")
	known := knownSubcommands(root)
	for _, name := range []string{"sync", "gsync", "gdrive-sync"} {
		if !known[name] {
			t.Fatalf("knownSubcommands missing %q", name)
		}
	}

	cmd, _, err := root.Find([]string{"sync"})
	if err != nil {
		t.Fatalf("Find(sync): %v", err)
	}
	if cmd.Name() != "sync" {
		t.Fatalf("Find(sync) = %q, want sync", cmd.Name())
	}

	for _, alias := range []string{"gsync", "gdrive-sync"} {
		legacy, _, err := root.Find([]string{alias})
		if err != nil {
			t.Fatalf("Find(%s): %v", alias, err)
		}
		if legacy.Name() != "sync" {
			t.Fatalf("Find(%s) = %q, want sync", alias, legacy.Name())
		}
	}
}

func TestBareSyncPrintsHelp(t *testing.T) {
	cmd := newSyncCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bare gsync execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Run without a subcommand to print this help.",
		"Deprecated aliases: 'dot gsync', 'dot gdrive-sync'.",
		"dot sync push",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("bare gsync help missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestSetLocalSchedule_DryRunDoesNotPersist(t *testing.T) {
	paths := syncer.ResolveLocalPaths(t.TempDir())
	cfg := &syncer.Config{LocalPaths: paths}

	if err := setLocalSchedule(cfg, 600, 900, syncer.ModeClean, syncer.ModeForce, true); err != nil {
		t.Fatalf("setLocalSchedule dry-run: %v", err)
	}
	if cfg.Interval != 600 || cfg.PullInterval != 900 || cfg.PushMode != syncer.ModeClean || cfg.PullMode != syncer.ModeForce {
		t.Fatalf("dry-run should still update runtime cfg for planning, got %+v", cfg)
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write local config; stat err=%v", err)
	}
}

func TestResolvePruneCutoff(t *testing.T) {
	cases := []struct {
		name         string
		olderDays    int
		all          bool
		olderChanged bool
		wantErr      bool
		wantAgeDays  int // expected approximate distance from now
	}{
		{name: "default 30 days", olderDays: 30, wantAgeDays: 30},
		{name: "explicit 7 days", olderDays: 7, olderChanged: true, wantAgeDays: 7},
		{name: "all prunes everything", all: true, olderDays: 30, wantAgeDays: 0},
		{name: "all with explicit older-than rejected", all: true, olderDays: 7, olderChanged: true, wantErr: true},
		{name: "negative rejected", olderDays: -1, olderChanged: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cutoff, err := resolvePruneCutoff(tc.olderDays, tc.all, tc.olderChanged)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			got := time.Since(cutoff).Round(time.Minute)
			want := time.Duration(tc.wantAgeDays) * 24 * time.Hour
			if got != want {
				t.Errorf("cutoff age = %v, want %v", got, want)
			}
		})
	}
}

func TestSyncConflictsRegistersListAndPrune(t *testing.T) {
	cmd := newSyncConflictsCmd()
	names := map[string]bool{}
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}
	for _, want := range []string{"list", "prune"} {
		if !names[want] {
			t.Errorf("conflicts is missing %q subcommand", want)
		}
	}
	prune, _, err := cmd.Find([]string{"prune"})
	if err != nil {
		t.Fatalf("Find(prune): %v", err)
	}
	if prune.Flags().Lookup("older-than") == nil || prune.Flags().Lookup("all") == nil {
		t.Error("prune is missing --older-than/--all flags")
	}
}

// TestReportPushPartial pins the mirror push's treatment of rsync exit 23.
// The target is a cloud folder: files the client keeps online-only cannot be
// stat'd (macOS returns EDEADLK rather than hydrate synchronously), so rsync
// skips them and exits 23. Treating that as fatal made the scheduled push exit
// 1 on every cycle for a workspace containing any archived, never-opened file.
func TestReportPushPartial(t *testing.T) {
	p := &Printer{Out: io.Discard, Err: io.Discard}

	if got := reportPushPartial(p, nil); got != nil {
		t.Errorf("nil in, %v out", got)
	}

	partial := syncer.ClassifyRsyncError(rsyncExit(t, 23))
	if !syncer.IsPartialTransfer(partial) {
		t.Fatal("precondition: exit 23 should classify as partial")
	}
	if got := reportPushPartial(p, partial); got != nil {
		t.Errorf("partial transfer must not fail the push, got %v", got)
	}

	// A genuine failure must still fail: exit 12 is a protocol error, not a
	// skipped file, and silently succeeding there would hide a broken mirror.
	fatal := syncer.ClassifyRsyncError(rsyncExit(t, 12))
	if got := reportPushPartial(p, fatal); got == nil {
		t.Error("exit 12 must still fail the push")
	}
}

func rsyncExit(t *testing.T, code int) error {
	t.Helper()
	err := osexec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatalf("expected exit %d", code)
	}
	return err
}
