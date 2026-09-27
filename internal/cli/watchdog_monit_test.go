package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/watchdog"
)

// P3 CLI surface: setup dry-run coverage, status monitrc drift, and
// uninstall dry-run coverage, all through the goos-injected entry points.

func TestWatchdogSetup_DryRunCoversMonit(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cfg.yaml")
	writeCLITestFile(t, cfgPath, "watchdog:\n  enabled: true\n  monit:\n    enabled: true\n")
	cmd := watchdogFlagCmd(t, []string{"--config", cfgPath, "--yes", "--dry-run"})
	var out strings.Builder
	cmd.SetOut(&out)
	if err := runWatchdogSetupForGOOS(cmd, nil, "darwin"); err != nil {
		t.Fatalf("dry-run setup: %v", err)
	}
	for _, want := range []string{
		"Monit",
		"[dry-run] would render",
		"monitrc",
		"com.dotfiles.monit.plist",
		watchdog.ScreenSharingHealPath,
		watchdog.ScreenSharingSudoersPath,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run setup missing %q:\n%s", want, out.String())
		}
	}
}

// An explicit screensharing: false must keep the root helper out of even the
// dry-run plan.
func TestWatchdogSetup_DryRunSkipsHealWhenScreenSharingDisabled(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cfg.yaml")
	writeCLITestFile(t, cfgPath, "watchdog:\n  enabled: true\n  monit:\n    enabled: true\n    screensharing: false\n")
	cmd := watchdogFlagCmd(t, []string{"--config", cfgPath, "--yes", "--dry-run"})
	var out strings.Builder
	cmd.SetOut(&out)
	if err := runWatchdogSetupForGOOS(cmd, nil, "darwin"); err != nil {
		t.Fatalf("dry-run setup: %v", err)
	}
	if strings.Contains(out.String(), watchdog.ScreenSharingHealPath) {
		t.Fatalf("screensharing: false still planned the heal helper:\n%s", out.String())
	}
}

func TestWatchdogSetup_InvalidMonitConfigFails(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cfg.yaml")
	writeCLITestFile(t, cfgPath, "watchdog:\n  enabled: true\n  monit:\n    enabled: true\n    cycles: 100\n")
	cmd := watchdogFlagCmd(t, []string{"--config", cfgPath, "--yes", "--dry-run"})
	err := runWatchdogSetupForGOOS(cmd, nil, "darwin")
	if err == nil || !strings.Contains(err.Error(), "cycles") {
		t.Fatalf("cycles above monit's limit must fail setup, got: %v", err)
	}
}

// stubDotOnPATH puts a fake `dot` first on PATH so the drift check resolves
// the same binary path deterministically on any platform.
func stubDotOnPATH(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	dot := filepath.Join(bin, "dot")
	if err := os.WriteFile(dot, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dot
}

func TestMonitrcStatus_DriftAndSync(t *testing.T) {
	home := t.TempDir()
	dot := stubDotOnPATH(t)
	mgr := watchdog.NewManager(watchdogRunner(false), home)
	wcfg := &config.WatchdogConfig{Monit: config.WatchdogMonitConfig{Enabled: true}}

	if got := monitrcStatus(mgr, wcfg); got != "missing" {
		t.Fatalf("no monitrc = %q, want missing", got)
	}
	writeCLITestFile(t, mgr.MonitrcPath(), "set daemon 30\n")
	if got := monitrcStatus(mgr, wcfg); !strings.Contains(got, "DRIFTED") {
		t.Fatalf("stale monitrc = %q, want DRIFTED", got)
	}
	settings, err := watchdog.ResolveMonit(wcfg.Monit, runtime.NumCPU())
	if err != nil {
		t.Fatalf("ResolveMonit: %v", err)
	}
	writeCLITestFile(t, mgr.MonitrcPath(), watchdog.RenderMonitrc(dot, mgr.MonitLogPath(), settings))
	if got := monitrcStatus(mgr, wcfg); got != "in sync" {
		t.Fatalf("freshly rendered monitrc = %q, want in sync", got)
	}
	// No snapshot config to compare against: present, never a false drift.
	wcfg.Monit.Enabled = false
	if got := monitrcStatus(mgr, wcfg); got != "present" {
		t.Fatalf("monit disabled in snapshot = %q, want present", got)
	}
}

func TestWatchdogStatus_P3Rows(t *testing.T) {
	home := t.TempDir()
	seedWatchdogSnapshot(t, home, "enabled: true\nmonit:\n  enabled: true\n")
	out, _, err := runDotForTest("--home", home, "watchdog", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"Monit"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q row:\n%s", want, out)
		}
	}
}

func TestWatchdogUninstall_DryRunCoversMonit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCLITestFile(t, filepath.Join(home, "Library", "LaunchAgents", "com.dotfiles.monit.plist"), "<plist/>")
	cmd := watchdogFlagCmd(t, []string{"--dry-run"})
	var out strings.Builder
	cmd.SetOut(&out)
	if err := runWatchdogUninstallForGOOS(cmd, nil, "darwin"); err != nil {
		t.Fatalf("dry-run uninstall: %v", err)
	}
	if !strings.Contains(out.String(), "com.dotfiles.monit.plist") || !strings.Contains(out.String(), "monitrc") {
		t.Fatalf("dry-run uninstall must cover the monit files:\n%s", out.String())
	}
}
