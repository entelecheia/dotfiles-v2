package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunPreflightChecks(t *testing.T) {
	sys := &SystemInfo{
		OS:       "linux",
		Arch:     "amd64",
		Hostname: "test-host",
		Shell:    "zsh",
		HasGit:   true,
		GitVersion: "2.43.0",
		HasBrew:  false,
	}

	home := t.TempDir()
	report := RunPreflightChecks(sys, home)

	if len(report.Checks) == 0 {
		t.Fatal("expected checks, got none")
	}

	// Verify expected check names are present
	names := make(map[string]bool)
	for _, c := range report.Checks {
		names[c.Name] = true
	}

	required := []string{"OS", "Hostname", "Shell", "Git", "Homebrew", "GitHub connectivity"}
	for _, name := range required {
		if !names[name] {
			t.Errorf("missing check: %s", name)
		}
	}
}

func TestRunPreflightChecks_GPUChecks(t *testing.T) {
	sys := &SystemInfo{
		OS:           "linux",
		Arch:         "amd64",
		Hostname:     "gpu-server",
		Shell:        "bash",
		HasGit:       true,
		GitVersion:   "2.40.0",
		HasNVIDIAGPU: true,
		GPUModel:     "NVIDIA A100",
		HasCUDA:      true,
		CUDAHome:     "/usr/local/cuda",
		IsDGX:        true,
	}

	home := t.TempDir()
	report := RunPreflightChecks(sys, home)

	names := make(map[string]bool)
	for _, c := range report.Checks {
		names[c.Name] = true
	}

	for _, name := range []string{"NVIDIA GPU", "CUDA", "DGX"} {
		if !names[name] {
			t.Errorf("missing GPU check: %s", name)
		}
	}
}

func TestPreflightReportCounts(t *testing.T) {
	report := &PreflightReport{
		Checks: []PreflightCheck{
			{Status: CheckPass},
			{Status: CheckPass},
			{Status: CheckWarn},
			{Status: CheckFail},
		},
	}

	pass, warn, fail := report.Counts()
	if pass != 2 || warn != 1 || fail != 1 {
		t.Errorf("expected 2/1/1, got %d/%d/%d", pass, warn, fail)
	}
}

func TestCheckDirectories(t *testing.T) {
	home := t.TempDir()

	// Missing directories → warn
	checks := checkDirectories(home)
	for _, c := range checks {
		if c.Status != CheckWarn {
			t.Errorf("expected warn for missing dir %s, got %s", c.Name, c.Status)
		}
	}

	// Create directories → pass
	os.MkdirAll(filepath.Join(home, ".config"), 0755)
	os.MkdirAll(filepath.Join(home, ".local", "bin"), 0755)

	checks = checkDirectories(home)
	for _, c := range checks {
		if c.Status != CheckPass {
			t.Errorf("expected pass for existing dir %s, got %s", c.Name, c.Status)
		}
	}
}

func TestGeneratePreflightConfig_ServerProfile(t *testing.T) {
	sys := &SystemInfo{
		OS:           "linux",
		Arch:         "amd64",
		HasNVIDIAGPU: true,
		HasCUDA:      true,
	}

	state := GeneratePreflightConfig(sys)

	if state.Profile != "server" {
		t.Errorf("expected server profile, got %s", state.Profile)
	}
	if !state.Modules.AITools {
		t.Error("expected AI tools enabled")
	}
	if state.Modules.Fonts.Family != "" {
		t.Errorf("expected no font family for server, got %s", state.Modules.Fonts.Family)
	}
}

func TestGeneratePreflightConfig_FullProfile(t *testing.T) {
	sys := &SystemInfo{
		OS:   "darwin",
		Arch: "arm64",
	}

	state := GeneratePreflightConfig(sys)

	if state.Profile != "full" {
		t.Errorf("expected full profile, got %s", state.Profile)
	}
	if state.Modules.Fonts.Family != "FiraCode" {
		t.Errorf("expected FiraCode font, got %s", state.Modules.Fonts.Family)
	}
}

func TestGeneratePreflightConfig_SSHKeyName(t *testing.T) {
	sys := &SystemInfo{OS: "linux", Arch: "amd64"}

	// Without GITHUB_USER
	t.Setenv("GITHUB_USER", "")
	state := GeneratePreflightConfig(sys)
	if state.SSH.KeyName != "id_ed25519" {
		t.Errorf("expected id_ed25519, got %s", state.SSH.KeyName)
	}

	// With GITHUB_USER
	t.Setenv("GITHUB_USER", "testuser")
	state = GeneratePreflightConfig(sys)
	if state.SSH.KeyName != "id_ed25519_testuser" {
		t.Errorf("expected id_ed25519_testuser, got %s", state.SSH.KeyName)
	}
}

func TestCheckStatusString(t *testing.T) {
	tests := []struct {
		status CheckStatus
		want   string
	}{
		{CheckPass, "OK"},
		{CheckWarn, "WARN"},
		{CheckFail, "FAIL"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("CheckStatus(%d).String() = %s, want %s", tt.status, got, tt.want)
		}
	}
}
