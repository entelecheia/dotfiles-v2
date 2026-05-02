package gdrivesync

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// intakeFixture sets up a synthetic mirror + workspace pair with a
// real .dotfiles/gdrive-sync layout, baseline.manifest seeded, and
// returns a Config ready for Intake().
type intakeFixture struct {
	t      *testing.T
	root   string
	local  string
	mirror string
	cfg    *Config
	runner *exec.Runner
}

func newIntakeFixture(t *testing.T) *intakeFixture {
	t.Helper()
	root := t.TempDir()
	local := filepath.Join(root, "workspace")
	mirror := filepath.Join(root, "mirror")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}

	paths := ResolveLocalPaths(local + "/")
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("EnsureLocalLayout: %v", err)
	}
	cfg := &Config{
		LocalPath:   local + "/",
		MirrorPath:  mirror + "/",
		LogFile:     filepath.Join(root, "gdrive-sync.log"),
		LockDir:     filepath.Join(root, "lock"),
		MaxDelete:   100,
		Propagation: DefaultPropagationPolicy(),
		LocalPaths:  paths,
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := exec.NewRunner(false, logger)
	return &intakeFixture{t: t, root: root, local: local, mirror: mirror, cfg: cfg, runner: runner}
}

// writeMirror creates a file under mirror/<rel> with body and returns
// its mtime so tests can seed baseline manifest entries.
func (f *intakeFixture) writeMirror(rel, body string) time.Time {
	f.t.Helper()
	abs := filepath.Join(f.mirror, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		f.t.Fatal(err)
	}
	return info.ModTime()
}

func (f *intakeFixture) seedBaseline(rel, body string, mtime time.Time) {
	f.t.Helper()
	existing, _ := LoadBaselineManifest(f.cfg.LocalPaths.BaselineFile)
	existing[rel] = Fingerprint{Size: int64(len(body)), Mtime: mtime}
	if err := SaveBaselineManifest(f.cfg.LocalPaths.BaselineFile, existing); err != nil {
		f.t.Fatal(err)
	}
}

func TestIntake_SkipsBaselineMatches(t *testing.T) {
	f := newIntakeFixture(t)
	body := "ours-from-push"
	mtime := f.writeMirror("projects/foo/note.md", body)
	f.seedBaseline("projects/foo/note.md", body, mtime)

	res, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{})
	if err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if len(res.Intaked) != 0 {
		t.Errorf("Intaked = %v, want empty (baseline match should skip)", res.Intaked)
	}
	if len(res.SkippedBase) != 1 || res.SkippedBase[0] != "projects/foo/note.md" {
		t.Errorf("SkippedBase = %v", res.SkippedBase)
	}
}

func TestIntake_StagesGdriveOriginNewFile(t *testing.T) {
	f := newIntakeFixture(t)
	f.writeMirror("from-cloud/note.md", "from drive")

	res, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{})
	if err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if len(res.Intaked) != 1 || res.Intaked[0] != "from-cloud/note.md" {
		t.Fatalf("Intaked = %v, want [from-cloud/note.md]", res.Intaked)
	}
	staged := filepath.Join(res.StagingDir, "from-cloud/note.md")
	if _, err := os.Stat(staged); err != nil {
		t.Errorf("staged file missing at %s: %v", staged, err)
	}
	// Workspace tree itself untouched.
	if _, err := os.Stat(filepath.Join(f.local, "from-cloud/note.md")); !os.IsNotExist(err) {
		t.Errorf("workspace got the file directly — intake must stage to inbox/gdrive only")
	}
}

