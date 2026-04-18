package rclone

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// CheckRclone checks if rclone is installed and returns its version.
func CheckRclone(runner *exec.Runner) (string, bool) {
	if !runner.CommandExists("rclone") {
		return "", false
	}
	result, err := runner.RunQuery(context.Background(), "rclone", "version")
	if err != nil {
		return "", false
	}
	// First line: "rclone v1.73.3"
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0]), true
	}
	return "unknown", true
}

// InstallRclone installs rclone via brew or curl installer.
func InstallRclone(ctx context.Context, runner *exec.Runner) error {
	brew := exec.NewBrew(runner)
	if brew.IsAvailable() {
		fmt.Println("Installing rclone via Homebrew...")
		return brew.Install(ctx, []string{"rclone"})
	}

	if runtime.GOOS == "linux" {
		fmt.Println("Installing rclone via official installer...")
		_, err := runner.RunShell(ctx, "curl -fsSL https://rclone.org/install.sh | sudo bash")
		return err
	}

	return fmt.Errorf("cannot auto-install rclone: install Homebrew first or download from https://rclone.org/downloads/")
}

// ListRemotes returns configured rclone remote names (without trailing colon).
func ListRemotes(ctx context.Context, runner *exec.Runner) ([]string, error) {
	result, err := runner.RunQuery(ctx, "rclone", "listremotes")
	if err != nil {
		return nil, fmt.Errorf("listing remotes: %w", err)
	}

	var remotes []string
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		name := strings.TrimSuffix(strings.TrimSpace(line), ":")
		if name != "" {
			remotes = append(remotes, name)
		}
	}
	return remotes, nil
}

// HasRemote checks if a named remote is configured.
func HasRemote(ctx context.Context, runner *exec.Runner, name string) bool {
	remotes, err := ListRemotes(ctx, runner)
	if err != nil {
		return false
	}
	for _, r := range remotes {
		if r == name {
			return true
		}
	}
	return false
}

// ConfigRemote runs interactive rclone config to create a Google Drive remote.
// Requires a TTY; in dry-run mode this is a no-op with a warning log.
func ConfigRemote(ctx context.Context, runner *exec.Runner, name string) error {
	return runner.RunInteractive(ctx, "rclone", "config", "create", name, "drive",
		"--drive-scope", "drive",
	)
}

// ReconnectRemote runs interactive rclone config reconnect to refresh auth.
// Requires a TTY; in dry-run mode this is a no-op with a warning log.
func ReconnectRemote(ctx context.Context, runner *exec.Runner, name string) error {
	return runner.RunInteractive(ctx, "rclone", "config", "reconnect", name+":")
}

// CheckRemote verifies that a remote is accessible (with 15s timeout).
func CheckRemote(ctx context.Context, runner *exec.Runner, remote string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := runner.RunQuery(timeoutCtx, "rclone", "lsd", remote+":", "--max-depth", "0")
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("remote %q timed out (15s) — run 'dot clone reconnect' to fix authentication", remote)
		}
		return fmt.Errorf("remote %q not accessible: %w", remote, err)
	}
	return nil
}
