package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSessionName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"myproject", false},
		{"my-project", false},
		{"my_project", false},
		{"Project123", false},
		{"a", false},
		{"", true},
		{"my.project", true},
		{"my:project", true},
		{"my project", true},
		{"my/project", true},
		{"my@project", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSessionName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSessionName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestIsValidLayout(t *testing.T) {
	for _, name := range []string{"dev", "claude", "monitor"} {
		if !IsValidLayout(name) {
			t.Errorf("IsValidLayout(%q) = false, want true", name)
		}
	}
	if IsValidLayout("nonexistent") {
		t.Error("IsValidLayout(nonexistent) = true, want false")
	}
}

func TestIsValidTheme(t *testing.T) {
	for _, name := range []string{"default", "dracula", "nord", "catppuccin", "tokyo-night"} {
		if !IsValidTheme(name) {
			t.Errorf("IsValidTheme(%q) = false, want true", name)
		}
	}
	if IsValidTheme("nonexistent") {
		t.Error("IsValidTheme(nonexistent) = true, want false")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	// Point config to a temp dir with no file
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", t.TempDir())
	defer os.Setenv("HOME", origHome)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.DefaultLayout != "dev" {
		t.Errorf("DefaultLayout = %q, want %q", cfg.DefaultLayout, "dev")
	}
	if cfg.DefaultTheme != "default" {
		t.Errorf("DefaultTheme = %q, want %q", cfg.DefaultTheme, "default")
	}
	if len(cfg.Projects) != 0 {
		t.Errorf("Projects = %d, want 0", len(cfg.Projects))
	}
}

func TestLoadConfig_FromFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "dot")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	yaml := `default_layout: claude
default_theme: dracula
projects:
  - name: test
    path: /tmp/test
    layout: monitor
`
	if err := os.WriteFile(filepath.Join(configDir, "workspace.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.DefaultLayout != "claude" {
		t.Errorf("DefaultLayout = %q, want %q", cfg.DefaultLayout, "claude")
	}
	if cfg.DefaultTheme != "dracula" {
		t.Errorf("DefaultTheme = %q, want %q", cfg.DefaultTheme, "dracula")
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("Projects = %d, want 1", len(cfg.Projects))
	}
	if cfg.Projects[0].Name != "test" {
		t.Errorf("Projects[0].Name = %q, want %q", cfg.Projects[0].Name, "test")
	}
	if cfg.Projects[0].Layout != "monitor" {
		t.Errorf("Projects[0].Layout = %q, want %q", cfg.Projects[0].Layout, "monitor")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "dot")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "workspace.yaml"), []byte("{{invalid yaml"), 0644)

	_, err := LoadConfig()
	if err == nil {
		t.Error("LoadConfig with invalid YAML: expected error")
	}
}

func TestSaveAndLoad_Roundtrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &Config{
		DefaultLayout: "monitor",
		DefaultTheme:  "nord",
		Projects: []ProjectConfig{
			{Name: "proj1", Path: "/tmp/proj1", Layout: "claude"},
			{Name: "proj2", Path: "/tmp/proj2", Theme: "dracula"},
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}

	if loaded.DefaultLayout != cfg.DefaultLayout {
		t.Errorf("DefaultLayout = %q, want %q", loaded.DefaultLayout, cfg.DefaultLayout)
	}
	if loaded.DefaultTheme != cfg.DefaultTheme {
		t.Errorf("DefaultTheme = %q, want %q", loaded.DefaultTheme, cfg.DefaultTheme)
	}
	if len(loaded.Projects) != 2 {
		t.Fatalf("Projects = %d, want 2", len(loaded.Projects))
	}
	if loaded.Projects[0].Layout != "claude" {
		t.Errorf("Projects[0].Layout = %q, want %q", loaded.Projects[0].Layout, "claude")
	}
	if loaded.Projects[1].Theme != "dracula" {
		t.Errorf("Projects[1].Theme = %q, want %q", loaded.Projects[1].Theme, "dracula")
	}
}

func TestAddProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &Config{DefaultLayout: "dev", DefaultTheme: "default"}

	// Add to existing temp dir (guaranteed to exist)
	if err := cfg.AddProject("test", home, "", ""); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	if len(cfg.Projects) != 1 {
		t.Fatalf("Projects = %d, want 1", len(cfg.Projects))
	}
	if cfg.Projects[0].Name != "test" {
		t.Errorf("Name = %q, want %q", cfg.Projects[0].Name, "test")
	}
	// Path should be absolute
	if !filepath.IsAbs(cfg.Projects[0].Path) {
		t.Errorf("Path should be absolute, got %q", cfg.Projects[0].Path)
	}
	// Layout and Theme should be empty (same as default)
	if cfg.Projects[0].Layout != "" {
		t.Errorf("Layout should be empty for default, got %q", cfg.Projects[0].Layout)
	}
}

