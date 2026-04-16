package exec

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"runtime"
	"strings"
)

// Brew wraps Homebrew operations.
type Brew struct {
	Runner *Runner
}

// NewBrew creates a new Brew wrapper.
func NewBrew(runner *Runner) *Brew {
	return &Brew{Runner: runner}
}

// IsAvailable checks if brew is installed.
func (b *Brew) IsAvailable() bool {
	return b.Runner.CommandExists("brew")
}

// IsInstalled checks if a formula is installed.
func (b *Brew) IsInstalled(formula string) bool {
	result, err := b.Runner.RunQuery(context.Background(), "brew", "list", "--formula", formula)
	if err != nil {
		return false
	}
	return result.ExitCode == 0
}

// IsCaskInstalled checks if a cask is installed.
func (b *Brew) IsCaskInstalled(cask string) bool {
	result, err := b.Runner.RunQuery(context.Background(), "brew", "list", "--cask", cask)
	if err != nil {
		return false
	}
	return result.ExitCode == 0
}

// Install installs formulas.
func (b *Brew) Install(ctx context.Context, formulas []string) error {
	if len(formulas) == 0 {
		return nil
	}
	args := append([]string{"install"}, formulas...)
	_, err := b.Runner.Run(ctx, "brew", args...)
	return err
}

// InstallCask installs casks.
func (b *Brew) InstallCask(ctx context.Context, casks []string) error {
	if len(casks) == 0 {
		return nil
	}
	args := append([]string{"install", "--cask"}, casks...)
	_, err := b.Runner.Run(ctx, "brew", args...)
	return err
}

// InstallBrew installs Homebrew non-interactively.
func (b *Brew) InstallBrew(ctx context.Context) error {
	script := `NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`
	_, err := b.Runner.RunShell(ctx, script)
	if err != nil {
		return fmt.Errorf("install homebrew: %w", err)
	}
	// Add brew to PATH for this process
	b.RefreshPath()
	return nil
}

// RefreshPath adds the Homebrew bin directory to PATH for the current process.
func (b *Brew) RefreshPath() {
	var brewPaths []string
	if runtime.GOOS == "darwin" {
		brewPaths = []string{"/opt/homebrew/bin"}
	} else {
		brewPaths = []string{"/home/linuxbrew/.linuxbrew/bin", "/home/linuxbrew/.linuxbrew/sbin"}
	}
	for _, p := range brewPaths {
		if _, err := os.Stat(p); err == nil {
			os.Setenv("PATH", p+":"+os.Getenv("PATH"))
		}
	}
	// Clear cached lookups so IsAvailable() picks up the new PATH
	osexec.LookPath("brew") // warm cache
}

// MissingFormulas returns formulas from the list that are not installed.
func (b *Brew) MissingFormulas(formulas []string) []string {
	// Use brew list --formula -1 to get all installed formulas at once
	result, err := b.Runner.RunQuery(context.Background(), "brew", "list", "--formula", "-1")
	if err != nil {
		return formulas // assume all missing if we can't check
	}

	installed := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		installed[strings.TrimSpace(line)] = true
	}

	var missing []string
	for _, f := range formulas {
		if !installed[f] {
			missing = append(missing, f)
		}
	}
	return missing
}

// InstalledCasks returns the set of all currently installed casks.
func (b *Brew) InstalledCasks() map[string]bool {
	installed := make(map[string]bool)
	result, err := b.Runner.RunQuery(context.Background(), "brew", "list", "--cask", "-1")
	if err != nil {
		return installed
	}
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			installed[s] = true
		}
	}
	return installed
}

// MissingCasks returns casks from the list that are not installed.
func (b *Brew) MissingCasks(casks []string) []string {
	installed := b.InstalledCasks()
	if len(installed) == 0 && len(casks) > 0 {
		// Query failed; assume all missing so caller can attempt install.
		return casks
	}
	var missing []string
	for _, c := range casks {
		if !installed[c] {
			missing = append(missing, c)
		}
	}
	return missing
}
