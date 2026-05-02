package gdrivesync

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

func newTestConfig(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	excludes, err := MaterializeExcludesFile(dir)
	if err != nil {
		t.Fatalf("MaterializeExcludesFile: %v", err)
	}
	return &Config{
		LocalPath:    "/tmp/test-local/",
		MirrorPath:   "/tmp/test-mirror/",
		ExcludesFile: excludes,
		LogFile:      "/tmp/test.log",
		LockDir:      "/tmp/test.lock",
		MaxDelete:    1000,
	}
}

// newIsolatedState returns a UserState whose gdrive-sync LocalPath
// points at a fresh per-test directory. Use this for any test that
// calls ResolveConfig — without it the test would migrate against the
// real ~/workspace/work tree and pollute it (or pick up state from
// previous test runs).
func newIsolatedState(t *testing.T) *config.UserState {
	t.Helper()
	state := &config.UserState{}
	state.Modules.GdriveSync.LocalPath = t.TempDir()
	return state
}

func TestPullArgs_WorkspaceAuthoritative(t *testing.T) {
	cfg := newTestConfig(t)
	conflict := &ConflictDir{Timestamp: "2026-05-01T12-00-00Z"}

	args := pullArgs(cfg, conflict, "", false)

	// Must include --update (workspace-authoritative).
	if !slices.Contains(args, "--update") {
		t.Errorf("pullArgs missing --update; got %v", args)
	}

	// Must NEVER include --delete or --delete-after (would destroy workspace-only adds).
	for _, forbidden := range []string{"--delete", "--delete-after", "--delete-excluded"} {
		if slices.Contains(args, forbidden) {
			t.Errorf("pullArgs leaked %q — workspace adds would be destroyed", forbidden)
		}
	}

	// Must include --backup --backup-dir pointing at from-gdrive subtree.
	if !slices.Contains(args, "--backup") {
		t.Errorf("pullArgs missing --backup; got %v", args)
	}
	wantBackup := "--backup-dir=.sync-conflicts/2026-05-01T12-00-00Z/from-gdrive"
	if !slices.Contains(args, wantBackup) {
		t.Errorf("pullArgs missing %q; got %v", wantBackup, args)
	}

	// Source = mirror, destination = local; both with trailing slashes.
	if args[len(args)-2] != cfg.MirrorPath || args[len(args)-1] != cfg.LocalPath {
		t.Errorf("pullArgs source/dest order wrong: ...%v %v (want mirror→local)",
			args[len(args)-2], args[len(args)-1])
	}
}

func TestPullArgs_DryRunPlumbing(t *testing.T) {
	cfg := newTestConfig(t)
	conflict := &ConflictDir{Timestamp: "ts"}

	noDry := pullArgs(cfg, conflict, "", false)
	if slices.Contains(noDry, "--dry-run") {
		t.Error("pullArgs(dryRun=false) leaked --dry-run")
	}

	dry := pullArgs(cfg, conflict, "", true)
	if !slices.Contains(dry, "--dry-run") {
		t.Errorf("pullArgs(dryRun=true) missing --dry-run; got %v", dry)
	}
}

func TestPushArgs_DeletePropagationWithSafetyCap(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.MaxDelete = 250
	conflict := &ConflictDir{Timestamp: "2026-05-01T12-00-00Z"}

	args := pushArgs(cfg, conflict, "", false)

	if !slices.Contains(args, "--delete-after") {
		t.Errorf("pushArgs missing --delete-after; got %v", args)
	}

	// --max-delete=N where N matches cfg.MaxDelete.
	wantMax := "--max-delete=" + strconv.Itoa(cfg.MaxDelete)
	if !slices.Contains(args, wantMax) {
		t.Errorf("pushArgs missing %q; got %v", wantMax, args)
	}

	// from-workspace backup subdir on the mirror side.
	wantBackup := "--backup-dir=.sync-conflicts/2026-05-01T12-00-00Z/from-workspace"
	if !slices.Contains(args, wantBackup) {
		t.Errorf("pushArgs missing %q; got %v", wantBackup, args)
	}

	// --update belongs on pull, never push.
	if slices.Contains(args, "--update") {
		t.Error("pushArgs leaked --update — push must overwrite based on mtime+size, not skip-newer")
	}

	// Source = local, destination = mirror.
	if args[len(args)-2] != cfg.LocalPath || args[len(args)-1] != cfg.MirrorPath {
		t.Errorf("pushArgs source/dest order wrong: ...%v %v (want local→mirror)",
			args[len(args)-2], args[len(args)-1])
	}
}

func TestMigrateArgs_NoDeleteNoUpdate(t *testing.T) {
	cfg := newTestConfig(t)
	args := migrateArgs(cfg, "", false)

	for _, forbidden := range []string{"--delete", "--delete-after", "--update"} {
		if slices.Contains(args, forbidden) {
			t.Errorf("migrateArgs leaked %q — migrate must be additive only", forbidden)
		}
	}

	// Source=mirror, dest=local (one-shot ingest).
	if args[len(args)-2] != cfg.MirrorPath || args[len(args)-1] != cfg.LocalPath {
		t.Errorf("migrateArgs source/dest wrong: ...%v %v (want mirror→local)",
			args[len(args)-2], args[len(args)-1])
	}
}

func TestResolveConfig_Defaults(t *testing.T) {
	state := newIsolatedState(t)
	cfg, err := ResolveConfig(state)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	// LocalPath comes from the isolated tempdir, but ResolveConfig
	// must have suffix-normalized it to a trailing slash.
	if !strings.HasSuffix(cfg.LocalPath, "/") {
		t.Errorf("LocalPath missing trailing slash: %q", cfg.LocalPath)
	}
	if !strings.HasSuffix(cfg.MirrorPath, "gdrive-workspace/work/") {
		t.Errorf("MirrorPath default wrong: %s", cfg.MirrorPath)
	}
	if cfg.MaxDelete != defaultMaxDelete {
		t.Errorf("MaxDelete default = %d, want %d", cfg.MaxDelete, defaultMaxDelete)
	}
	if cfg.ExcludesFile == "" {
		t.Error("ExcludesFile not resolved")
	}
	// Default propagation policy is the safe baseline.
	want := DefaultPropagationPolicy()
	if cfg.Propagation != want {
		t.Errorf("Propagation = %+v, want %+v", cfg.Propagation, want)
	}
	// LocalPaths is always populated so callers can drill into the .dotfiles store.
	if cfg.LocalPaths == nil || cfg.LocalPaths.StoreDir == "" {
		t.Errorf("LocalPaths not resolved: %+v", cfg.LocalPaths)
	}
}

func TestResolveConfig_StateOverrides(t *testing.T) {
	tmp := t.TempDir()
	state := &config.UserState{}
	state.Modules.GdriveSync.LocalPath = tmp
	state.Modules.GdriveSync.MirrorPath = "/custom/mirror"
	state.Modules.GdriveSync.MaxDelete = 50

	cfg, err := ResolveConfig(state)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if cfg.LocalPath != tmp+"/" {
		t.Errorf("LocalPath = %q, want %q", cfg.LocalPath, tmp+"/")
	}
	if cfg.MirrorPath != "/custom/mirror/" {
		t.Errorf("MirrorPath = %q, want trailing-slashed /custom/mirror/", cfg.MirrorPath)
	}
	if cfg.MaxDelete != 50 {
		t.Errorf("MaxDelete = %d, want 50", cfg.MaxDelete)
	}
}
