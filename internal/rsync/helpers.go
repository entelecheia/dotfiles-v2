package rsync

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// CheckRsync checks if rsync is installed and returns its version.
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

// InstallRsync installs rsync via brew or apt.
func InstallRsync(ctx context.Context, runner *exec.Runner) error {
	brew := exec.NewBrew(runner)
	if brew.IsAvailable() {
		fmt.Println("Installing rsync via Homebrew...")
		return brew.Install(ctx, []string{"rsync"})
	}

	if runtime.GOOS == "linux" {
		fmt.Println("Installing rsync via apt...")
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
