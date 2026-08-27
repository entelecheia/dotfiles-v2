package syncer

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// CheckRsync checks if rsync is installed and returns its version.
// RemoteRsyncPath resolves an rsync on the peer that can actually serve a
// modern client, and returns the value for --rsync-path (empty means "the
// default is fine").
//
// ponytail: known ceiling. See docs/CEILINGS.md (preview protocol limit).
// macOS 26 replaced rsync with openrsync, which reports "protocol version 29 /
// rsync 2.6.9 compatible" and cannot receive -aHAX from a 3.x client: the real
// transfer dies with "error in rsync protocol data stream (code 12)". A
// --dry-run does NOT surface this, because it never ships file data - so this
// has to be probed before the transfer, not discovered during it.
//
// Two traps this navigates. A non-interactive ssh shell does not source the
// Homebrew shellenv, so bare `rsync` resolves to /usr/bin/rsync even on a
// machine that has a proper 3.x installed two directories away. And the probe
// must therefore try absolute paths rather than trust PATH.
func RemoteRsyncPath(ctx context.Context, runner *exec.Runner, host string) (string, error) {
	candidates := []string{"rsync", "/opt/homebrew/bin/rsync", "/usr/local/bin/rsync"}
	var lastVer string
	for _, cand := range candidates {
		res, err := runner.Run(ctx, "ssh",
			"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", host,
			cand+" --version 2>&1 | head -2")
		if err != nil {
			continue
		}
		ver := strings.TrimSpace(res.Stdout)
		if ver == "" {
			continue
		}
		lastVer = ver
		if remoteRsyncUsable(ver) {
			if cand == "rsync" {
				return "", nil // default is fine
			}
			return cand, nil
		}
	}
	if lastVer == "" {
		return "", fmt.Errorf("no rsync found on %s", host)
	}
	return "", fmt.Errorf("peer %s only offers openrsync/2.x (%q), which cannot receive -aHAX; install rsync 3.x there (brew install rsync)",
		host, firstLine(lastVer))
}

func remoteRsyncUsable(version string) bool {
	v := strings.ToLower(version)
	if strings.Contains(v, "openrsync") {
		return false
	}
	// "rsync  version 2.6.9" and the "2.6.9 compatible" banner both disqualify.
	if strings.Contains(v, "version 2.") {
		return false
	}
	return strings.Contains(v, "version 3.") || strings.Contains(v, "version 4.")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func CheckRsync(runner *exec.Runner) (string, bool) {
	if !runner.CommandExists("rsync") {
		return "", false
	}
	result, err := runner.Run(context.Background(), "rsync", "--version")
	if err != nil {
		return "", false
	}
	// First line: "rsync  version 3.3.0  protocol version 31"
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0]), true
	}
	return "unknown", true
}

// InstallRsync installs rsync via brew or apt. Progress lines go to out;
// a nil out means process stdout.
func InstallRsync(ctx context.Context, runner *exec.Runner, out io.Writer) error {
	out = outOrStdout(out)
	brew := exec.NewBrew(runner)
	if brew.IsAvailable() {
		fmt.Fprintln(out, "Installing rsync via Homebrew...")
		return brew.Install(ctx, []string{"rsync"})
	}

	if runtime.GOOS == "linux" {
		fmt.Fprintln(out, "Installing rsync via apt...")
		_, err := runner.Run(ctx, "sudo", "apt-get", "install", "-y", "rsync")
		return err
	}

	return fmt.Errorf("cannot auto-install rsync: install Homebrew first or use your package manager")
}

// CheckSSH verifies SSH connectivity to a remote host (5s timeout).
func CheckSSH(ctx context.Context, runner *exec.Runner, host string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := runner.Run(timeoutCtx, "ssh",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		// ponytail: known ceiling. See docs/CEILINGS.md (first-contact trust).
		"-o", "StrictHostKeyChecking=accept-new",
		host, "echo ok")
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("SSH to %s timed out (5s) — check VPN/network", host)
		}
		return fmt.Errorf("SSH to %s failed: %w", host, err)
	}
	return nil
}
