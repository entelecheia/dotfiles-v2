package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate_Profile(t *testing.T) {
	tests := []struct {
		profile string
		wantErr bool
	}{
		{"", false},
		{"minimal", false},
		{"full", false},
		{"server", false},
		{"MINIMAL", true},
		{"custom", true},
		{"unknown", true},
	}
	for _, tt := range tests {
		s := &UserState{Profile: tt.profile}
		err := s.Validate()
		if (err != nil) != tt.wantErr {
			t.Errorf("Validate(profile=%q) err=%v, wantErr=%v", tt.profile, err, tt.wantErr)
		}
	}
}

func TestValidate_Email(t *testing.T) {
	tests := []struct {
		email   string
		wantErr bool
	}{
		{"", false},
		{"user@example.com", false},
		{"a@b", false},
		{"noat", true},
		{"spaces allowed@x.com", false}, // lenient
	}
	for _, tt := range tests {
		s := &UserState{Email: tt.email}
		err := s.Validate()
		if (err != nil) != tt.wantErr {
			t.Errorf("Validate(email=%q) err=%v, wantErr=%v", tt.email, err, tt.wantErr)
		}
	}
}

func TestValidate_GithubUser(t *testing.T) {
	tests := []struct {
		user    string
		wantErr bool
	}{
		{"", false},
		{"entelecheia", false},
		{"user-name", false},
		{"User123", false},
		{"bad user", true},
		{"under_score", true},
		{"dot.name", true},
		{strings.Repeat("a", 40), true},
	}
	for _, tt := range tests {
		s := &UserState{GithubUser: tt.user}
		err := s.Validate()
		if (err != nil) != tt.wantErr {
			t.Errorf("Validate(github_user=%q) err=%v, wantErr=%v", tt.user, err, tt.wantErr)
		}
	}
}

func TestValidate_SyncInterval(t *testing.T) {
	tests := []struct {
		interval int
		wantErr  bool
	}{
		{0, false},
		{60, false},
		{300, false},
		{86400, false},
		{59, true},
		{86401, true},
		{-1, true},
	}
	for _, tt := range tests {
		s := &UserState{}
		s.Modules.Sync.Interval = tt.interval
		err := s.Validate()
		if (err != nil) != tt.wantErr {
			t.Errorf("Validate(interval=%d) err=%v, wantErr=%v", tt.interval, err, tt.wantErr)
		}
	}
}

func TestValidate_GdriveSyncSharedExcludes(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		wantErr bool
	}{
		{"empty", nil, false},
		{"single relative", []string{"projects/koica-shared"}, false},
		{"multiple relative", []string{"projects/a", "external/b/c"}, false},
		{"absolute path rejected", []string{"/abs/path"}, true},
		{"parent escape rejected", []string{"../escape"}, true},
		{"parent escape mid-path rejected", []string{"projects/../../etc"}, true},
		{"single dot allowed", []string{"./relative"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &UserState{}
			s.Modules.GdriveSync.SharedExcludes = tt.entries
			err := s.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(shared_excludes=%v) err=%v, wantErr=%v", tt.entries, err, tt.wantErr)
			}
		})
	}
}

