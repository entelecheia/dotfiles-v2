package module

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/config/catalog"
)

// MacAppsModule installs macOS cask applications selected during init or
// listed in the active profile. No-op on non-darwin.
type MacAppsModule struct{}

func (m *MacAppsModule) Name() string { return "macapps" }

// resolveCasks returns the de-duplicated cask list the module should ensure.
// Config.Casks wins; when empty we fall back to the embedded catalog defaults
// so a profile can opt in without repeating the list.
func (m *MacAppsModule) resolveCasks(rc *RunContext) []string {
	configured := rc.Config.AllCasks()
	if len(configured) > 0 {
		return configured
	}
	cat, err := catalog.LoadMacApps()
	if err != nil {
		rc.Runner.Logger.Warn("macapps catalog load", "err", err)
		return nil
	}
	return cat.Defaults
}

func (m *MacAppsModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	if runtime.GOOS != "darwin" {
		return &CheckResult{Satisfied: true}, nil
	}
	if rc.Brew == nil || !rc.Brew.IsAvailable() {
		return &CheckResult{
			Satisfied: false,
			Changes: []Change{{
				Description: "homebrew not available; install brew first",
				Command:     "(see scripts/install.sh)",
			}},
		}, nil
	}

	casks := m.resolveCasks(rc)
	if len(casks) == 0 {
		return &CheckResult{Satisfied: true}, nil
	}

	missing := rc.Brew.MissingCasks(casks)
	if len(missing) == 0 {
		return &CheckResult{Satisfied: true}, nil
	}

	changes := []Change{{
		Description: fmt.Sprintf("install %d cask(s): %s", len(missing), strings.Join(missing, ", ")),
		Command:     "brew install --cask " + strings.Join(missing, " "),
	}}
	return &CheckResult{Satisfied: false, Changes: changes}, nil
}

func (m *MacAppsModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	if runtime.GOOS != "darwin" {
		return &ApplyResult{}, nil
	}
	if rc.Brew == nil || !rc.Brew.IsAvailable() {
		return nil, fmt.Errorf("homebrew not available")
	}

	casks := m.resolveCasks(rc)
	if len(casks) == 0 {
		return &ApplyResult{}, nil
	}
	missing := rc.Brew.MissingCasks(casks)
	if len(missing) == 0 {
		return &ApplyResult{}, nil
	}

	if err := rc.Brew.InstallCask(ctx, missing); err != nil {
		return nil, fmt.Errorf("install casks: %w", err)
	}
	return &ApplyResult{
		Changed:  true,
		Messages: []string{fmt.Sprintf("installed %d cask(s): %s", len(missing), strings.Join(missing, ", "))},
	}, nil
}
