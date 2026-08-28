package cli

import (
	"bytes"
	"io"
	osexec "os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

func TestSyncStatusSensitiveOverridesZeroOneMany(t *testing.T) {
	render := func(overrides []syncer.SensitiveOverride) string {
		t.Helper()
		var out bytes.Buffer
		printSensitiveOverrides(&Printer{Out: &out, Err: io.Discard}, overrides, true)
		return out.String()
	}

	if got := render(nil); got != "" {
		t.Fatalf("zero overrides rendered %q", got)
	}

	one := render([]syncer.SensitiveOverride{{AllowPattern: "/.secrets/app.env", DenyPattern: "/.secrets/**"}})
	for _, want := range []string{
		"Sensitive overrides",
		"1 allow rule(s) override built-in secret exclusions",
		"/.secrets/app.env re-includes /.secrets/**",
	} {
		if !strings.Contains(one, want) {
			t.Fatalf("one override output missing %q:\n%s", want, one)
		}
	}

	many := render([]syncer.SensitiveOverride{
		{AllowPattern: "/.aws/credentials", DenyPattern: "/.aws/credentials"},
		{AllowPattern: "/.secrets/app.env", DenyPattern: "/.secrets/**"},
	})
	if !strings.Contains(many, "2 allow rule(s) override built-in secret exclusions") {
		t.Fatalf("many override count missing:\n%s", many)
	}
	if strings.Index(many, "/.aws/credentials re-includes /.aws/credentials") > strings.Index(many, "/.secrets/app.env re-includes /.secrets/**") {
		t.Fatalf("many overrides lost their sorted order:\n%s", many)
	}
}

func TestSyncStatusSensitiveOverridesOrderingAndConstructionFailure(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: io.Discard}
	p.KV("Secrets", "deny-by-default (allow.txt empty)")
	printSensitiveOverrides(p, nil, true)
	p.KV("Include file", "include.txt")
	got := out.String()
	if strings.Contains(got, "Sensitive overrides") {
		t.Fatalf("empty or failed collection must not render a partial warning:\n%s", got)
	}
	if strings.Index(got, "Secrets") > strings.Index(got, "Include file") {
		t.Fatalf("status adjacency changed:\n%s", got)
	}
}

func TestSyncSensitiveOverridesLongControlEscaping(t *testing.T) {
	long := "/.secrets/" + strings.Repeat("long-pattern-", 64) + "\x1b[31m\napp.env"
	deny := "/.secrets/**\t"
	var out bytes.Buffer
	printSensitiveOverrides(&Printer{Out: &out, Err: io.Discard}, []syncer.SensitiveOverride{{
		AllowPattern: long,
		DenyPattern:  deny,
	}}, true)
	got := out.String()
	for _, want := range []string{"long-pattern-", "\\x1b[31m", "\\napp.env", "\\t"} {
		if !strings.Contains(got, want) {
			t.Fatalf("escaped output missing %q:\n%s", want, got)
		}
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	bullet := lines[len(lines)-1]
	if strings.ContainsAny(bullet, "\x1b\n\t") {
		t.Fatalf("escaped output contains a raw terminal control byte: %q", bullet)
	}
	if strings.Contains(got, "...") {
		t.Fatalf("long output was truncated: %q", got)
	}
	if len(lines) != 2 {
		t.Fatalf("one override must occupy one logical line after its status KV, got %d lines:\n%s", len(lines), got)
	}
}

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
