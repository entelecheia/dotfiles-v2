package cli

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/gsync"
)

// gsyncCLIFixture isolates HOME/XDG so gsyncBootstrap resolves state,
// lock dir, and trees inside a temp sandbox, then returns the two trees.
type gsyncCLIFixture struct {
	home   string
	local  string
	mirror string
}

func newGsyncCLIFixture(t *testing.T) *gsyncCLIFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	local := filepath.Join(home, "workspace", "work")
	mirror := filepath.Join(home, "gdrive-workspace", "work")
	for _, dir := range []string{local, mirror} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	stateFile := filepath.Join(home, ".config", "dotfiles", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatal(err)
	}
	state := "modules:\n  gsync:\n    local_path: " + local + "\n    mirror_path: " + mirror + "\n"
	if err := os.WriteFile(stateFile, []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	return &gsyncCLIFixture{home: home, local: local, mirror: mirror}
}

// seedOldConflict creates <tree>/.sync-conflicts/<stamp>/ with one file,
// aged 40 days so the default 30-day prune cutoff selects it.
func (f *gsyncCLIFixture) seedOldConflict(t *testing.T, tree, stamp string) string {
	t.Helper()
	dir := filepath.Join(tree, ".sync-conflicts", stamp)
	if err := os.MkdirAll(filepath.Join(dir, "from-gdrive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "from-gdrive", "old.bin"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGsyncConflictsPruneCLI_DryRunThenYes(t *testing.T) {
	f := newGsyncCLIFixture(t)
	wsConflict := f.seedOldConflict(t, f.local, "2026-01-01T00-00-00Z")
	mirrorConflict := f.seedOldConflict(t, f.mirror, "2026-01-02T00-00-00Z")

	out, errOut, err := runDotForTest("gsync", "conflicts", "prune", "--dry-run")
	if err != nil {
		t.Fatalf("prune --dry-run: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "Would reclaim") {
		t.Errorf("dry-run output missing plan summary:\n%s", out)
	}
	for _, label := range []string{"workspace", "mirror"} {
		if !strings.Contains(out, label) {
			t.Errorf("dry-run plan missing %s tree section:\n%s", label, out)
		}
	}
	for _, dir := range []string{wsConflict, mirrorConflict} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("dry-run must not delete %s: %v", dir, err)
		}
	}

	out, errOut, err = runDotForTest("gsync", "conflicts", "prune", "--yes")
	if err != nil {
		t.Fatalf("prune --yes: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "pruned") {
		t.Errorf("prune output missing result:\n%s", out)
	}
	for _, dir := range []string{wsConflict, mirrorConflict} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s should be removed after prune --yes", dir)
		}
	}
}

