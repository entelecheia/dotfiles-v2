package watchdog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Monit service-health supervision (phase P3): monitrc rendering, the
// Manager-owned paths for the user LaunchAgent, and the root-owned Screen
// Sharing heal helper plus its sudoers entry.

const (
	// ScreenSharingHealPath is the root-owned helper monit execs through
	// passwordless sudo when the VNC port stops answering. Root-owned so the
	// user (and any process running as the user) cannot edit what sudo runs.
	ScreenSharingHealPath = "/Library/Application Support/dot/screensharing-heal"
	// ScreenSharingSudoersPath grants the installing user passwordless exec
	// of exactly the helper above — never an editor, never a wildcard.
	ScreenSharingSudoersPath = "/etc/sudoers.d/dot-watchdog-screensharing"
	// screenSharingService is the launchd service the helper kickstarts.
	screenSharingService = "system/com.apple.screensharing"
)

// MonitrcPath is where the rendered monit configuration lives. monit refuses
// a control file any group/other can write, so InstallMonit writes it 0600.
func (m *Manager) MonitrcPath() string {
	return filepath.Join(m.Home, ".config", "monit", "monitrc")
}

// MonitPlistPath is where the user LaunchAgent running monit lives.
func (m *Manager) MonitPlistPath() string {
	return filepath.Join(m.Home, "Library", "LaunchAgents", MonitLabel+".plist")
}

// MonitLogPath is monit's own log (separate from the watchdog JSON-lines log).
func (m *Manager) MonitLogPath() string {
	return filepath.Join(m.LogDir(), "monit.log")
}

// RenderMonitrc renders the monit control file: system load/CPU checks that
// fan out to `dot watchdog notify`, and — when ScreenSharing is on — a host
// check that heals Screen Sharing after cycles consecutive VNC handshake
// failures. No `set httpd`: the web UI stays off. The daemon cycle is 60s,
// so the default 3 cycles means ~3 minutes of sustained breach before acting.
func RenderMonitrc(dotPath, logPath string, s MonitSettings) string {
	load := strconv.FormatFloat(s.Load1Threshold, 'f', -1, 64)
	cpuUser := strconv.FormatFloat(s.CPUUserThreshold, 'f', -1, 64)
	cpuSystem := strconv.FormatFloat(s.CPUSystemThreshold, 'f', -1, 64)
	notify := func(msg string) string {
		return fmt.Sprintf("exec \"%s watchdog notify warn '%s'\"", dotPath, msg)
	}
	var b strings.Builder
	b.WriteString("# dot watchdog monit control file, rendered by `dot watchdog setup`.\n")
	b.WriteString("# Edit watchdog.monit in the active profile and rerun setup to change it.\n")
	b.WriteString("set daemon 60\n")
	fmt.Fprintf(&b, "set logfile %s\n", logPath)
	b.WriteString("\ncheck system $HOST\n")
	fmt.Fprintf(&b, "  if loadavg(5min) > %s for %d cycles then %s\n",
		load, s.Cycles, notify(fmt.Sprintf("monit: loadavg(5min) above %s for %d cycles", load, s.Cycles)))
	fmt.Fprintf(&b, "  if cpu(user) > %s%% for %d cycles then %s\n",
		cpuUser, s.Cycles, notify(fmt.Sprintf("monit: cpu(user) above %s%% for %d cycles", cpuUser, s.Cycles)))
	fmt.Fprintf(&b, "  if cpu(system) > %s%% for %d cycles then %s\n",
		cpuSystem, s.Cycles, notify(fmt.Sprintf("monit: cpu(system) above %s%% for %d cycles", cpuSystem, s.Cycles)))
	if s.ScreenSharing {
		// The RFB handshake is server-speaks-first, so the empty SEND is
		// correct: any answer not starting with "RFB" means Screen Sharing
		// (or something squatting on 5900) is unhealthy. The helper path has
		// a space; the inner single quotes are for the shell monit execs
		// through, not for monit itself.
		fmt.Fprintf(&b, "\ncheck host screensharing with address 127.0.0.1\n"+
			"  if failed port 5900 send \"\" expect \"RFB\" for %d cycles then exec \"/usr/bin/sudo -n '%s'\"\n",
			s.Cycles, ScreenSharingHealPath)
	}
	return b.String()
}

// RenderScreenSharingHeal renders the root-owned helper: kickstart the
// Screen Sharing daemon. Kept to one action so the passwordless sudo grant
// can be this exact path and nothing else.
func RenderScreenSharingHeal() string {
	return "#!/bin/sh\n" +
		"# Installed root-owned by `dot watchdog setup`; monit runs it through\n" +
		"# passwordless sudo when Screen Sharing stops answering the VNC handshake.\n" +
		"exec /bin/launchctl kickstart -k " + screenSharingService + "\n"
}

