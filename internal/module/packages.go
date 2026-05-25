package module

import (
	"context"
	"fmt"
	"strings"
)

// PackagesModule installs Homebrew packages.
type PackagesModule struct{}

func (m *PackagesModule) Name() string { return "packages" }

func (m *PackagesModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	// Ensure Homebrew PATH is set (may not be in PATH for fresh processes)
	rc.Brew.RefreshPath()

	if !rc.Brew.IsAvailable() {
		changes = append(changes, Change{
			Description: "install Homebrew",
			Command:     "curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh | bash",
		})
		for _, tap := range rc.Brew.MissingFormulaTaps(rc.Config.AllPackages()) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("tap Homebrew repository %q", tap),
				Command:     "brew tap " + tap,
			})
		}
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
	missingTaps := rc.Brew.MissingFormulaTaps(missing)
	if len(missing) == 0 && len(missingTaps) == 0 {
		return &CheckResult{Satisfied: true}, nil
	}

	for _, tap := range missingTaps {
		changes = append(changes, Change{
			Description: fmt.Sprintf("tap Homebrew repository %q", tap),
			Command:     "brew tap " + tap,
		})
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

	// Ensure Homebrew PATH is set (may not be in PATH for fresh processes)
	rc.Brew.RefreshPath()

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

	if missingTaps := rc.Brew.MissingFormulaTaps(missing); len(missingTaps) > 0 {
		if err := rc.Brew.Tap(ctx, missingTaps); err != nil {
			return nil, fmt.Errorf("tap homebrew repositories: %w", err)
		}
		messages = append(messages, fmt.Sprintf("tapped %d Homebrew repo(s): %s", len(missingTaps), strings.Join(missingTaps, ", ")))
	}

	if err := rc.Brew.Install(ctx, missing); err != nil {
		// brew install can exit non-zero for non-fatal issues (e.g. post-install
		// step warnings for gcc). Re-check which packages are actually missing.
		stillMissing := rc.Brew.MissingFormulas(missing)
		if len(stillMissing) > 0 {
			return nil, fmt.Errorf("brew install: %w; %d package(s) still missing after install: %v", err, len(stillMissing), stillMissing)
		}
		rc.Runner.Logger.Warn("brew install exited with error but all packages are present", "err", err)
	}

	messages = append(messages, fmt.Sprintf("installed %d package(s): %v", len(missing), missing))
	return &ApplyResult{Changed: true, Messages: messages}, nil
}