func TestGsyncConflictsPruneCLI_LockHeldDeletesNothing(t *testing.T) {
	f := newGsyncCLIFixture(t)
	conflict := f.seedOldConflict(t, f.local, "2026-01-01T00-00-00Z")

	paths, err := gsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	release, err := gsync.AcquireLock(paths.LockDir)
	if err != nil {
		t.Fatalf("acquiring lock: %v", err)
	}
	defer release()

	out, errOut, err := runDotForTest("gsync", "conflicts", "prune", "--yes")
	if err != nil {
		t.Fatalf("prune under held lock should not error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "another sync is running") {
		t.Errorf("expected lock-held notice, got:\n%s", out)
	}
	if _, err := os.Stat(conflict); err != nil {
		t.Errorf("held lock must prevent deletion: %v", err)
	}
}

func TestGsyncPullCLI_StrictFlagWiring(t *testing.T) {
	if _, err := osexec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed; gsync preflight would refuse to run")
	}
	f := newGsyncCLIFixture(t)

	// Seed a baseline-tracked file whose mirror copy changed content while
	// preserving size+mtime — invisible to the default fast tier, visible
	// only under --strict. Must use an extension the default include-mode
	// filter syncs (binary payloads only).
	rel := "assets/data.png"
	mirrorAbs := filepath.Join(f.mirror, rel)
	localAbs := filepath.Join(f.local, rel)
	for _, abs := range []string{mirrorAbs, localAbs} {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("v1-bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	localPaths := gsync.ResolveLocalPaths(f.local + "/")
	if err := gsync.EnsureLocalLayout(localPaths); err != nil {
		t.Fatal(err)
	}
	base, err := gsync.FingerprintFile(mirrorAbs, gsync.FingerprintStrict)
	if err != nil {
		t.Fatal(err)
	}
	if err := gsync.SaveBaselineManifest(localPaths.BaselineFile, map[string]gsync.Fingerprint{rel: base}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(localAbs, base.Mtime, base.Mtime); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mirrorAbs, []byte("v2-BYTES"), 0o644); err != nil { // same length
		t.Fatal(err)
	}
	if err := os.Chtimes(mirrorAbs, base.Mtime, base.Mtime); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := runDotForTest("gsync", "pull", "--dry-run")
	if err != nil {
		t.Fatalf("pull --dry-run: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(out, "Updates from Drive") {
		t.Errorf("default fast tier should not plan a pull here:\n%s", out)
	}

	out, errOut, err = runDotForTest("gsync", "pull", "--strict", "--dry-run")
	if err != nil {
		t.Fatalf("pull --strict --dry-run: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "Updates from Drive: 1") {
		t.Errorf("--strict must reach PullTracked and plan the pull:\n%s", out)
	}
}

func TestGsyncMirrorCLI_SetsLocalConfigAndPrints(t *testing.T) {
	f := newGsyncCLIFixture(t)
	newMirror := filepath.Join(f.home, "Dropbox", "work")

	// Set the mirror.
	out, errOut, err := runDotForTest("gsync", "mirror", newMirror)
	if err != nil {
		t.Fatalf("gsync mirror set: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	if !strings.Contains(out, "mirror path set") || !strings.Contains(out, newMirror) {
		t.Errorf("set output unexpected:\n%s", out)
	}

	// Local config (authoritative for the current workspace) must carry it.
	localCfg := filepath.Join(f.local, ".dotfiles", "gdrive-sync", "config.yaml")
	data, err := os.ReadFile(localCfg)
	if err != nil {
		t.Fatalf("read local config: %v", err)
	}
	if !strings.Contains(string(data), "mirror_path: "+newMirror) {
		t.Errorf("local config missing mirror_path:\n%s", data)
	}

	// No-arg prints the resolved mirror.
	out, _, err = runDotForTest("gsync", "mirror")
	if err != nil {
		t.Fatalf("gsync mirror print: %v", err)
	}
	if !strings.Contains(out, newMirror) {
		t.Errorf("print output should show %q:\n%s", newMirror, out)
	}
}

func TestGsyncMirrorCLI_PrintAndDryRunAreReadOnly(t *testing.T) {
	f := newGsyncCLIFixture(t)
	store := filepath.Join(f.local, ".dotfiles", "gdrive-sync")

	// No-arg print on a fresh workspace must not create the local layout.
	if _, _, err := runDotForTest("gsync", "mirror"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Errorf("no-arg print created the local gsync layout: %v", err)
	}

	// --dry-run set: shows the would-be path, still no layout, no .gitignore.
	newMirror := filepath.Join(f.home, "Dropbox", "work")
	out, _, err := runDotForTest("gsync", "mirror", newMirror, "--dry-run")
	if err != nil {
		t.Fatalf("gsync mirror --dry-run: %v", err)
	}
	if !strings.Contains(out, "[dry-run]") || !strings.Contains(out, newMirror) {
		t.Errorf("dry-run output unexpected:\n%s", out)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Errorf("dry-run created the local gsync layout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.local, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("dry-run created .gitignore: %v", err)
	}
}

func TestGsyncMirrorCLI_HonorsHomeOverride(t *testing.T) {
	f := newGsyncCLIFixture(t)
	other := t.TempDir() // a different user's home
	if err := os.MkdirAll(filepath.Join(other, ".config", "dotfiles"), 0o755); err != nil {
		t.Fatal(err)
	}

	// ~ in the path must expand against --home, and global state must be
	// written for that home — not the current HOME.
	out, errOut, err := runDotForTest("gsync", "mirror", "~/Dropbox/work", "--home", other)
	if err != nil {
		t.Fatalf("gsync mirror --home: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	wantMirror := filepath.Join(other, "Dropbox", "work")
	if !strings.Contains(out, wantMirror) {
		t.Errorf("~ should expand against --home %q:\n%s", wantMirror, out)
	}
	otherCfg, err := os.ReadFile(filepath.Join(other, ".config", "dotfiles", "config.yaml"))
	if err != nil {
		t.Fatalf("read --home state: %v", err)
	}
	if !strings.Contains(string(otherCfg), "mirror_path: "+wantMirror) {
		t.Errorf("--home global state missing mirror_path:\n%s", otherCfg)
	}
	// The current user's state must NOT have been written with this mirror.
	if cur, _ := os.ReadFile(filepath.Join(f.home, ".config", "dotfiles", "config.yaml")); strings.Contains(string(cur), wantMirror) {
		t.Errorf("current-user state wrongly written under --home:\n%s", cur)
	}
}