// RenderScreenSharingSudoers renders the single-grant sudoers drop-in. The
// space in the helper path is backslash-escaped, which is how sudoers
// expresses a literal space in a command spec; `visudo -c -f` validates the
// file before it goes live.
func RenderScreenSharingSudoers(user string) string {
	return fmt.Sprintf("%s ALL=(root) NOPASSWD: %s\n", user, strings.ReplaceAll(ScreenSharingHealPath, " ", `\ `))
}

// InstallMonit writes the monitrc and the user LaunchAgent, then (re)loads
// the agent. Idempotent: any pre-loaded copy is unloaded first so a config
// change takes effect on rerun. monitPath/dotPath resolution is the caller's
// job; the monitrc is written 0600 because monit refuses a group/world-
// writable control file.
func (m *Manager) InstallMonit(ctx context.Context, monitPath, monitrc string) error {
	if m.goos() != "darwin" {
		return ErrNeedsDarwin
	}
	for _, dir := range []string{filepath.Dir(m.MonitrcPath()), filepath.Dir(m.MonitPlistPath()), m.LogDir()} {
		if err := m.Runner.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := m.Runner.WriteFileAtomic(m.MonitrcPath(), []byte(monitrc), 0o600); err != nil {
		return fmt.Errorf("writing monitrc: %w", err)
	}
	plist := RenderMonitPlist(monitPath, m.MonitrcPath(), m.LogDir())
	if err := m.Runner.WriteFileAtomic(m.MonitPlistPath(), []byte(plist), 0o644); err != nil {
		return fmt.Errorf("writing monit plist: %w", err)
	}
	_, _ = m.Runner.Run(ctx, "launchctl", "unload", m.MonitPlistPath())
	if _, err := m.Runner.Run(ctx, "launchctl", "load", m.MonitPlistPath()); err != nil {
		return fmt.Errorf("loading monit plist: %w", err)
	}
	return nil
}

// UninstallMonit unloads the agent and removes the plist and monitrc.
// Missing files are not an error. The monit log is deliberately kept; the
// CLI asks about logs separately.
func (m *Manager) UninstallMonit(ctx context.Context) error {
	if m.goos() != "darwin" {
		return ErrNeedsDarwin
	}
	_, _ = m.Runner.Run(ctx, "launchctl", "unload", m.MonitPlistPath())
	for _, path := range []string{m.MonitPlistPath(), m.MonitrcPath()} {
		if !m.Runner.FileExists(path) {
			continue
		}
		if err := m.Runner.Remove(path); err != nil {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}
	return nil
}

// ProbeMonit reports whether the plist exists and the agent is loaded. On
// non-darwin systems the probe is plist-only.
func (m *Manager) ProbeMonit(ctx context.Context) Status {
	st := Status{PlistExists: m.Runner.FileExists(m.MonitPlistPath())}
	if !st.PlistExists || m.goos() != "darwin" {
		return st
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), MonitLabel)
	result, err := m.Runner.RunQuery(ctx, "launchctl", "print", target)
	st.Loaded = err == nil && result != nil && result.ExitCode == 0
	return st
}

// InstallScreenSharingHeal installs the root-owned heal helper and the
// sudoers drop-in that lets the installing user run exactly it without a
// password. The sudoers content is validated with `visudo -c -f` before it
// goes live: a bad drop-in can break sudo for the whole host, so a failed
// check refuses the install. The caller primes sudo first.
func (m *Manager) InstallScreenSharingHeal(ctx context.Context, user string) error {
	if m.goos() != "darwin" {
		return ErrNeedsDarwin
	}
	if err := sudoInstallContent(ctx, m.Runner, []byte(RenderScreenSharingHeal()), ScreenSharingHealPath, 0o755); err != nil {
		return err
	}
	return m.sudoInstallSudoers(ctx, []byte(RenderScreenSharingSudoers(user)), ScreenSharingSudoersPath)
}

// sudoInstallSudoers stages the drop-in, validates it with visudo, then
// installs it root:wheel 0440 (the mode sudo requires for sudoers.d files).
func (m *Manager) sudoInstallSudoers(ctx context.Context, content []byte, dest string) error {
	tmp, err := os.CreateTemp("", "dot-watchdog-sudoers-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if _, err := m.Runner.Run(ctx, "sudo", "visudo", "-c", "-f", tmpPath); err != nil {
		return fmt.Errorf("sudoers content failed `visudo -c -f`; refusing to install %s: %w", dest, err)
	}
	if _, err := m.Runner.Run(ctx, "sudo", "install", "-m", "0440", "-o", "root", "-g", "wheel", tmpPath, dest); err != nil {
		return fmt.Errorf("installing %s: %w", dest, err)
	}
	return nil
}

// UninstallScreenSharingHeal removes the helper and the sudoers drop-in.
// Missing files are not an error. The caller primes sudo first.
func (m *Manager) UninstallScreenSharingHeal(ctx context.Context) error {
	if m.goos() != "darwin" {
		return ErrNeedsDarwin
	}
	for _, path := range []string{ScreenSharingSudoersPath, ScreenSharingHealPath} {
		if _, err := m.Runner.Run(ctx, "sudo", "rm", "-f", path); err != nil {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}
	return nil
}
