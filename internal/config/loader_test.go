package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MinimalProfile(t *testing.T) {
	cfg, err := Load("minimal", "", nil)
	if err != nil {
		t.Fatalf("Load minimal: %v", err)
	}

	// Packages list should be non-empty
	if len(cfg.Packages) == 0 {
		t.Error("minimal profile: expected non-empty packages list")
	}

	// Check a few expected packages
	want := map[string]bool{"git": false, "curl": false, "fzf": false}
	for _, p := range cfg.Packages {
		want[p] = true
	}
	for pkg, found := range want {
		if !found {
			t.Errorf("minimal profile: expected package %q not found", pkg)
		}
	}

	// Modules enabled in minimal
	if !cfg.Modules.Shell.Enabled {
		t.Error("minimal profile: shell should be enabled")
	}
	if !cfg.Modules.Git.Enabled {
		t.Error("minimal profile: git should be enabled")
	}
	if !cfg.Modules.SSH.Enabled {
		t.Error("minimal profile: ssh should be enabled")
	}
	if !cfg.Modules.Terminal.Enabled {
		t.Error("minimal profile: terminal should be enabled")
	}
	if !cfg.Modules.Node.Enabled {
		t.Error("minimal profile: node should be enabled (so npm is usable post-apply)")
	}

	// fnm must be in the base package list so it's installed even on minimal.
	hasFnm := false
	for _, p := range cfg.Packages {
		if p == "fnm" {
			hasFnm = true
			break
		}
	}
	if !hasFnm {
		t.Error("minimal profile: expected fnm in base packages")
	}

	// Modules disabled in minimal
	if cfg.Modules.Workspace.Enabled {
		t.Error("minimal profile: workspace should be disabled")
	}
	if cfg.Modules.AI.Enabled {
		t.Error("minimal profile: ai should be disabled")
	}
	if cfg.Modules.Tmux.Enabled {
		t.Error("minimal profile: tmux should be disabled")
	}
}

func TestLoad_FullProfile(t *testing.T) {
	cfg, err := Load("full", "", nil)
	if err != nil {
		t.Fatalf("Load full: %v", err)
	}

	// Full extends minimal so it should have minimal packages in AllPackages
	all := cfg.AllPackages()
	minimalPkgs := []string{"git", "curl", "fzf", "ripgrep"}
	for _, p := range minimalPkgs {
		found := false
		for _, a := range all {
			if a == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("full profile: expected inherited package %q not found in AllPackages", p)
		}
	}

	// Full profile adds extra packages
	extraPkgs := []string{"anchor-cli", "btop", "lazygit", "tmux", "gnupg"}
	for _, p := range extraPkgs {
		found := false
		for _, a := range all {
			if a == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("full profile: expected extra package %q not found in AllPackages", p)
		}
	}

	// Modules enabled by full but not minimal
	if !cfg.Modules.Workspace.Enabled {
		t.Error("full profile: workspace should be enabled")
	}
	if !cfg.Modules.AI.Enabled {
		t.Error("full profile: ai should be enabled")
	}
	if !cfg.Modules.Tmux.Enabled {
		t.Error("full profile: tmux should be enabled")
	}
	if !cfg.Modules.GPG.Enabled {
		t.Error("full profile: gpg should be enabled")
	}
	if !cfg.Modules.Secrets.Enabled {
		t.Error("full profile: secrets should be enabled")
	}
	if len(cfg.CasksExtra) != 1 || cfg.CasksExtra[0] != "anchor" {
		t.Errorf("full profile: expected casks_extra [anchor], got %v", cfg.CasksExtra)
	}

	// Extends field is cleared after resolution
	if cfg.Extends != "" {
		t.Errorf("full profile: Extends should be cleared after merge, got %q", cfg.Extends)
	}
}

func TestLoad_AllProfilesEnableNode(t *testing.T) {
	// npm must be usable after apply on every profile, which requires
	// modules.node to be enabled and fnm to be in the package list.
	for _, name := range []string{"minimal", "full", "server"} {
		cfg, err := Load(name, "", nil)
		if err != nil {
			t.Fatalf("Load %s: %v", name, err)
		}
		if !cfg.Modules.Node.Enabled {
			t.Errorf("%s profile: node module should be enabled", name)
		}
		hasFnm := false
		for _, p := range cfg.AllPackages() {
			if p == "fnm" {
				hasFnm = true
				break
			}
		}
		if !hasFnm {
			t.Errorf("%s profile: expected fnm in AllPackages", name)
		}
	}
}

