package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeploy_CreatesFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	changed, err := Deploy()
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !changed {
		t.Error("Deploy: expected changed=true on first run")
	}

	dataDir, _ := DataDir()
	for _, name := range scriptFiles {
		path := filepath.Join(dataDir, name)
		fi, err := os.Stat(path)
		if err != nil {
			t.Errorf("Deploy: %s not created: %v", name, err)
			continue
		}
		if fi.Size() == 0 {
			t.Errorf("Deploy: %s is empty", name)
		}
		// Check executable permissions
		if fi.Mode()&0111 == 0 {
			t.Errorf("Deploy: %s not executable (mode %v)", name, fi.Mode())
		}
	}
}

func TestDeploy_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// First deploy
	_, err := Deploy()
	if err != nil {
		t.Fatalf("Deploy (first): %v", err)
	}

	// Second deploy should report no changes
	changed, err := Deploy()
	if err != nil {
		t.Fatalf("Deploy (second): %v", err)
	}
	if changed {
		t.Error("Deploy: expected changed=false on second run (idempotent)")
	}
}

func TestDeploy_DetectsChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// First deploy
	_, err := Deploy()
	if err != nil {
		t.Fatalf("Deploy (first): %v", err)
	}

	// Tamper with a file
	dataDir, _ := DataDir()
	path := filepath.Join(dataDir, "launcher.sh")
	os.WriteFile(path, []byte("#!/bin/bash\n# tampered"), 0755)

	// Third deploy should detect the change
	changed, err := Deploy()
	if err != nil {
		t.Fatalf("Deploy (after tamper): %v", err)
	}
	if !changed {
		t.Error("Deploy: expected changed=true after file was tampered")
	}
}

func TestLauncherPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := LauncherPath()
	if err != nil {
		t.Fatalf("LauncherPath: %v", err)
	}

	if filepath.Base(path) != "launcher.sh" {
		t.Errorf("LauncherPath base = %q, want launcher.sh", filepath.Base(path))
	}

	expected, _ := DataDir()
	if filepath.Dir(path) != expected {
		t.Errorf("LauncherPath dir = %q, want %q", filepath.Dir(path), expected)
	}
}

func TestDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}

	expected := filepath.Join(home, ".local", "share", "dot", "workspace")
	if dir != expected {
		t.Errorf("DataDir = %q, want %q", dir, expected)
	}
}

func TestConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}

	expected := filepath.Join(home, ".config", "dot", "workspace.yaml")
	if path != expected {
		t.Errorf("ConfigPath = %q, want %q", path, expected)
	}
}

func TestEmbeddedScripts_Readable(t *testing.T) {
	for _, name := range scriptFiles {
		content, err := embeddedScripts.ReadFile("scripts/" + name)
		if err != nil {
			t.Errorf("reading embedded %s: %v", name, err)
			continue
		}
		if len(content) == 0 {
			t.Errorf("embedded %s is empty", name)
		}
		// Verify shebang
		if len(content) > 2 && string(content[:2]) != "#!" {
			t.Errorf("embedded %s missing shebang", name)
		}
	}
}