func TestIntake_StagesGdriveOriginModifiedFile(t *testing.T) {
	f := newIntakeFixture(t)
	// We pushed v1 (recorded in baseline); GDrive editor changed it to v2.
	v1Mtime := f.writeMirror("doc.md", "v1-from-us")
	f.seedBaseline("doc.md", "v1-from-us", v1Mtime)
	// Now overwrite mirror with v2 (longer + later).
	time.Sleep(10 * time.Millisecond) // ensure mtime advances
	if err := os.WriteFile(filepath.Join(f.mirror, "doc.md"), []byte("v2-from-collaborator"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{})
	if err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if len(res.Intaked) != 1 {
		t.Errorf("Intaked = %v, want 1 (modified file)", res.Intaked)
	}
}

func TestIntake_SkipsImportsManifestMatches(t *testing.T) {
	f := newIntakeFixture(t)
	mtime := f.writeMirror("from-cloud.md", "from drive")

	// First intake — stages it.
	res1, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.Intaked) != 1 {
		t.Fatalf("first intake should stage 1; got %v", res1.Intaked)
	}

	// Operator processes the file (moves it out of staging).
	if err := os.RemoveAll(res1.StagingDir); err != nil {
		t.Fatal(err)
	}

	// Second intake — should NOT re-stage (imports manifest still has it).
	res2, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Intaked) != 0 {
		t.Errorf("second intake re-staged after operator processing: %v", res2.Intaked)
	}
	if len(res2.SkippedImports) != 1 {
		t.Errorf("SkippedImports = %v, want 1", res2.SkippedImports)
	}
	_ = mtime // referenced only to keep test self-explanatory
}

func TestIntake_RestagesOnUpstreamChange(t *testing.T) {
	f := newIntakeFixture(t)
	f.writeMirror("from-cloud.md", "v1-from-drive")

	res1, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{})
	if err != nil || len(res1.Intaked) != 1 {
		t.Fatalf("first intake setup failed: %v %v", err, res1)
	}

	time.Sleep(10 * time.Millisecond)
	// Mirror file changes upstream.
	if err := os.WriteFile(filepath.Join(f.mirror, "from-cloud.md"), []byte("v2-newer-from-drive"), 0o644); err != nil {
		t.Fatal(err)
	}

	res2, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Intaked) != 1 {
		t.Errorf("Intaked = %v, want 1 (upstream changed)", res2.Intaked)
	}
	// Two intake runs → two distinct timestamped subdirs.
	entries, err := os.ReadDir(filepath.Join(f.local, "inbox", "gdrive"))
	if err != nil {
		t.Fatal(err)
	}
	dirCount := 0
	for _, e := range entries {
		if e.IsDir() {
			dirCount++
		}
	}
	if dirCount != 2 {
		t.Errorf("got %d run-dirs in inbox/gdrive/, want 2", dirCount)
	}
}

func TestIntake_DetectsMirrorDeletion_Tombstone(t *testing.T) {
	f := newIntakeFixture(t)
	mtime := f.writeMirror("ephemeral.md", "ours")
	f.seedBaseline("ephemeral.md", "ours", mtime)
	// Mirror loses the file.
	if err := os.Remove(filepath.Join(f.mirror, "ephemeral.md")); err != nil {
		t.Fatal(err)
	}

	res, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tombstones) != 1 || res.Tombstones[0].RelPath != "ephemeral.md" {
		t.Errorf("Tombstones = %v, want [ephemeral.md]", res.Tombstones)
	}
	// Local tree must NOT have a corresponding deletion — local was authoritative.
	if _, err := os.Stat(f.local); err != nil {
		t.Errorf("local workspace missing: %v", err)
	}
	// Tombstone log on disk.
	tomb, err := LoadTombstones(f.cfg.LocalPaths.TombstonesFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(tomb) != 1 {
		t.Errorf("tombstones.log has %d entries, want 1", len(tomb))
	}
}

func TestIntake_NeverIntakesInboxOrDotfiles(t *testing.T) {
	f := newIntakeFixture(t)
	// Mirror somehow has these (operator pushed before always-on excludes
	// landed). Intake must refuse to echo them back.
	f.writeMirror(".dotfiles/gdrive-sync/secrets.txt", "should-never-cross")
	f.writeMirror("inbox/gdrive/2025-99-99/leftover.md", "should-never-cross")
	f.writeMirror("normal/file.md", "fine")

	res, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range res.Intaked {
		if strings.HasPrefix(rel, ".dotfiles") || strings.HasPrefix(rel, "inbox/gdrive") {
			t.Errorf("Intake leaked always-excluded path: %s", rel)
		}
	}
	if len(res.Intaked) != 1 || res.Intaked[0] != "normal/file.md" {
		t.Errorf("Intaked = %v, want [normal/file.md]", res.Intaked)
	}
}

