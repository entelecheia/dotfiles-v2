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
	writeCLITestFile(t, filepath.Join(home, ".ssh", "id_ed25519"), "identity-material")
	writeCLITestFile(t, filepath.Join(home, ".local", "share", "dotfiles-secrets", "90-secrets.sh.age"), "export SECRET=1\n")

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

	if _, err := os.Stat(filepath.Join(f.root, "secrets-age", f.host, "90-secrets.sh.age")); err != nil {
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

func TestOnestopRestoreCrossHost(t *testing.T) {
	f := newOnestopFixture(t)

	if _, _, err := runDotForTest("backup", "--yes", "--to", f.root, "--scope", "profile,ai,secrets", "--include-secrets"); err != nil {
		t.Fatal(err)
	}
	// Move every per-host tree to a fake other host.
	for _, tree := range []string{"profiles", "ai-config", "secrets-age"} {
		if err := os.Rename(filepath.Join(f.root, tree, f.host), filepath.Join(f.root, tree, "otherhost")); err != nil {
			t.Fatal(err)
		}
	}

	statePath := filepath.Join(f.home, ".config", "dotfiles", "config.yaml")
	writeCLITestFile(t, statePath, "name: mutated\n")
	writeCLITestFile(t, filepath.Join(f.home, ".claude", "settings.json"), `{"model":"mutated"}`)

	// stubAge so the secrets step "decrypts" by copying.
	stubAge(t, false)

	out, errOut, err := runDotForTest("restore", "--yes", "--from", f.root, "--host", "otherhost",
		"--scope", "profile,ai,secrets", "--include-secrets")
	if err != nil {
		t.Fatalf("restore: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	if !strings.Contains(out, "one-stop restore complete") {
		t.Errorf("missing completion line:\n%s", out)
	}
	got, _ := os.ReadFile(statePath)
	if string(got) != "name: original\n" {
		t.Errorf("state not restored: %q", got)
	}
	claude, _ := os.ReadFile(filepath.Join(f.home, ".claude", "settings.json"))
	if string(claude) != `{"model":"opus"}` {
		t.Errorf("claude settings not restored: %q", claude)
	}
	shell, err := os.ReadFile(filepath.Join(f.home, ".config", "shell", "90-secrets.sh"))
	if err != nil || string(shell) != "export SECRET=1\n" {
		t.Errorf("shell secrets not restored: %q err=%v", shell, err)
	}
}

func TestOnestopRestoreProfileFailureAborts(t *testing.T) {
	f := newOnestopFixture(t)
	// Only an AI snapshot exists — no profile tree.
	if _, _, err := runDotForTest("backup", "--yes", "--to", f.root, "--scope", "ai"); err != nil {
		t.Fatal(err)
	}

	// profile is unavailable, so explicitly requesting it must fail fast.
	_, _, err := runDotForTest("restore", "--yes", "--from", f.root, "--scope", "profile,ai")
	if err == nil || !strings.Contains(err.Error(), "profile") {
		t.Fatalf("expected unavailable-profile error, got %v", err)
	}

	// ai-only restore succeeds.
	writeCLITestFile(t, filepath.Join(f.home, ".claude", "settings.json"), `{"model":"mutated"}`)
	out, errOut, err := runDotForTest("restore", "--yes", "--from", f.root, "--scope", "ai")
	if err != nil {
		t.Fatalf("ai-only restore: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	claude, _ := os.ReadFile(filepath.Join(f.home, ".claude", "settings.json"))
	if string(claude) != `{"model":"opus"}` {
		t.Errorf("claude settings not restored: %q", claude)
	}
}

func TestOnestopRestorePinsSessionRoot(t *testing.T) {
	f := newOnestopFixture(t)

	// The snapshot's config.yaml carries another machine's backup_root.
	writeCLITestFile(t, filepath.Join(f.home, ".config", "dotfiles", "config.yaml"),
		"name: original\nmodules:\n  macapps:\n    backup_root: /nonexistent/other-machine-root\n")
	if _, _, err := runDotForTest("backup", "--yes", "--to", f.root, "--scope", "profile,ai"); err != nil {
		t.Fatal(err)
	}

	// Local state has no backup_root; restore from --from must keep using
	// the session root for the AI step even after profile restore rewrote
	// config.yaml with the foreign backup_root.
	writeCLITestFile(t, filepath.Join(f.home, ".config", "dotfiles", "config.yaml"), "name: local\n")
	writeCLITestFile(t, filepath.Join(f.home, ".claude", "settings.json"), `{"model":"mutated"}`)

	out, errOut, err := runDotForTest("restore", "--yes", "--from", f.root, "--scope", "profile,ai")
	if err != nil {
		t.Fatalf("restore: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	got, _ := os.ReadFile(filepath.Join(f.home, ".config", "dotfiles", "config.yaml"))
	if !strings.Contains(string(got), "/nonexistent/other-machine-root") {
		t.Fatalf("profile restore should have brought back the foreign config: %q", got)
	}
	claude, _ := os.ReadFile(filepath.Join(f.home, ".claude", "settings.json"))
	if string(claude) != `{"model":"opus"}` {
		t.Errorf("ai step did not use the pinned session root: %q", claude)
	}
}

func TestOnestopRestoreNoBackupsAbortsBeforeChanges(t *testing.T) {
	f := newOnestopFixture(t)
	empty := t.TempDir()
	_, _, err := runDotForTest("restore", "--yes", "--from", empty)
	if err == nil || !strings.Contains(err.Error(), "no backups found") {
		t.Fatalf("expected preflight error, got %v", err)
	}
	_ = f
}

func TestOnestopRestoreUnmatchedSecretArchiveFails(t *testing.T) {
	f := newOnestopFixture(t)
	// An archive that matches no secretEntries name (e.g. an SSH key from a
	// host with a different ssh.key_name).
	writeCLITestFile(t, filepath.Join(f.home, ".local", "share", "dotfiles-secrets", "id_rsa.age"), "foreign-key")
	if _, _, err := runDotForTest("backup", "--yes", "--to", f.root, "--scope", "secrets"); err != nil {
		t.Fatal(err)
	}
	// x.age matches no secretEntries name → wizard must flag the step.
	stubAge(t, false)
	_, _, err := runDotForTest("restore", "--yes", "--from", f.root, "--scope", "secrets")
	if err == nil || !strings.Contains(err.Error(), "restore step(s) failed") {
		t.Fatalf("expected failed-step error for unmatched archive, got %v", err)
	}
}
