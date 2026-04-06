package driveexclude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanFindsNodeModules(t *testing.T) {
	tmp := t.TempDir()

	// Create a fake project with node_modules
	nm := filepath.Join(tmp, "project", "node_modules", ".package-lock.json")
	if err := os.MkdirAll(filepath.Dir(nm), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nm, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(tmp)
	results, err := scanner.Scan()
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Pattern != "node_modules" {
		t.Errorf("expected pattern 'node_modules', got %q", results[0].Pattern)
	}
	if results[0].Status != StatusPending {
		t.Errorf("expected StatusPending, got %v", results[0].Status)
	}
}

func TestScanFindsBuildCaches(t *testing.T) {
	tmp := t.TempDir()

	dirs := []string{".next", ".astro", "__pycache__", ".venv"}
	for _, d := range dirs {
		p := filepath.Join(tmp, "proj", d)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		// Put a file inside so it's not empty
		if err := os.WriteFile(filepath.Join(p, "dummy"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	scanner := NewScanner(tmp)
	results, err := scanner.Scan()
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != len(dirs) {
		t.Fatalf("expected %d results, got %d", len(dirs), len(results))
	}

	found := make(map[string]bool)
	for _, r := range results {
		found[r.Pattern] = true
	}
	for _, d := range dirs {
		if !found[d] {
			t.Errorf("missing pattern %q in scan results", d)
		}
	}
}

func TestScanSkipsGitDir(t *testing.T) {
	tmp := t.TempDir()

	// .git should be skipped even though it's a hidden dir
	gitDir := filepath.Join(tmp, ".git", "objects")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(tmp)
	results, err := scanner.Scan()
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 0 {
		t.Fatalf("expected 0 results (should skip .git), got %d", len(results))
	}
}

func TestScanDoesNotDescendIntoMatch(t *testing.T) {
	tmp := t.TempDir()

	// Nested node_modules should not produce multiple results
	nested := filepath.Join(tmp, "proj", "node_modules", "pkg", "node_modules", "deep")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(tmp)
	results, err := scanner.Scan()
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result (no descent), got %d", len(results))
	}
}

func TestScanDetectsSymlink(t *testing.T) {
	tmp := t.TempDir()

	target := filepath.Join(tmp, "store", "nm")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}

	projDir := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(projDir, "node_modules")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(tmp)
	results, err := scanner.Scan()
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != StatusSymlink {
		t.Errorf("expected StatusSymlink, got %v", results[0].Status)
	}
	if results[0].LinkTarget != target {
		t.Errorf("expected link target %q, got %q", target, results[0].LinkTarget)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500B"},
		{2048, "2K"},
		{5 * 1024 * 1024, "5M"},
		{2 * 1024 * 1024 * 1024, "2.0G"},
	}
	for _, tt := range tests {
		got := FormatSize(tt.bytes)
		if got != tt.expected {
			t.Errorf("FormatSize(%d) = %q, want %q", tt.bytes, got, tt.expected)
		}
	}
}

func TestCountByStatus(t *testing.T) {
	results := []ScanResult{
		{Status: StatusPending},
		{Status: StatusPending},
		{Status: StatusExcluded},
		{Status: StatusSymlink},
	}

	counts := CountByStatus(results)
	if counts[StatusPending] != 2 {
		t.Errorf("pending: got %d, want 2", counts[StatusPending])
	}
	if counts[StatusExcluded] != 1 {
		t.Errorf("excluded: got %d, want 1", counts[StatusExcluded])
	}
	if counts[StatusSymlink] != 1 {
		t.Errorf("symlink: got %d, want 1", counts[StatusSymlink])
	}
}

func TestSumPendingSize(t *testing.T) {
	results := []ScanResult{
		{Status: StatusPending, Size: 100},
		{Status: StatusExcluded, Size: 200},
		{Status: StatusPending, Size: 300},
	}

	got := SumPendingSize(results)
	if got != 400 {
		t.Errorf("SumPendingSize = %d, want 400", got)
	}
}

func TestRelPath(t *testing.T) {
	got := RelPath("/home/user/workspace", "/home/user/workspace/proj/node_modules")
	if got != "proj/node_modules" {
		t.Errorf("RelPath = %q, want %q", got, "proj/node_modules")
	}
}

func TestIsInDrive(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/Users/foo/My Drive (bar)/work", true},
		{"/Users/foo/Google Drive/work", true},
		{"/home/foo/projects", false},
	}
	for _, tt := range tests {
		got := IsInDrive(tt.path)
		if got != tt.want {
			t.Errorf("IsInDrive(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
