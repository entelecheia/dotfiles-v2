package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// onestopFixture sandboxes HOME/XDG and seeds the state file, an age key,
// AI/Anchor settings, and a secrets store so every wizard domain has
// something to work on.
type onestopFixture struct {
	home string
	root string
	host string
}

func newOnestopFixture(t *testing.T) *onestopFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	writeCLITestFile(t, filepath.Join(home, ".config", "dotfiles", "config.yaml"), "name: original\n")
	writeCLITestFile(t, filepath.Join(home, ".ssh", "age_key"), "AGE-SECRET-KEY-TEST")
	if err := os.Chmod(filepath.Join(home, ".ssh", "age_key"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(home, ".claude", "settings.json"), `{"model":"opus"}`)
	writeCLITestFile(t, filepath.Join(home, ".anchor", "settings.json"), `{"theme":"dark"}`)
	writeCLITestFile(t, filepath.Join(home, ".local", "share", "dotfiles-secrets", "x.age"), "encrypted-payload")

	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if idx := strings.Index(host, "."); idx > 0 {
		host = host[:idx]
	}
	return &onestopFixture{home: home, root: t.TempDir(), host: host}
}

// readTag extracts the `tag:` value from a meta.yaml file.
func readTag(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "tag:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "tag:"))
		}
	}
	return ""
}

// latestVersion reads <hostDir>/latest.txt.
func latestVersion(t *testing.T, hostDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(hostDir, "latest.txt"))
	if err != nil {
		t.Fatalf("latest.txt: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func TestOnestopBackupAllDomains(t *testing.T) {
	f := newOnestopFixture(t)

	// apps is excluded: it is darwin-only AND depends on brew + real app
	// settings; the other three domains cover the wizard plumbing.
	out, errOut, err := runDotForTest("backup", "--yes", "--to", f.root, "--scope", "profile,ai,secrets", "--include-secrets")
	if err != nil {
		t.Fatalf("backup: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	if !strings.Contains(out, "one-stop backup complete") {
		t.Errorf("missing completion line:\n%s", out)
	}

	profileHost := filepath.Join(f.root, "profiles", f.host)
	profVer := latestVersion(t, profileHost)
	for _, rel := range []string{"config.yaml", "meta.yaml", "secrets/age_key"} {
		if _, err := os.Stat(filepath.Join(profileHost, profVer, rel)); err != nil {
			t.Errorf("profile snapshot missing %s: %v", rel, err)
		}
	}

	aiHost := filepath.Join(f.root, "ai-config", f.host)
	aiVer := latestVersion(t, aiHost)
	for _, rel := range []string{"home/.claude/settings.json", "home/.anchor/settings.json"} {
		if _, err := os.Stat(filepath.Join(aiHost, aiVer, rel)); err != nil {
			t.Errorf("ai snapshot missing %s: %v", rel, err)
		}
	}

	if _, err := os.Stat(filepath.Join(f.root, "secrets-age", f.host, "x.age")); err != nil {
		t.Errorf("secrets archive missing: %v", err)
	}

	// Profile and AI snapshots share one auto-generated onestop tag.
	profTag := readTag(t, filepath.Join(profileHost, profVer, "meta.yaml"))
	aiTag := readTag(t, filepath.Join(aiHost, aiVer, "meta.yaml"))
	if profTag == "" || profTag != aiTag {
		t.Errorf("shared tag mismatch: profile=%q ai=%q", profTag, aiTag)
	}
	if !strings.HasPrefix(profTag, "onestop-") {
		t.Errorf("auto tag should be onestop-<ts>: %q", profTag)
	}
}

func TestOnestopBackupDryRunWritesNothing(t *testing.T) {
	f := newOnestopFixture(t)

	out, errOut, err := runDotForTest("backup", "--yes", "--dry-run", "--to", f.root, "--scope", "profile,ai,secrets", "--include-secrets")
	if err != nil {
		t.Fatalf("dry-run backup: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	var found []string
	_ = filepath.WalkDir(f.root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && p != f.root {
			found = append(found, p)
		}
		return nil
	})
	if len(found) > 0 {
		t.Errorf("dry-run created files under the backup root: %v", found)
	}
}

func TestOnestopBackupRejectsUnknownScope(t *testing.T) {
	f := newOnestopFixture(t)
	_, _, err := runDotForTest("backup", "--yes", "--to", f.root, "--scope", "profile,bogus")
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown-scope error, got %v", err)
	}
}

func TestOnestopBackupCustomTagPropagates(t *testing.T) {
	f := newOnestopFixture(t)
	if _, _, err := runDotForTest("backup", "--yes", "--to", f.root, "--scope", "profile,ai", "--tag", "migrate-2026"); err != nil {
		t.Fatal(err)
	}
	profileHost := filepath.Join(f.root, "profiles", f.host)
	aiHost := filepath.Join(f.root, "ai-config", f.host)
	if tag := readTag(t, filepath.Join(profileHost, latestVersion(t, profileHost), "meta.yaml")); tag != "migrate-2026" {
		t.Errorf("profile tag = %q", tag)
	}
	if tag := readTag(t, filepath.Join(aiHost, latestVersion(t, aiHost), "meta.yaml")); tag != "migrate-2026" {
		t.Errorf("ai tag = %q", tag)
	}
}
