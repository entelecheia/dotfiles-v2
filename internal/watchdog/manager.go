package watchdog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Manager owns the on-disk locations and launchd lifecycle of the watchdog.
// Everything macOS-specific is gated on GOOS at runtime (never build tags),
// so the package compiles and its pure logic stays testable on Linux.
type Manager struct {
	Runner *exec.Runner
	Home   string
	GOOS   string // empty means runtime.GOOS
}

// NewManager builds a Manager for the given home on the current platform.
func NewManager(runner *exec.Runner, home string) *Manager {
	return &Manager{Runner: runner, Home: home}
}

func (m *Manager) goos() string {
	if m.GOOS != "" {
		return m.GOOS
	}
	return runtime.GOOS
}

// PlistPath is where the reaper LaunchAgent lives.
func (m *Manager) PlistPath() string {
	return filepath.Join(m.Home, "Library", "LaunchAgents", ReapLabel+".plist")
}

// StateDir holds the resolved config snapshot and the sample history.
func (m *Manager) StateDir() string {
	return filepath.Join(m.Home, ".local", "state", "dot", "watchdog")
}

// SnapshotPath is the resolved watchdog config the scheduled reaper reads.
func (m *Manager) SnapshotPath() string {
	return filepath.Join(m.StateDir(), "watchdog.yaml")
}

// SamplesPath is the cross-run CPU history.
func (m *Manager) SamplesPath() string {
	return filepath.Join(m.StateDir(), "samples.json")
}

// LogDir holds the JSON-lines watchdog log and the launchd stdout/stderr logs.
func (m *Manager) LogDir() string {
	return filepath.Join(m.Home, "Library", "Logs", "dot")
}

// LogPath is the JSON-lines event log `dot watchdog log` tails.
func (m *Manager) LogPath() string {
	return filepath.Join(m.LogDir(), "watchdog.log")
}

// ErrNeedsDarwin gates launchd operations on non-macOS builds at runtime.
var ErrNeedsDarwin = errors.New("the watchdog LaunchAgent is macOS-only")

// Install writes the config snapshot and the reaper plist, then (re)loads
// the LaunchAgent. Idempotent: any pre-loaded copy is unloaded first so an
// interval change takes effect on rerun.
func (m *Manager) Install(ctx context.Context, dotPath string, interval time.Duration, snapshot []byte) error {
	if m.goos() != "darwin" {
		return ErrNeedsDarwin
	}
	for _, dir := range []string{filepath.Dir(m.PlistPath()), m.StateDir(), m.LogDir()} {
		if err := m.Runner.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := m.Runner.WriteFileAtomic(m.SnapshotPath(), snapshot, 0o644); err != nil {
		return fmt.Errorf("writing config snapshot: %w", err)
	}
	plist := RenderReapPlist(dotPath, interval, m.LogDir())
	if err := m.Runner.WriteFileAtomic(m.PlistPath(), []byte(plist), 0o644); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}
	_, _ = m.Runner.Run(ctx, "launchctl", "unload", m.PlistPath())
	if _, err := m.Runner.Run(ctx, "launchctl", "load", m.PlistPath()); err != nil {
		return fmt.Errorf("loading plist: %w", err)
	}
	return nil
}

// Uninstall unloads the LaunchAgent and removes the plist. Missing files
// are not an error. The state dir and log are deliberately kept; the CLI
// asks about those separately.
func (m *Manager) Uninstall(ctx context.Context) error {
	if m.goos() != "darwin" {
		return ErrNeedsDarwin
	}
	_, _ = m.Runner.Run(ctx, "launchctl", "unload", m.PlistPath())
	if !m.Runner.FileExists(m.PlistPath()) {
		return nil
	}
	if err := m.Runner.Remove(m.PlistPath()); err != nil {
		return fmt.Errorf("removing plist: %w", err)
	}
	return nil
}

// Status is the launchd-side view of the reaper unit.
type Status struct {
	PlistExists bool
	Loaded      bool
}

// Probe reports whether the plist exists and the unit is loaded. On
// non-darwin systems the probe is plist-only.
func (m *Manager) Probe(ctx context.Context) Status {
	st := Status{PlistExists: m.Runner.FileExists(m.PlistPath())}
	if !st.PlistExists || m.goos() != "darwin" {
		return st
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), ReapLabel)
	result, err := m.Runner.RunQuery(ctx, "launchctl", "print", target)
	st.Loaded = err == nil && result != nil && result.ExitCode == 0
	return st
}
