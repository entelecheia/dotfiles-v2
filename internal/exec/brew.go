package exec

import (
	"context"
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
	result, err := b.Runner.Run(context.Background(), "brew", "list", "--formula", formula)
	if err != nil {
		return false
	}
	return result.ExitCode == 0
}

// IsCaskInstalled checks if a cask is installed.
func (b *Brew) IsCaskInstalled(cask string) bool {
	result, err := b.Runner.Run(context.Background(), "brew", "list", "--cask", cask)
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

// MissingFormulas returns formulas from the list that are not installed.
func (b *Brew) MissingFormulas(formulas []string) []string {
	// Use brew list --formula -1 to get all installed formulas at once
	result, err := b.Runner.Run(context.Background(), "brew", "list", "--formula", "-1")
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