func TestAddProject_InvalidName(t *testing.T) {
	cfg := &Config{DefaultLayout: "dev", DefaultTheme: "default"}

	if err := cfg.AddProject("my.bad", "/tmp", "", ""); err == nil {
		t.Error("AddProject with invalid name: expected error")
	}
}

func TestAddProject_Duplicate(t *testing.T) {
	home := t.TempDir()
	cfg := &Config{
		DefaultLayout: "dev",
		DefaultTheme:  "default",
		Projects:      []ProjectConfig{{Name: "test", Path: home}},
	}

	if err := cfg.AddProject("test", home, "", ""); err == nil {
		t.Error("AddProject duplicate: expected error")
	}
}

func TestAddProject_InvalidPath(t *testing.T) {
	cfg := &Config{DefaultLayout: "dev", DefaultTheme: "default"}

	if err := cfg.AddProject("test", "/nonexistent/path/XYZ", "", ""); err == nil {
		t.Error("AddProject with nonexistent path: expected error")
	}
}

func TestAddProject_NonDefaultLayout(t *testing.T) {
	home := t.TempDir()
	cfg := &Config{DefaultLayout: "dev", DefaultTheme: "default"}

	if err := cfg.AddProject("test", home, "claude", "dracula"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	if cfg.Projects[0].Layout != "claude" {
		t.Errorf("Layout = %q, want %q", cfg.Projects[0].Layout, "claude")
	}
	if cfg.Projects[0].Theme != "dracula" {
		t.Errorf("Theme = %q, want %q", cfg.Projects[0].Theme, "dracula")
	}
}

func TestFindProject(t *testing.T) {
	cfg := &Config{
		Projects: []ProjectConfig{
			{Name: "alpha", Path: "/tmp/a"},
			{Name: "beta", Path: "/tmp/b"},
		},
	}

	p := cfg.FindProject("beta")
	if p == nil {
		t.Fatal("FindProject(beta) = nil")
	}
	if p.Path != "/tmp/b" {
		t.Errorf("Path = %q, want %q", p.Path, "/tmp/b")
	}

	if cfg.FindProject("nonexistent") != nil {
		t.Error("FindProject(nonexistent) should return nil")
	}
}

func TestRemoveProject(t *testing.T) {
	cfg := &Config{
		Projects: []ProjectConfig{
			{Name: "alpha", Path: "/tmp/a"},
			{Name: "beta", Path: "/tmp/b"},
			{Name: "gamma", Path: "/tmp/c"},
		},
	}

	if !cfg.RemoveProject("beta") {
		t.Error("RemoveProject(beta) = false, want true")
	}
	if len(cfg.Projects) != 2 {
		t.Errorf("Projects = %d, want 2", len(cfg.Projects))
	}
	if cfg.FindProject("beta") != nil {
		t.Error("beta should be removed")
	}

	if cfg.RemoveProject("nonexistent") {
		t.Error("RemoveProject(nonexistent) = true, want false")
	}
}

func TestEffectiveLayout(t *testing.T) {
	cfg := &Config{DefaultLayout: "dev"}

	// Project with no override
	p1 := &ProjectConfig{Name: "test", Path: "/tmp"}
	if cfg.EffectiveLayout(p1) != "dev" {
		t.Errorf("EffectiveLayout = %q, want %q", cfg.EffectiveLayout(p1), "dev")
	}

	// Project with override
	p2 := &ProjectConfig{Name: "test", Path: "/tmp", Layout: "claude"}
	if cfg.EffectiveLayout(p2) != "claude" {
		t.Errorf("EffectiveLayout = %q, want %q", cfg.EffectiveLayout(p2), "claude")
	}
}

func TestEffectiveTheme(t *testing.T) {
	cfg := &Config{DefaultTheme: "default"}

	p1 := &ProjectConfig{Name: "test", Path: "/tmp"}
	if cfg.EffectiveTheme(p1) != "default" {
		t.Errorf("EffectiveTheme = %q, want %q", cfg.EffectiveTheme(p1), "default")
	}

	p2 := &ProjectConfig{Name: "test", Path: "/tmp", Theme: "nord"}
	if cfg.EffectiveTheme(p2) != "nord" {
		t.Errorf("EffectiveTheme = %q, want %q", cfg.EffectiveTheme(p2), "nord")
	}
}

func TestValidLayouts(t *testing.T) {
	layouts := ValidLayouts()
	if len(layouts) < 3 {
		t.Errorf("ValidLayouts() returned %d entries, want >= 3", len(layouts))
	}
}

func TestValidThemes(t *testing.T) {
	themes := ValidThemes()
	if len(themes) < 5 {
		t.Errorf("ValidThemes() returned %d entries, want >= 5", len(themes))
	}
}