func TestIntake_DryRun_NoSideEffects(t *testing.T) {
	f := newIntakeFixture(t)
	f.writeMirror("from-cloud.md", "drive")

	_, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	// No staging dir should exist.
	entries, err := os.ReadDir(filepath.Join(f.local, "inbox", "gdrive"))
	if err == nil && len(entries) > 0 {
		t.Errorf("dry-run created %d entries in inbox/gdrive/", len(entries))
	}
	// Imports manifest should still be empty.
	imp, _ := LoadImportsManifest(f.cfg.LocalPaths.ImportsFile)
	if len(imp) != 0 {
		t.Errorf("dry-run wrote imports manifest: %v", imp)
	}
}

func TestIntake_StrictMode_DetectsContentChangeWithSameSizeMtime(t *testing.T) {
	f := newIntakeFixture(t)
	body := "exactly10b"
	mtime := f.writeMirror("paranoid.md", body)
	// Baseline records strict-mode fingerprint of v1.
	v1FP, err := FingerprintFile(filepath.Join(f.mirror, "paranoid.md"), FingerprintStrict)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBaselineManifest(f.cfg.LocalPaths.BaselineFile, map[string]Fingerprint{
		"paranoid.md": v1FP,
	}); err != nil {
		t.Fatal(err)
	}

	// Replace with same-length content + same mtime — fast mode would
	// miss this, strict mode catches it.
	if err := os.WriteFile(filepath.Join(f.mirror, "paranoid.md"), []byte("ALTERED10b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(f.mirror, "paranoid.md"), mtime, mtime); err != nil {
		t.Fatal(err)
	}

	// Fast mode misses it.
	resFast, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{Strict: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(resFast.Intaked) != 0 {
		t.Logf("note: fast mode caught the change (FingerprintsCompatible re-hashes when manifest has sha) — %v", resFast.Intaked)
	}

	// Reset imports manifest before strict re-run so we observe baseline-vs-mirror only.
	if err := os.Remove(f.cfg.LocalPaths.ImportsFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := SaveImportsManifest(f.cfg.LocalPaths.ImportsFile, map[string]ImportEntry{}); err != nil {
		t.Fatal(err)
	}

	resStrict, err := Intake(context.Background(), f.runner, f.cfg, IntakeOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(resStrict.Intaked) != 1 {
		t.Errorf("strict-mode Intake didn't catch content change: %v", resStrict.Intaked)
	}
}

func TestRefreshBaseline_PopulatesFromLocalTree(t *testing.T) {
	f := newIntakeFixture(t)
	// Local tree gets some files.
	if err := os.MkdirAll(filepath.Join(f.local, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "notes/a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "notes/b.md"), []byte("bb"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .dotfiles/ and inbox/gdrive/ files must NOT land in baseline.
	if err := os.WriteFile(filepath.Join(f.local, ".dotfiles/secret.txt"), []byte("x"), 0o644); err != nil {
		// .dotfiles/ doesn't exist yet; create.
		if err := os.MkdirAll(filepath.Join(f.local, ".dotfiles"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.local, ".dotfiles/secret.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(f.local, "inbox/gdrive/old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "inbox/gdrive/old/staged.md"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RefreshBaseline(f.cfg, FingerprintFast); err != nil {
		t.Fatal(err)
	}
	base, err := LoadBaselineManifest(f.cfg.LocalPaths.BaselineFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := base["notes/a.md"]; !ok {
		t.Error("notes/a.md missing from baseline")
	}
	if _, ok := base["notes/b.md"]; !ok {
		t.Error("notes/b.md missing from baseline")
	}
	for k := range base {
		if strings.HasPrefix(k, ".dotfiles") || strings.HasPrefix(k, "inbox/gdrive") {
			t.Errorf("baseline includes always-excluded path: %s", k)
		}
	}
}