func TestValidate_TerminalSelections(t *testing.T) {
	valid := &UserState{Profile: "full"}
	valid.Modules.TerminalApps.Enabled = true
	valid.Modules.TerminalApps.Casks = []string{"warp", "cmux", "iterm2"}
	valid.Modules.TerminalTools.Enabled = true
	valid.Modules.TerminalTools.Formulas = []string{"yazi", "bat", "zoxide"}
	valid.Modules.TerminalTools.FormulasExtra = []string{"atuin"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid terminal selections rejected: %v", err)
	}

	invalidApp := &UserState{Profile: "full"}
	invalidApp.Modules.TerminalApps.Casks = []string{"not-a-terminal"}
	if err := invalidApp.Validate(); err == nil {
		t.Fatal("expected invalid terminal app token to fail validation")
	}

	invalidTool := &UserState{Profile: "full"}
	invalidTool.Modules.TerminalTools.Formulas = []string{"not-curated"}
	if err := invalidTool.Validate(); err == nil {
		t.Fatal("expected invalid terminal tool formula to fail validation")
	}

	invalidExtra := &UserState{Profile: "full"}
	invalidExtra.Modules.TerminalTools.FormulasExtra = []string{"bad token"}
	if err := invalidExtra.Validate(); err == nil {
		t.Fatal("expected invalid terminal tool extra formula to fail validation")
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := &UserState{
		Name:       "Test User",
		Email:      "test@example.com",
		GithubUser: "testuser",
		Timezone:   "UTC",
		Profile:    "full",
	}
	original.Modules.Workspace.Path = "~/workspace"
	original.Modules.AI.Enabled = true
	original.Modules.Sync.Interval = 600
	original.Modules.TerminalApps.Enabled = true
	original.Modules.TerminalApps.Casks = []string{"warp", "iterm2"}
	original.Modules.TerminalTools.Enabled = true
	original.Modules.TerminalTools.Formulas = []string{"yazi", "bat", "zoxide"}
	original.Modules.TerminalTools.FormulasExtra = []string{"atuin"}

	if err := saveStateAt(path, original); err != nil {
		t.Fatalf("saveStateAt: %v", err)
	}

	loaded, err := loadStateAt(path)
	if err != nil {
		t.Fatalf("loadStateAt: %v", err)
	}

	if loaded.Name != original.Name {
		t.Errorf("Name: got %q, want %q", loaded.Name, original.Name)
	}
	if loaded.Email != original.Email {
		t.Errorf("Email: got %q, want %q", loaded.Email, original.Email)
	}
	if loaded.Profile != original.Profile {
		t.Errorf("Profile: got %q, want %q", loaded.Profile, original.Profile)
	}
	if loaded.Modules.Workspace.Path != original.Modules.Workspace.Path {
		t.Errorf("Workspace.Path: got %q, want %q", loaded.Modules.Workspace.Path, original.Modules.Workspace.Path)
	}
	if !loaded.Modules.AI.Enabled {
		t.Error("AI.Enabled: expected true")
	}
	if loaded.Modules.Sync.Interval != 600 {
		t.Errorf("Sync.Interval: got %d, want 600", loaded.Modules.Sync.Interval)
	}
	if strings.Join(loaded.Modules.TerminalApps.Casks, ",") != "warp,iterm2" {
		t.Errorf("TerminalApps.Casks: got %v", loaded.Modules.TerminalApps.Casks)
	}
	if strings.Join(loaded.Modules.TerminalTools.Formulas, ",") != "yazi,bat,zoxide" {
		t.Errorf("TerminalTools.Formulas: got %v", loaded.Modules.TerminalTools.Formulas)
	}
	if strings.Join(loaded.Modules.TerminalTools.FormulasExtra, ",") != "atuin" {
		t.Errorf("TerminalTools.FormulasExtra: got %v", loaded.Modules.TerminalTools.FormulasExtra)
	}
}

func TestLoadState_LegacyAIToolsMigratesToAI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("name: Test\nprofile: full\nmodules:\n  ai_tools: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadStateAt(path)
	if err != nil {
		t.Fatalf("loadStateAt: %v", err)
	}
	if !loaded.Modules.AI.Enabled {
		t.Fatal("legacy ai_tools did not enable modules.ai.enabled")
	}
	if err := saveStateAt(path, loaded); err != nil {
		t.Fatalf("saveStateAt: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ai_tools") {
		t.Fatalf("legacy ai_tools key was persisted: %s", data)
	}
}

func TestLoadState_LegacyWarpMigratesToTerminalApps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("name: Test\nprofile: full\nmodules:\n  warp: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadStateAt(path)
	if err != nil {
		t.Fatalf("loadStateAt: %v", err)
	}
	if !loaded.Modules.TerminalApps.Enabled || strings.Join(loaded.Modules.TerminalApps.Casks, ",") != "warp" {
		t.Fatalf("legacy warp did not migrate to terminal_apps: %+v", loaded.Modules.TerminalApps)
	}
	if err := saveStateAt(path, loaded); err != nil {
		t.Fatalf("saveStateAt: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "warp: true") {
		t.Fatalf("legacy warp key was persisted: %s", data)
	}
	if !strings.Contains(string(data), "terminal_apps:") {
		t.Fatalf("terminal_apps key missing after migration: %s", data)
	}
}

func TestApplyStateToConfig_TerminalSelections(t *testing.T) {
	cfg := &Config{
		Packages: []string{"git"},
		Modules: ModulesConfig{
			Terminal: TermConfig{
				Enabled: true,
				Warp:    true,
				Apps:    []string{"warp"},
				Tools:   DefaultTerminalTools("full"),
			},
		},
	}
	state := &UserState{Profile: "full"}
	state.Modules.TerminalApps.Enabled = true
	state.Modules.TerminalApps.Casks = []string{"cmux", "iterm2"}
	state.Modules.TerminalTools.Enabled = true
	state.Modules.TerminalTools.Formulas = []string{"yazi", "bat"}
	state.Modules.TerminalTools.FormulasExtra = []string{"atuin"}

	ApplyStateToConfig(cfg, state)

	if cfg.Modules.Terminal.Warp {
		t.Fatal("warp theme should be disabled when explicit terminal app selection excludes warp")
	}
	if strings.Join(cfg.Modules.Terminal.Apps, ",") != "cmux,iterm2" {
		t.Fatalf("Terminal.Apps = %v", cfg.Modules.Terminal.Apps)
	}
	if strings.Join(cfg.Modules.Terminal.Tools, ",") != "yazi,bat" {
		t.Fatalf("Terminal.Tools = %v", cfg.Modules.Terminal.Tools)
	}
	if strings.Join(cfg.Modules.Terminal.ToolsExtra, ",") != "atuin" {
		t.Fatalf("Terminal.ToolsExtra = %v", cfg.Modules.Terminal.ToolsExtra)
	}
	if !cfg.Modules.MacApps.Enabled {
		t.Fatal("terminal app casks should enable macapps module")
	}
	if strings.Join(cfg.AllCasks(), ",") != "cmux,iterm2" {
		t.Fatalf("AllCasks = %v", cfg.AllCasks())
	}
	if strings.Join(cfg.AllPackages(), ",") != "git,yazi,bat,atuin" {
		t.Fatalf("AllPackages = %v", cfg.AllPackages())
	}
}

func TestSave_AtomicNoTempLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	state := &UserState{Profile: "minimal"}
	if err := saveStateAt(path, state); err != nil {
		t.Fatalf("saveStateAt: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") || strings.Contains(name, ".config.yaml.") {
			t.Errorf("temp file not cleaned up: %s", name)
		}
	}
}

func TestSave_RejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	invalid := &UserState{Profile: "bogus"}
	err := saveStateAt(path, invalid)
	if err == nil {
		t.Fatal("expected error for invalid profile, got nil")
	}
	if !strings.Contains(err.Error(), "invalid profile") {
		t.Errorf("unexpected error: %v", err)
	}

	// File should NOT exist
	if _, err := os.Stat(path); err == nil {
		t.Error("config file written despite validation failure")
	}
}

func TestLoad_MissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.yaml")

	state, err := loadStateAt(path)
	if err != nil {
		t.Fatalf("loadStateAt missing: %v", err)
	}
	if state == nil {
		t.Fatal("expected empty state, got nil")
	}
	if state.Name != "" || state.Profile != "" {
		t.Errorf("expected zero value, got %+v", state)
	}
}

func TestLoad_CorruptYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("this: is: not: valid: yaml: [\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := loadStateAt(path)
	if err == nil {
		t.Fatal("expected error for corrupt YAML, got nil")
	}
	if !strings.Contains(err.Error(), "parsing state") {
		t.Errorf("unexpected error: %v", err)
	}
}