func TestLoad_ProfilesDoNotInstallBunByDefault(t *testing.T) {
	const bunFormula = "oven-sh/bun/bun"

	for _, name := range []string{"minimal", "full", "server"} {
		cfg, err := Load(name, "", nil)
		if err != nil {
			t.Fatalf("Load %s: %v", name, err)
		}

		hasBun := false
		for _, p := range cfg.AllPackages() {
			if p == bunFormula {
				hasBun = true
				break
			}
		}
		if hasBun {
			t.Errorf("%s profile: bun should not be installed by default", name)
		}
	}
}

func TestLoad_ServerProfileExtraPackages(t *testing.T) {
	cfg, err := Load("server", "", nil)
	if err != nil {
		t.Fatalf("Load server: %v", err)
	}

	want := map[string]bool{
		"btop": false,
		"tmux": false,
		"uv":   false,
		"pipx": false,
	}
	for _, p := range cfg.AllPackages() {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for pkg, found := range want {
		if !found {
			t.Fatalf("server profile: expected %s in AllPackages", pkg)
		}
	}
}

func TestMergeConfigs(t *testing.T) {
	base := &Config{
		Packages: []string{"git", "curl"},
		Modules: ModulesConfig{
			Shell: ShellConfig{Enabled: true},
			Git:   GitConfig{Enabled: true, Signing: false},
		},
	}
	overlay := &Config{
		Extends:       "base",
		PackagesExtra: []string{"btop", "lazygit"},
		Modules: ModulesConfig{
			Git: GitConfig{Enabled: true, Signing: true},
		},
	}

	merged := mergeConfigs(base, overlay)

	// Base packages kept (overlay.Packages is empty, so base wins)
	if len(merged.Packages) != 2 {
		t.Errorf("mergeConfigs: expected 2 base packages, got %d", len(merged.Packages))
	}

	// Extra packages appended
	if len(merged.PackagesExtra) != 2 {
		t.Errorf("mergeConfigs: expected 2 extra packages, got %d", len(merged.PackagesExtra))
	}

	// Overlay Git config wins (Enabled=true)
	if !merged.Modules.Git.Signing {
		t.Error("mergeConfigs: overlay Git.Signing should win")
	}

	// Base Shell preserved (overlay Shell not set)
	if !merged.Modules.Shell.Enabled {
		t.Error("mergeConfigs: base Shell.Enabled should be preserved")
	}

	// Extends cleared
	if merged.Extends != "" {
		t.Errorf("mergeConfigs: Extends should be cleared, got %q", merged.Extends)
	}
}

func TestMergeConfigs_OverlayPackagesWin(t *testing.T) {
	base := &Config{
		Packages: []string{"git", "curl"},
	}
	overlay := &Config{
		Packages: []string{"ripgrep", "fd"},
	}

	merged := mergeConfigs(base, overlay)

	if len(merged.Packages) != 2 {
		t.Errorf("expected 2 overlay packages, got %d", len(merged.Packages))
	}
	if merged.Packages[0] != "ripgrep" {
		t.Errorf("expected overlay packages to win, got %v", merged.Packages)
	}
}

func TestAvailableProfiles(t *testing.T) {
	profiles := AvailableProfiles()
	want := map[string]bool{"minimal": false, "full": false}
	for _, p := range profiles {
		want[p] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("AvailableProfiles: missing %q", name)
		}
	}
}

func TestLoad_InvalidProfile(t *testing.T) {
	_, err := Load("nonexistent", "", nil)
	if err == nil {
		t.Error("Load with invalid profile name: expected error, got nil")
	}
}

func TestLoad_CustomPath(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "custom.yaml")
	content := []byte("packages:\n  - vim\n  - htop\n")
	if err := os.WriteFile(cfgFile, content, 0644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	cfg, err := Load("", cfgFile, nil)
	if err != nil {
		t.Fatalf("Load with custom path: %v", err)
	}
	if len(cfg.Packages) != 2 {
		t.Errorf("expected 2 packages, got %d", len(cfg.Packages))
	}
}

func TestLoad_CustomPath_InvalidFile(t *testing.T) {
	_, err := Load("", "/nonexistent/path/config.yaml", nil)
	if err == nil {
		t.Error("Load with invalid custom path: expected error, got nil")
	}
}
