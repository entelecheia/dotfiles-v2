package guard

import (
	"os"
	"path/filepath"
	"testing"
)

// disableTempExemption keeps only the ~/.claude/plans exemption so
// t.TempDir-based fixtures (which live under os.TempDir) still exercise
// boundary denies.
func disableTempExemption(t *testing.T) {
	t.Helper()
	orig := exemptRootsFn
	exemptRootsFn = func(home string) []string {
		if home == "" {
			return nil
		}
		plans := filepath.Join(home, ".claude", "plans")
		if resolved, err := filepath.EvalSymlinks(plans); err == nil {
			plans = resolved
		}
		return []string{plans}
	}
	t.Cleanup(func() { exemptRootsFn = orig })
}

func TestCheckPath(t *testing.T) {
	root := t.TempDir()
	boundary := filepath.Join(root, "src")
	sibling := filepath.Join(root, "src-old") // prefix collision with boundary
	home := filepath.Join(root, "home")
	for _, dir := range []string{boundary, sibling, filepath.Join(home, ".claude", "plans")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	disableTempExemption(t)

	cases := []struct {
		name     string
		filePath string
		cwd      string
		boundary string
		deny     bool
	}{
		{"inside boundary", filepath.Join(boundary, "a.go"), "", boundary, false},
		{"boundary itself", boundary, "", boundary, false},
		{"nested inside", filepath.Join(boundary, "pkg", "deep", "a.go"), "", boundary, false},
		{"outside boundary", filepath.Join(root, "other.go"), "", boundary, true},
		{"prefix-collision sibling", filepath.Join(sibling, "a.go"), "", boundary, true},
		{"relative path resolved via cwd inside", "a.go", boundary, boundary, false},
		{"relative path resolved via cwd outside", "a.go", sibling, boundary, true},
		{"relative path without cwd fails open", "a.go", "", boundary, false},
		{"not-yet-existing file inside", filepath.Join(boundary, "new", "..", "b.go"), "", boundary, false},
		{"empty boundary allows", filepath.Join(root, "other.go"), "", "", false},
		{"empty path allows", "", "", boundary, false},
		{"claude plans exempt", filepath.Join(home, ".claude", "plans", "plan.md"), "", boundary, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := CheckPath(tc.filePath, tc.cwd, tc.boundary, home)
			if tc.deny && d.Permission != "deny" {
				t.Fatalf("CheckPath(%q, cwd=%q) = %+v, want deny", tc.filePath, tc.cwd, d)
			}
			if !tc.deny && d.Permission != "" {
				t.Fatalf("CheckPath(%q, cwd=%q) = %+v, want allow", tc.filePath, tc.cwd, d)
			}
			if tc.deny && d.Pattern != "freeze_boundary" {
				t.Fatalf("deny pattern = %q, want freeze_boundary", d.Pattern)
			}
		})
	}
}

func TestCheckPathTempExemptions(t *testing.T) {
	// Real exemptions active: temp locations are writable under any boundary.
	boundary := "/nonexistent-boundary-for-exemption-test"
	for _, path := range []string{
		"/tmp/scratch.txt",
		"/private/tmp/scratch.txt",
		filepath.Join(os.TempDir(), "scratch.txt"),
	} {
		if d := CheckPath(path, "", boundary, ""); d.Permission != "" {
			t.Fatalf("CheckPath(%q) = %+v, want exempt allow", path, d)
		}
	}
}

func TestCheckPathSymlinkedParent(t *testing.T) {
	disableTempExemption(t)
	root := t.TempDir()
	boundary := filepath.Join(root, "real")
	if err := os.MkdirAll(boundary, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(boundary, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	// A path through the symlink resolves into the boundary and is allowed.
	if d := CheckPath(filepath.Join(link, "a.go"), "", boundary, ""); d.Permission != "" {
		t.Fatalf("symlinked path into boundary should allow, got %+v", d)
	}
	// A boundary given via the symlink still contains the real path.
	if d := CheckPath(filepath.Join(boundary, "a.go"), "", link, ""); d.Permission != "" {
		t.Fatalf("real path against symlinked boundary should allow, got %+v", d)
	}
}

func TestCheckPathSymlinkFileEscape(t *testing.T) {
	disableTempExemption(t)
	root := t.TempDir()
	boundary := filepath.Join(root, "src")
	outside := filepath.Join(root, "outside.txt")
	if err := os.MkdirAll(boundary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(boundary, "escape.txt")
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	// A symlink file inside the boundary pointing outside must be denied.
	if d := CheckPath(escape, "", boundary, ""); d.Permission != "deny" {
		t.Fatalf("symlink escape should deny, got %+v", d)
	}
}
