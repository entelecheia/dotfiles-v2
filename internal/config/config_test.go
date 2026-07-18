package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsModuleEnabled(t *testing.T) {
	cfg := &Config{
		Modules: ModulesConfig{
			Packages:  ModuleToggle{Enabled: true},
			Shell:     ShellConfig{Enabled: true},
			Node:      ModuleToggle{Enabled: true},
			Git:       GitConfig{Enabled: false},
			SSH:       SSHModConfig{Enabled: true},
			Terminal:  TermConfig{Enabled: false},
			Tmux:      ModuleToggle{Enabled: true},
			Workspace: WorkConfig{Enabled: false},
			AI:        AIConfig{Enabled: true},
			Fonts:     FontsConfig{Enabled: false},
			Conda:     ModuleToggle{Enabled: true},
			GPG:       ModuleToggle{Enabled: false},
			Secrets:   ModuleToggle{Enabled: true},
			MacApps:   MacAppsConfig{Enabled: true},
		},
	}

	cases := []struct {
		name string
		want bool
	}{
		{"packages", true},
		{"shell", true},
		{"node", true},
		{"git", false},
		{"ssh", true},
		{"terminal", false},
		{"tmux", true},
		{"workspace", false},
		{"ai", true},
		{"fonts", false},
		{"conda", true},
		{"gpg", false},
		{"secrets", true},
		{"macapps", true},
		{"unknown", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.IsModuleEnabled(tc.name)
			if got != tc.want {
				t.Errorf("IsModuleEnabled(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestAllPackages_Deduplication(t *testing.T) {
	cfg := &Config{
		Packages:      []string{"git", "curl", "fzf"},
		PackagesExtra: []string{"fzf", "btop", "curl", "lazygit"},
	}

	all := cfg.AllPackages()

	// Should deduplicate: git, curl, fzf, btop, lazygit = 5 unique
	if len(all) != 5 {
		t.Errorf("AllPackages: expected 5 unique packages, got %d: %v", len(all), all)
	}

	seen := make(map[string]int)
	for _, p := range all {
		seen[p]++
	}
	for pkg, count := range seen {
		if count > 1 {
			t.Errorf("AllPackages: duplicate package %q appears %d times", pkg, count)
		}
	}
}

func TestAllPackages_OrderPreserved(t *testing.T) {
	cfg := &Config{
		Packages:      []string{"git", "curl"},
		PackagesExtra: []string{"btop", "lazygit"},
	}

	all := cfg.AllPackages()
	expected := []string{"git", "curl", "btop", "lazygit"}
	if len(all) != len(expected) {
		t.Fatalf("AllPackages: expected %d packages, got %d", len(expected), len(all))
	}
	for i, p := range expected {
		if all[i] != p {
			t.Errorf("AllPackages[%d]: expected %q, got %q", i, p, all[i])
		}
	}
}

func TestAllPackages_EmptyExtra(t *testing.T) {
	cfg := &Config{
		Packages: []string{"git", "curl"},
	}
	all := cfg.AllPackages()
	if len(all) != 2 {
		t.Errorf("AllPackages with no extra: expected 2, got %d", len(all))
	}
}

func TestAllCasks_DeduplicatesAndPreservesOrder(t *testing.T) {
	cfg := &Config{
		Casks:      []string{"raycast", "obsidian", "arc"},
		CasksExtra: []string{"obsidian", "slack", "arc", "zed"},
	}
	all := cfg.AllCasks()
	expected := []string{"raycast", "obsidian", "arc", "slack", "zed"}
	if len(all) != len(expected) {
		t.Fatalf("AllCasks: expected %d, got %d: %v", len(expected), len(all), all)
	}
	for i, v := range expected {
		if all[i] != v {
			t.Errorf("AllCasks[%d] = %q, want %q", i, all[i], v)
		}
	}
}

func TestAllCasks_IncludesTerminalAppsWhenEnabled(t *testing.T) {
	cfg := &Config{
		Casks:      []string{"raycast", "warp"},
		CasksExtra: []string{"cmux", "raycast"},
		Modules: ModulesConfig{
			Terminal: TermConfig{
				Enabled: true,
				Apps:    []string{"warp", "wave", "cmux"},
			},
		},
	}
	all := cfg.AllCasks()
	expected := []string{"raycast", "warp", "wave", "cmux"}
	if len(all) != len(expected) {
		t.Fatalf("AllCasks: expected %d, got %d: %v", len(expected), len(all), all)
	}
	for i, v := range expected {
		if all[i] != v {
			t.Errorf("AllCasks[%d] = %q, want %q", i, all[i], v)
		}
	}
}

func TestTerminalCatalogIncludesRequestedOptions(t *testing.T) {
	for _, token := range []string{"warp", "wave", "cmux", "iterm2"} {
		if !IsTerminalAppToken(token) {
			t.Fatalf("terminal app catalog missing %q", token)
		}
	}
}

func TestTemplateData_Keys(t *testing.T) {
	cfg := &Config{
		Name:       "Test User",
		Email:      "test@example.com",
		GithubUser: "testuser",
		Timezone:   "UTC",
		System: &SystemInfo{
			OS:       "darwin",
			Arch:     "arm64",
			Hostname: "testhost",
		},
		Modules: ModulesConfig{
			Workspace: WorkConfig{Enabled: true, Path: "/home/test", Gdrive: "gdrive"},
			AI:        AIConfig{Enabled: true},
			Terminal:  TermConfig{Warp: true},
			SSH:       SSHModConfig{KeyName: "id_ed25519"},
			Git:       GitConfig{Signing: true},
			Fonts:     FontsConfig{Family: "FiraCode"},
		},
	}

	data := cfg.TemplateData()

	requiredKeys := []string{
		"Home",
		"Name", "Email", "GithubUser", "Timezone",
		"Hostname", "IsDarwin",
		"EnableWorkspace", "EnableAI",
		"WorkspacePath", "VaultPath", "CloudSymlink",
		"SSHKeyName", "CoauthorGuard",
		"HasCUDA", "CUDAHome", "HasNVIDIAGPU",
	}
	for _, k := range requiredKeys {
		if _, ok := data[k]; !ok {
			t.Errorf("TemplateData: missing key %q", k)
		}
	}

	// Spot-check values
	if data["Name"] != "Test User" {
		t.Errorf("TemplateData[Name] = %v, want %q", data["Name"], "Test User")
	}
	if data["IsDarwin"] != true {
		t.Errorf("TemplateData[IsDarwin] = %v, want true", data["IsDarwin"])
	}
	if data["EnableWorkspace"] != true {
		t.Errorf("TemplateData[EnableWorkspace] = %v, want true", data["EnableWorkspace"])
	}
}

func TestTemplateData_NilSystem(t *testing.T) {
	cfg := &Config{}
	data := cfg.TemplateData()

	if data["IsDarwin"] != false {
		t.Errorf("TemplateData[IsDarwin] with nil System = %v, want false", data["IsDarwin"])
	}
	if data["Hostname"] != "" {
		t.Errorf("TemplateData[Hostname] with nil System = %v, want empty string", data["Hostname"])
	}
}

func TestVaultPath_Explicit(t *testing.T) {
	cfg := &Config{Modules: ModulesConfig{Workspace: WorkConfig{
		Path:  "~/workspace",
		Vault: "~/custom/vault",
	}}}
	if got, want := cfg.VaultPath(), "~/custom/vault"; got != want {
		t.Errorf("VaultPath() = %q, want %q", got, want)
	}
}

func TestVaultPath_DetectsWorkVault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "workspace", "work", "vault"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Modules: ModulesConfig{Workspace: WorkConfig{Path: "~/workspace"}}}
	if got, want := cfg.VaultPath(), "~/workspace/work/vault"; got != want {
		t.Errorf("VaultPath() = %q, want %q", got, want)
	}
}

func TestVaultPath_DetectsTopLevelVault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "workspace", "vault"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Modules: ModulesConfig{Workspace: WorkConfig{Path: "~/workspace"}}}
	if got, want := cfg.VaultPath(), "~/workspace/vault"; got != want {
		t.Errorf("VaultPath() = %q, want %q", got, want)
	}
}

