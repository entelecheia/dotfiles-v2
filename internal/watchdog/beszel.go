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
// the env path. listen lands as the LISTEN fallback in the shell line:
// `export LISTEN="${LISTEN:-<listen>}"`, so an env-file LISTEN wins and the
// resolved config value (default :45876) only applies when the env file
// leaves it unset (precedence: env file > config).
//
// Every interpolated value is validated against validPlistPath (no double
// quote, no line break — the same rule monit's renderer enforces) and then
// XML-escaped; a path or listen value that fails validation is an error,
// never a corrupted unit.
func RenderBeszelPlist(agentPath, envPath, listen, logDir string) (string, error) {
	for name, path := range map[string]string{"agent path": agentPath, "env path": envPath, "log dir": logDir, "listen": listen} {
		if err := validPlistPath(path); err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
	}
	script := fmt.Sprintf(`set -a; . "%s"; set +a; export LISTEN="${LISTEN:-%s}"; exec "%s"`,
		xmlEscape(envPath), xmlEscape(listen), xmlEscape(agentPath))
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
    <string>%s</string>
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
`, BeszelLabel, script, xmlEscape(logDir), xmlEscape(logDir)), nil
}

// InstallBeszel writes the agent plist (0644) and (re)loads the
// LaunchAgent. Idempotent like Install: any pre-loaded copy is unloaded
// first so a binary- or env-path change takes effect on rerun. The env file
// is NOT written here — it is secret material restored by `dot secrets`.
// listen is the resolved watchdog.beszel.listen value (see RenderBeszelPlist
// for the env-file-wins precedence).
func (m *Manager) InstallBeszel(ctx context.Context, agentPath, envPath, listen string) error {
	if m.goos() != "darwin" {
		return ErrNeedsDarwin
	}
	plist, err := RenderBeszelPlist(agentPath, m.BeszelEnvRef(envPath), listen, m.LogDir())
	if err != nil {
		return err
	}
	if err := m.Runner.MkdirAll(filepath.Dir(m.BeszelPlistPath()), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(m.BeszelPlistPath()), err)
	}
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
