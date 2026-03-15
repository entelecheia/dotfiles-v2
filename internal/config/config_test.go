package config

import (
	"testing"
)

func TestIsModuleEnabled(t *testing.T) {
	cfg := &Config{
		Modules: ModulesConfig{
			Packages:  ModuleToggle{Enabled: true},
			Shell:     ShellConfig{Enabled: true},
			Git:       GitConfig{Enabled: false},
			SSH:       SSHModConfig{Enabled: true},
			Terminal:  TermConfig{Enabled: false},
			Tmux:      ModuleToggle{Enabled: true},
			Workspace: WorkConfig{Enabled: false},
			AITools:   ModuleToggle{Enabled: true},
			Fonts:     FontsConfig{Enabled: false},
			Conda:     ModuleToggle{Enabled: true},
			GPG:       ModuleToggle{Enabled: false},
			Secrets:   ModuleToggle{Enabled: true},
		},
	}

	cases := []struct {
		name    string
		want    bool
	}{
		{"packages", true},
		{"shell", true},
		{"git", false},
		{"ssh", true},
		{"terminal", false},
		{"tmux", true},
		{"workspace", false},
		{"ai-tools", true},
		{"fonts", false},
		{"conda", true},
		{"gpg", false},
		{"secrets", true},
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
			AITools:   ModuleToggle{Enabled: true},
			Terminal:  TermConfig{Warp: true},
			SSH:       SSHModConfig{KeyName: "id_ed25519"},
			Git:       GitConfig{Signing: true},
			Fonts:     FontsConfig{Family: "FiraCode"},
		},
	}

	data := cfg.TemplateData()

	requiredKeys := []string{
		"Name", "Email", "GithubUser", "Timezone",
		"OS", "Arch", "Hostname",
		"IsDarwin", "IsLinux", "Profile",
		"EnableWorkspace", "EnableAITools", "EnableWarp",
		"WorkspacePath", "WorkspaceGdrive",
		"SSHKeyName", "GitSigning", "FontFamily",
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
	if data["IsLinux"] != false {
		t.Errorf("TemplateData[IsLinux] = %v, want false", data["IsLinux"])
	}
	if data["OS"] != "darwin" {
		t.Errorf("TemplateData[OS] = %v, want %q", data["OS"], "darwin")
	}
	if data["EnableWorkspace"] != true {
		t.Errorf("TemplateData[EnableWorkspace] = %v, want true", data["EnableWorkspace"])
	}
	if data["GitSigning"] != true {
		t.Errorf("TemplateData[GitSigning] = %v, want true", data["GitSigning"])
	}
}

func TestTemplateData_NilSystem(t *testing.T) {
	cfg := &Config{}
	data := cfg.TemplateData()

	if data["OS"] != "" {
		t.Errorf("TemplateData[OS] with nil System = %v, want empty string", data["OS"])
	}
	if data["IsDarwin"] != false {
		t.Errorf("TemplateData[IsDarwin] with nil System = %v, want false", data["IsDarwin"])
	}
}
