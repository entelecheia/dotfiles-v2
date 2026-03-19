package module

import (
	"context"
	"fmt"
)

// PackagesModule installs Homebrew packages.
type PackagesModule struct{}

func (m *PackagesModule) Name() string { return "packages" }

func (m *PackagesModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	if !rc.Brew.IsAvailable() {
		changes = append(changes, Change{
			Description: "install Homebrew",
			Command:     "curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh | bash",
		})
		// Without brew, all packages are considered missing
		for _, pkg := range rc.Config.AllPackages() {
			changes = append(changes, Change{
				Description: fmt.Sprintf("install package %q", pkg),
				Command:     fmt.Sprintf("brew install %s", pkg),
			})
		}
		return &CheckResult{Satisfied: false, Changes: changes}, nil
	}

	missing := rc.Brew.MissingFormulas(rc.Config.AllPackages())
	if len(missing) == 0 {
		return &CheckResult{Satisfied: true}, nil
	}

	for _, pkg := range missing {
		changes = append(changes, Change{
			Description: fmt.Sprintf("install package %q", pkg),
			Command:     fmt.Sprintf("brew install %s", pkg),
		})
	}
	return &CheckResult{Satisfied: false, Changes: changes}, nil
}

func (m *PackagesModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string

	if !rc.Brew.IsAvailable() {
		if err := rc.Brew.InstallBrew(ctx); err != nil {
			return nil, fmt.Errorf("install homebrew: %w", err)
		}
		messages = append(messages, "installed Homebrew")

		if !rc.Brew.IsAvailable() {
			return nil, fmt.Errorf("homebrew installed but not found in PATH")
		}
	}

	missing := rc.Brew.MissingFormulas(rc.Config.AllPackages())
	if len(missing) == 0 {
		return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
	}

	if err := rc.Brew.Install(ctx, missing); err != nil {
		return nil, fmt.Errorf("brew install: %w", err)
	}

	messages = append(messages, fmt.Sprintf("installed %d package(s): %v", len(missing), missing))
	return &ApplyResult{Changed: true, Messages: messages}, nil
}
