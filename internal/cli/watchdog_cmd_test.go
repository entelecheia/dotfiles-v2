package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// seedWatchdogSnapshot writes the resolved config the setup would have
// written, at the location the scheduled reaper reads.
func seedWatchdogSnapshot(t *testing.T, home, body string) {
	t.Helper()
	writeCLITestFile(t, filepath.Join(home, ".local", "state", "dot", "watchdog", "watchdog.yaml"), body)
}

// TestWatchdogReap_EndToEndDryRun runs a real pass against the live process
// table: the test binary itself is the (only possible) match, and dry-run
// mode plus a first sighting guarantee no candidate and no kill.
func TestWatchdogReap_EndToEndDryRun(t *testing.T) {
	home := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	seedWatchdogSnapshot(t, home, fmt.Sprintf(`
enabled: true
reaper:
  mode: dry-run
  cpu_threshold: 99
  match:
    orphan_only: false
    paths: ["%s"]
`, exe))

	if _, _, err := runDotForTest("--home", home, "watchdog", "reap"); err != nil {
		t.Fatalf("reap: %v", err)
	}

	samplesPath := filepath.Join(home, ".local", "state", "dot", "watchdog", "samples.json")
	data, err := os.ReadFile(samplesPath)
	if err != nil {
		t.Fatalf("samples.json not persisted: %v", err)
	}
	// A first sighting is never a candidate, so nothing may be logged as killed.
	if strings.Contains(string(data), "over_since") {
		logPath := filepath.Join(home, "Library", "Logs", "dot", "watchdog.log")
		if logData, err := os.ReadFile(logPath); err == nil && strings.Contains(string(logData), `"event":"reap"`) {
			t.Fatalf("dry-run pass logged a kill:\n%s", logData)
		}
	}
}

func TestWatchdogReap_ScheduledRunStaysQuiet(t *testing.T) {
	home := t.TempDir()
	seedWatchdogSnapshot(t, home, "enabled: true\nreaper:\n  mode: dry-run\n")
	t.Setenv("DOT_WATCHDOG_RUN", "1")
	out, _, err := runDotForTest("--home", home, "watchdog", "reap")
	if err != nil {
		t.Fatalf("scheduled reap: %v", err)
	}
	if out != "" {
		t.Fatalf("a scheduled run wrote to stdout: %q", out)
	}
}

func TestWatchdogReap_MissingSnapshotIsActionable(t *testing.T) {
	_, _, err := runDotForTest("--home", t.TempDir(), "watchdog", "reap")
	if err == nil || !strings.Contains(err.Error(), "dot watchdog setup") {
		t.Fatalf("missing snapshot error must point at setup, got: %v", err)
	}
}

func TestWatchdogStatus_ReportsSnapshotAndDefaults(t *testing.T) {
	home := t.TempDir()
	out, _, err := runDotForTest("--home", home, "watchdog", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "no snapshot") {
		t.Fatalf("status must report the missing snapshot: %q", out)
	}

	seedWatchdogSnapshot(t, home, "enabled: true\nreaper:\n  mode: enforce\n")
	out, _, err = runDotForTest("--home", home, "watchdog", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"enforce", "50% sustained 30m0s", "Tracked processes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
}

func TestWatchdogLog_MissingAndSeeded(t *testing.T) {
	home := t.TempDir()
	out, _, err := runDotForTest("--home", home, "watchdog", "log")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(out, "No log file found") {
		t.Fatalf("missing log must say so: %q", out)
	}

	logPath := filepath.Join(home, "Library", "Logs", "dot", "watchdog.log")
	writeCLITestFile(t, logPath,
		"{\"ts\":\"2026-09-26T00:00:00Z\",\"level\":\"warn\",\"event\":\"candidate\",\"pid\":1}\n"+
			"{\"ts\":\"2026-09-26T00:05:00Z\",\"level\":\"warn\",\"event\":\"candidate\",\"pid\":2}\n"+
			"{\"ts\":\"2026-09-26T00:10:00Z\",\"level\":\"critical\",\"event\":\"reap\",\"pid\":3}\n")
	out, _, err = runDotForTest("--home", home, "watchdog", "log", "2")
	if err != nil {
		t.Fatalf("log 2: %v", err)
	}
	if strings.Contains(out, `"pid":1`) || !strings.Contains(out, `"pid":2`) || !strings.Contains(out, `"pid":3`) {
		t.Fatalf("log 2 must tail the newest two lines:\n%s", out)
	}

	if _, _, err := runDotForTest("--home", home, "watchdog", "log", "nope"); err == nil {
		t.Fatal("a non-numeric line count must fail")
	}
}

func TestWatchdogNotify_NoChannelsIsNoOp(t *testing.T) {
	home := t.TempDir()
	seedWatchdogSnapshot(t, home, "enabled: true\n")
	out, _, err := runDotForTest("--home", home, "watchdog", "notify", "info", "hello")
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !strings.Contains(out, "notification sent") {
		t.Fatalf("notify output = %q", out)
	}
}

// watchdogFlagCmd builds a bare command carrying the persistent flags the
// watchdog runners read, so the goos-injected entry points can be exercised
// directly on either platform.
func watchdogFlagCmd(t *testing.T, args []string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().Bool("yes", false, "")
	cmd.Flags().Bool("dry-run", false, "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("config", "", "")
	cmd.Flags().String("home", "", "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestWatchdogSetup_LinuxGate(t *testing.T) {
	err := runWatchdogSetupForGOOS(watchdogFlagCmd(t, nil), nil, "linux")
	if err == nil || !strings.Contains(err.Error(), "macOS-only") {
		t.Fatalf("linux setup = %v, want a macOS-only refusal", err)
	}
}

func TestWatchdogUninstall_LinuxGate(t *testing.T) {
	err := runWatchdogUninstallForGOOS(watchdogFlagCmd(t, nil), nil, "linux")
	if err == nil || !strings.Contains(err.Error(), "macOS-only") {
		t.Fatalf("linux uninstall = %v, want a macOS-only refusal", err)
	}
}

func TestWatchdogSetup_DisabledProfileRefuses(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cfg.yaml")
	writeCLITestFile(t, cfgPath, "watchdog:\n  enabled: false\n")
	cmd := watchdogFlagCmd(t, []string{"--config", cfgPath, "--yes"})
	err := runWatchdogSetupForGOOS(cmd, nil, "darwin")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled watchdog must refuse setup: %v", err)
	}
}

func TestWatchdogSetup_DryRunExplains(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cfg.yaml")
	writeCLITestFile(t, cfgPath, "watchdog:\n  enabled: true\n  reaper:\n    mode: dry-run\n")
	cmd := watchdogFlagCmd(t, []string{"--config", cfgPath, "--yes", "--dry-run"})
	var out strings.Builder
	cmd.SetOut(&out)
	if err := runWatchdogSetupForGOOS(cmd, nil, "darwin"); err != nil {
		t.Fatalf("dry-run setup: %v", err)
	}
	if !strings.Contains(out.String(), "[dry-run] would install and load") {
		t.Fatalf("dry-run setup output = %q", out.String())
	}
}
