package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A host the container module cannot set up (here: Linux with apt-get but no
// sudo, docker or podman) must keep its config and shell files untouched.
func TestContainerSetupStopWritesNothing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("drives the Linux install path; macOS would reach the real Homebrew prefix")
	}
	home := t.TempDir()
	bin := t.TempDir()
	writeCLITestFile(t, filepath.Join(bin, "apt-get"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "apt-get"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("XDG_CONFIG_HOME", "")
	statePath := filepath.Join(home, ".config", "dotfiles", "config.yaml")
	const before = "name: T\nprofile: server\n"
	writeCLITestFile(t, statePath, before)

	_, _, err := runDotForTest("container", "setup", "--yes", "--home", home)
	if err == nil || !strings.Contains(err.Error(), "sudo apt-get install -y podman") {
		t.Fatalf("setup error = %v, want the manual install commands", err)
	}
	if got, _ := os.ReadFile(statePath); string(got) != before {
		t.Fatalf("config changed on a stopped setup:\n%s", got)
	}
	for _, p := range []string{".zshrc", ".config/shell/00-exports.sh", ".local/share/dotfiles/shims/container"} {
		if _, err := os.Stat(filepath.Join(home, p)); err == nil {
			t.Errorf("stopped setup wrote %s", p)
		}
	}
}
