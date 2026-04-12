package clean

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanFindsNodeModules(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "project", "node_modules", "dep"), 0755)
	os.WriteFile(filepath.Join(root, "project", "node_modules", "dep", "index.js"), []byte("x"), 0644)

	s := NewScanner(root, false)
	result, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}
	if result.Matches[0].Pattern.Name != "node_modules" {
		t.Errorf("expected node_modules, got %s", result.Matches[0].Pattern.Name)
	}
}

func TestScanFindsPythonCaches(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache"} {
		os.MkdirAll(filepath.Join(root, name), 0755)
	}

	s := NewScanner(root, false)
	result, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 4 {
		t.Fatalf("expected 4 matches, got %d", len(result.Matches))
	}
}

func TestScanProtectsSysSubtree(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "_sys", "env", ".venv"), 0755)
	os.MkdirAll(filepath.Join(root, "project", ".venv"), 0755)

	s := NewScanner(root, false)
	result, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}
	if len(result.Protected) != 1 {
		t.Fatalf("expected 1 protected, got %d", len(result.Protected))
	}
}

func TestScanEnvRequiresProbe(t *testing.T) {
	root := t.TempDir()
	// env/ without pyvenv.cfg — should NOT match
	os.MkdirAll(filepath.Join(root, "project", "env"), 0755)
	// env/ with pyvenv.cfg — should match
	os.MkdirAll(filepath.Join(root, "other", "env"), 0755)
	os.WriteFile(filepath.Join(root, "other", "env", "pyvenv.cfg"), []byte("home = /usr"), 0644)

	s := NewScanner(root, false)
	result, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match (env with pyvenv.cfg), got %d", len(result.Matches))
	}
	if result.Matches[0].Pattern.Name != "env" {
		t.Errorf("expected env, got %s", result.Matches[0].Pattern.Name)
	}
}

func TestScanFindsDSStore(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".DS_Store"), []byte{0, 0, 0, 1}, 0644)
	os.MkdirAll(filepath.Join(root, "sub"), 0755)
	os.WriteFile(filepath.Join(root, "sub", ".DS_Store"), []byte{0, 0, 0, 1}, 0644)

	s := NewScanner(root, false)
	result, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	dsCount := 0
	for _, m := range result.Matches {
		if m.Pattern.Name == ".DS_Store" {
			dsCount++
		}
	}
	if dsCount != 2 {
		t.Fatalf("expected 2 .DS_Store matches, got %d", dsCount)
	}
}

func TestScanRiskyExcludedByDefault(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "dist"), 0755)
	os.MkdirAll(filepath.Join(root, "build"), 0755)

	s := NewScanner(root, false)
	result, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 0 {
		t.Fatalf("expected 0 matches (risky excluded), got %d", len(result.Matches))
	}
}

func TestScanRiskyIncludedWithFlag(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "dist"), 0755)
	os.MkdirAll(filepath.Join(root, "build"), 0755)

	s := NewScanner(root, true)
	result, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 2 {
		t.Fatalf("expected 2 matches (risky included), got %d", len(result.Matches))
	}
}

func TestScanSkipsGitDir(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git", "objects"), 0755)
	os.MkdirAll(filepath.Join(root, "project", "node_modules"), 0755)

	s := NewScanner(root, false)
	result, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	// Should only find node_modules, not anything inside .git
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}
}

func TestScanDoesNotDescendIntoMatch(t *testing.T) {
	root := t.TempDir()
	// Nested node_modules should produce only one result
	os.MkdirAll(filepath.Join(root, "node_modules", "dep", "node_modules"), 0755)

	s := NewScanner(root, false)
	result, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match (no descent), got %d", len(result.Matches))
	}
}

func TestDeleteRemovesMatches(t *testing.T) {
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules")
	os.MkdirAll(filepath.Join(nm, "dep"), 0755)
	os.WriteFile(filepath.Join(nm, "dep", "index.js"), []byte("x"), 0644)

	matches := []Match{
		{Path: nm, Pattern: Pattern{Kind: KindDirectory}, Size: 1, RelPath: "node_modules"},
	}
	deleted, _, errs := Delete(matches)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
	if _, err := os.Stat(nm); !os.IsNotExist(err) {
		t.Fatal("node_modules should be deleted")
	}
}
