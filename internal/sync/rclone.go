package sync

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
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
	result, err := runner.Run(context.Background(), "rclone", "version")
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
	result, err := runner.Run(ctx, "rclone", "listremotes")
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
// This requires a TTY — it attaches stdin/stdout/stderr directly.
func ConfigRemote(ctx context.Context, name string) error {
	cmd := osexec.CommandContext(ctx, "rclone", "config", "create", name, "drive",
		"--drive-scope", "drive",
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// CheckRemote verifies that a remote is accessible (with 15s timeout).
func CheckRemote(ctx context.Context, runner *exec.Runner, remote string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := runner.Run(timeoutCtx, "rclone", "lsd", remote+":", "--max-depth", "0")
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("remote %q timed out (15s) — check rclone auth: rclone config reconnect %s:", remote, remote)
		}
		return fmt.Errorf("remote %q not accessible: %w", remote, err)
	}
	return nil
}
