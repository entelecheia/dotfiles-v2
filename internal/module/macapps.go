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
	cat, err := catalog.LoadMacApps()
	if err != nil {
		rc.Runner.Logger.Warn("macapps catalog load", "err", err)
		return nil
	}
	base := rc.Config.Casks
	if len(base) == 0 {
		base = cat.Defaults
	}
	seen := make(map[string]bool, len(base)+len(rc.Config.CasksExtra))
	var casks []string
	for _, token := range base {
		if seen[token] {
			continue
		}
		seen[token] = true
		casks = append(casks, token)
	}
	for _, token := range rc.Config.CasksExtra {
		if seen[token] {
			continue
		}
		seen[token] = true
		casks = append(casks, token)
	}
	return casks
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

	// Skip casks whose .app already exists under /Applications. apply is
	// idempotent and should not fail when an app was installed outside brew.
	var skipped []string
	if existing := rc.Brew.ExistingCaskTargets(missing); len(existing) > 0 {
		var toInstall []string
		for _, c := range missing {
			if existing[c] {
				skipped = append(skipped, c)
			} else {
				toInstall = append(toInstall, c)
			}
		}
		missing = toInstall
		rc.Runner.Logger.Info("macapps: skipping externally-installed casks", "tokens", skipped)
	}

	if len(missing) == 0 {
		msgs := []string{"no casks to install"}
		if len(skipped) > 0 {
			msgs = append(msgs, fmt.Sprintf("skipped %d already-present: %s", len(skipped), strings.Join(skipped, ", ")))
		}
		return &ApplyResult{Messages: msgs}, nil
	}

	if err := rc.Brew.InstallCask(ctx, missing, false); err != nil {
		return nil, fmt.Errorf("install casks: %w", err)
	}
	msgs := []string{fmt.Sprintf("installed %d cask(s): %s", len(missing), strings.Join(missing, ", "))}
	if len(skipped) > 0 {
		msgs = append(msgs, fmt.Sprintf("skipped %d already-present: %s", len(skipped), strings.Join(skipped, ", ")))
	}
	return &ApplyResult{Changed: true, Messages: msgs}, nil
}
