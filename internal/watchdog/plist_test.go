package watchdog

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func TestRenderReapPlist_Golden(t *testing.T) {
	got := RenderReapPlist("/Users/test/.local/bin/dot", 300*time.Second, "/Users/test/Library/Logs/dot")
	golden := filepath.Join("testdata", "reap.plist.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("plist drifted from golden:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderReapPlist_CarriesTheScheduledMarker(t *testing.T) {
	plist := RenderReapPlist("/usr/local/bin/dot", 60*time.Second, "/tmp/logs")
	if !strings.Contains(plist, "<key>"+ScheduledRunEnv+"</key>") {
		t.Fatalf("plist lost the scheduled-run marker %s", ScheduledRunEnv)
	}
	if !strings.Contains(plist, "<integer>60</integer>") {
		t.Fatal("plist lost the interval")
	}
	if !strings.Contains(plist, ReapLabel) {
		t.Fatal("plist lost the label")
	}
}

func TestManager_Paths(t *testing.T) {
	m := NewManager(nil, "/home/u")
	if got := m.PlistPath(); got != "/home/u/Library/LaunchAgents/com.dotfiles.watchdog.reap.plist" {
		t.Errorf("PlistPath = %q", got)
	}
	if got := m.SnapshotPath(); got != "/home/u/.local/state/dot/watchdog/watchdog.yaml" {
		t.Errorf("SnapshotPath = %q", got)
	}
	if got := m.SamplesPath(); got != "/home/u/.local/state/dot/watchdog/samples.json" {
		t.Errorf("SamplesPath = %q", got)
	}
	if got := m.LogPath(); got != "/home/u/Library/Logs/dot/watchdog.log" {
		t.Errorf("LogPath = %q", got)
	}
}

func linuxManager(home string) *Manager {
	m := NewManager(exec.NewRunner(false, slog.Default()), home)
	m.GOOS = "linux"
	return m
}

func TestManager_InstallRefusedOffDarwin(t *testing.T) {
	m := linuxManager(t.TempDir())
	err := m.Install(context.Background(), "/usr/local/bin/dot", 300*time.Second, []byte("enabled: true\n"))
	if !errors.Is(err, ErrNeedsDarwin) {
		t.Fatalf("Install off-darwin = %v, want ErrNeedsDarwin", err)
	}
	// And nothing was written: the refusal must precede any mutation.
	if _, statErr := os.Stat(m.PlistPath()); !os.IsNotExist(statErr) {
		t.Fatalf("plist exists after a refused install: %v", statErr)
	}
}

func TestManager_UninstallRefusedOffDarwin(t *testing.T) {
	m := linuxManager(t.TempDir())
	if err := m.Uninstall(context.Background()); !errors.Is(err, ErrNeedsDarwin) {
		t.Fatalf("Uninstall off-darwin = %v, want ErrNeedsDarwin", err)
	}
}

func TestManager_ProbeOffDarwinIsPlistOnly(t *testing.T) {
	m := linuxManager(t.TempDir())
	if st := m.Probe(context.Background()); st.PlistExists || st.Loaded {
		t.Fatalf("empty home probe = %#v", st)
	}
	// A plist dropped on a linux box still reports, but never "loaded":
	// the launchctl print must not even be attempted.
	dir := filepath.Dir(m.PlistPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.PlistPath(), []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := m.Probe(context.Background())
	if !st.PlistExists || st.Loaded {
		t.Fatalf("probe = %#v, want {true false}", st)
	}
}
