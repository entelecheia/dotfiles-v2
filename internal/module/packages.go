package module

import (
	"context"
	"fmt"
)

// PackagesModule installs Homebrew packages.
type PackagesModule struct{}

func (m *PackagesModule) Name() string { return "packages" }

func (m *PackagesModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	if !rc.Brew.IsAvailable() {
		return &CheckResult{Satisfied: true}, nil // nothing to do without brew
	}

	missing := rc.Brew.MissingFormulas(rc.Config.AllPackages())
	if len(missing) == 0 {
		return &CheckResult{Satisfied: true}, nil
	}

	changes := make([]Change, 0, len(missing))
	for _, pkg := range missing {
		changes = append(changes, Change{
			Description: fmt.Sprintf("install package %q", pkg),
			Command:     fmt.Sprintf("brew install %s", pkg),
		})
	}
	return &CheckResult{Satisfied: false, Changes: changes}, nil
}

func (m *PackagesModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	if !rc.Brew.IsAvailable() {
		return &ApplyResult{Changed: false, Messages: []string{"brew not available, skipping"}}, nil
	}

	missing := rc.Brew.MissingFormulas(rc.Config.AllPackages())
	if len(missing) == 0 {
		return &ApplyResult{Changed: false}, nil
	}

	if err := rc.Brew.Install(ctx, missing); err != nil {
		return nil, fmt.Errorf("brew install: %w", err)
	}

	return &ApplyResult{
		Changed:  true,
		Messages: []string{fmt.Sprintf("installed %d package(s): %v", len(missing), missing)},
	}, nil
}
