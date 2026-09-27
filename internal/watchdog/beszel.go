package watchdog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BeszelLabel is the launchd label of the user LaunchAgent that runs the
// Beszel external-monitoring agent (phase P4).
const BeszelLabel = "com.dotfiles.beszel-agent"

// BeszelEnvPath is the default plaintext env file the agent's plist sources
// (HUB_URL/KEY/TOKEN/LISTEN). The file is secrets-managed (the beszel pair
// in internal/secrets Entries): dot probes its presence and never reads,
// renders, or removes it.
func (m *Manager) BeszelEnvPath() string {
	return filepath.Join(m.Home, ".config", "beszel", "agent.env")
}

// BeszelPlistPath is where the Beszel agent LaunchAgent lives.
func (m *Manager) BeszelPlistPath() string {
	return filepath.Join(m.Home, "Library", "LaunchAgents", BeszelLabel+".plist")
}

// BeszelEnvRef renders envPath for the plist's shell line: $HOME-relative
// when the path lives under the manager home (so the unit survives a
// home-dir rename), absolute otherwise.
func (m *Manager) BeszelEnvRef(envPath string) string {
	home := filepath.Clean(m.Home)
	clean := filepath.Clean(envPath)
	if clean == home {
		return "$HOME"
	}
	if strings.HasPrefix(clean, home+string(os.PathSeparator)) {
		return "$HOME" + clean[len(home):]
	}
	return clean
}

// RenderBeszelPlist renders the user LaunchAgent for the Beszel agent.
//
// This is deliberately a dot-owned plist rather than the issue's `brew
// services` unit: only a plist dot owns can inject the secrets-managed env
// file and be fully removed by `dot watchdog uninstall` inside the
// documented boundaries. envPath and agentPath are rendered as given — the
// caller resolves the agent binary (brew prefix) and passes BeszelEnvRef for
// the env path. Both are operator-owned local paths; like the reaper/warp
// units they are interpolated unescaped.
func RenderBeszelPlist(agentPath, envPath, logDir string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>set -a; . "%s"; set +a; exec "%s"</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s/beszel.out.log</string>
  <key>StandardErrorPath</key>
  <string>%s/beszel.err.log</string>
</dict>
</plist>
`, BeszelLabel, envPath, agentPath, logDir, logDir)
}

// InstallBeszel writes the agent plist (0644) and (re)loads the
// LaunchAgent. Idempotent like Install: any pre-loaded copy is unloaded
// first so a binary- or env-path change takes effect on rerun. The env file
// is NOT written here — it is secret material restored by `dot secrets`.
func (m *Manager) InstallBeszel(ctx context.Context, agentPath, envPath string) error {
	if m.goos() != "darwin" {
		return ErrNeedsDarwin
	}
	if err := m.Runner.MkdirAll(filepath.Dir(m.BeszelPlistPath()), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(m.BeszelPlistPath()), err)
	}
	plist := RenderBeszelPlist(agentPath, m.BeszelEnvRef(envPath), m.LogDir())
	if err := m.Runner.WriteFileAtomic(m.BeszelPlistPath(), []byte(plist), 0o644); err != nil {
		return fmt.Errorf("writing beszel plist: %w", err)
	}
	_, _ = m.Runner.Run(ctx, "launchctl", "unload", m.BeszelPlistPath())
	if _, err := m.Runner.Run(ctx, "launchctl", "load", m.BeszelPlistPath()); err != nil {
		return fmt.Errorf("loading beszel plist: %w", err)
	}
	return nil
}

// UninstallBeszel unloads the LaunchAgent and removes the plist — and only
// the plist. The env file is deliberately kept: it is secrets-managed
// material whose lifecycle belongs to `dot secrets`. Missing files are not
// an error.
func (m *Manager) UninstallBeszel(ctx context.Context) error {
	if m.goos() != "darwin" {
		return ErrNeedsDarwin
	}
	_, _ = m.Runner.Run(ctx, "launchctl", "unload", m.BeszelPlistPath())
	if !m.Runner.FileExists(m.BeszelPlistPath()) {
		return nil
	}
	if err := m.Runner.Remove(m.BeszelPlistPath()); err != nil {
		return fmt.Errorf("removing beszel plist: %w", err)
	}
	return nil
}

// ProbeBeszel reports whether the agent plist exists and the unit is loaded.
// On non-darwin systems the probe is plist-only, mirroring Probe.
func (m *Manager) ProbeBeszel(ctx context.Context) Status {
	st := Status{PlistExists: m.Runner.FileExists(m.BeszelPlistPath())}
	if !st.PlistExists || m.goos() != "darwin" {
		return st
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), BeszelLabel)
	result, err := m.Runner.RunQuery(ctx, "launchctl", "print", target)
	st.Loaded = err == nil && result != nil && result.ExitCode == 0
	return st
}