func TestVaultPath_FreshDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &Config{Modules: ModulesConfig{Workspace: WorkConfig{Path: "~/workspace"}}}
	if got, want := cfg.VaultPath(), "~/workspace/work/vault"; got != want {
		t.Errorf("VaultPath() = %q, want %q", got, want)
	}
}

func TestVaultPath_EmptyWorkspacePath(t *testing.T) {
	cfg := &Config{}
	if got := cfg.VaultPath(); got != "" {
		t.Errorf("VaultPath() with empty workspace path = %q, want empty", got)
	}
}

func TestVaultPath_RelativeAnchoredUnderWorkspace(t *testing.T) {
	for vault, want := range map[string]string{
		"work/vault": "~/workspace/work/vault",
		"vault":      "~/workspace/vault",
	} {
		cfg := &Config{Modules: ModulesConfig{Workspace: WorkConfig{Path: "~/workspace", Vault: vault}}}
		if got := cfg.VaultPath(); got != want {
			t.Errorf("VaultPath() with vault=%q = %q, want %q", vault, got, want)
		}
	}
}

func TestVaultCloneTarget_Explicit(t *testing.T) {
	cfg := &Config{Modules: ModulesConfig{Workspace: WorkConfig{Path: "~/workspace", Vault: "work/vault"}}}
	if got, want := cfg.VaultCloneTarget(), "~/workspace/work/vault"; got != want {
		t.Errorf("VaultCloneTarget() = %q, want %q", got, want)
	}
}

func TestVaultCloneTarget_Detected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "workspace", "work", "vault"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Modules: ModulesConfig{Workspace: WorkConfig{Path: "~/workspace"}}}
	if got, want := cfg.VaultCloneTarget(), "~/workspace/work/vault"; got != want {
		t.Errorf("VaultCloneTarget() = %q, want %q", got, want)
	}
}

// Legacy setups (separate vault repo, no workspace.vault key) on a fresh
// machine must keep the legacy <ws>/vault target: no fresh-default redirect.
func TestVaultCloneTarget_LegacyFallthrough(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &Config{Modules: ModulesConfig{Workspace: WorkConfig{Path: "~/workspace"}}}
	if got := cfg.VaultCloneTarget(); got != "" {
		t.Errorf("VaultCloneTarget() with nothing on disk = %q, want empty (legacy <ws>/vault)", got)
	}
}
