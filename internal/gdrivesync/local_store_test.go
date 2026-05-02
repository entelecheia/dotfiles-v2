package gdrivesync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

func TestPropagationPolicy_Validate(t *testing.T) {
	cases := []struct {
		name    string
		policy  PropagationPolicy
		wantErr bool
	}{
		{"default safe", DefaultPropagationPolicy(), false},
		{"create only", PropagationPolicy{Create: true}, false},
		{"update only", PropagationPolicy{Update: true}, false},
		{"delete only", PropagationPolicy{Delete: true}, false},
		{"all on", PropagationPolicy{Create: true, Update: true, Delete: true}, false},
		{"all off", PropagationPolicy{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestPropagationPolicy_String(t *testing.T) {
	cases := []struct {
		policy PropagationPolicy
		want   string
	}{
		{DefaultPropagationPolicy(), "create+update (delete off)"},
		{PropagationPolicy{Create: true, Update: true, Delete: true}, "create+update+delete"},
		{PropagationPolicy{Delete: true}, "delete (create,update off)"},
		{PropagationPolicy{}, "(none — invalid)"},
	}
	for _, tc := range cases {
		if got := tc.policy.String(); got != tc.want {
			t.Errorf("String(%+v) = %q, want %q", tc.policy, got, tc.want)
		}
	}
}

func TestResolveLocalPaths_LayoutShape(t *testing.T) {
	root := "/tmp/wk"
	paths := ResolveLocalPaths(root + "/")

	wantStore := filepath.Join(root, ".dotfiles", "gdrive-sync")
	if paths.StoreDir != wantStore {
		t.Errorf("StoreDir = %q, want %q", paths.StoreDir, wantStore)
	}
	if paths.WorkspaceRoot != root {
		t.Errorf("WorkspaceRoot = %q, want %q", paths.WorkspaceRoot, root)
	}
	for _, want := range []string{
		filepath.Join(wantStore, "config.yaml"),
		filepath.Join(wantStore, "exclude.txt"),
		filepath.Join(wantStore, "ignore.txt"),
		filepath.Join(wantStore, "shared-excludes.dyn.conf"),
		filepath.Join(wantStore, "baseline.manifest"),
		filepath.Join(wantStore, "imports.manifest"),
		filepath.Join(wantStore, "tombstones.log"),
		filepath.Join(wantStore, "log", "gdrive-sync.log"),
	} {
		seen := false
		for _, got := range []string{
			paths.ConfigFile, paths.ExcludeFile, paths.IgnoreFile,
			paths.SharedDynFile, paths.BaselineFile, paths.ImportsFile,
			paths.TombstonesFile, paths.LogFile,
		} {
			if got == want {
				seen = true
				break
			}
		}
		if !seen {
			t.Errorf("missing path %q in resolved layout: %+v", want, paths)
		}
	}
}

func TestEnsureLocalLayout_CreatesAllDefaults(t *testing.T) {
	tmp := t.TempDir()
	paths := ResolveLocalPaths(tmp)
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("EnsureLocalLayout: %v", err)
	}
	for _, p := range []string{paths.StoreDir, paths.LogDir} {
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			t.Errorf("dir not created: %s (err %v)", p, err)
		}
	}
	for _, p := range []string{paths.ExcludeFile, paths.IgnoreFile, paths.BaselineFile, paths.ImportsFile, paths.TombstonesFile} {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("missing file: %s (err %v)", p, err)
			continue
		}
		if len(body) == 0 {
			t.Errorf("file %s should have a header, got empty", p)
		}
	}
	// Workspace .gitignore must contain the /.dotfiles/ entry.
	body, err := os.ReadFile(paths.WorkspaceIgnore)
	if err != nil {
		t.Fatalf("reading workspace .gitignore: %v", err)
	}
	if !strings.Contains(string(body), gitignoreEntry) {
		t.Errorf(".gitignore missing %q\n--- got ---\n%s", gitignoreEntry, body)
	}
}

func TestEnsureLocalLayout_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	paths := ResolveLocalPaths(tmp)
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("first EnsureLocalLayout: %v", err)
	}
	// Mutate the exclude file; a second call must not overwrite.
	custom := []byte("# operator-edited\n")
	if err := os.WriteFile(paths.ExcludeFile, custom, 0644); err != nil {
		t.Fatalf("seed custom: %v", err)
	}
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("second EnsureLocalLayout: %v", err)
	}
	got, err := os.ReadFile(paths.ExcludeFile)
	if err != nil {
		t.Fatalf("read after second call: %v", err)
	}
	if string(got) != string(custom) {
		t.Errorf("exclude file rewritten on second EnsureLocalLayout; want untouched %q got %q", custom, got)
	}
}

func TestAppendGitignoreLine_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	gi := filepath.Join(tmp, ".gitignore")

	if err := appendGitignoreLine(gi, "/.dotfiles/"); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := appendGitignoreLine(gi, "/.dotfiles/"); err != nil {
		t.Fatalf("second append: %v", err)
	}

	body, err := os.ReadFile(gi)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if n := strings.Count(string(body), "/.dotfiles/"); n != 1 {
		t.Errorf("got %d copies of /.dotfiles/, want 1\n%s", n, body)
	}
}

func TestAppendGitignoreLine_PreservesExistingContent(t *testing.T) {
	tmp := t.TempDir()
	gi := filepath.Join(tmp, ".gitignore")
	existing := "# project ignores\n.env\n*.log\n"
	if err := os.WriteFile(gi, []byte(existing), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := appendGitignoreLine(gi, "/.dotfiles/"); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := os.ReadFile(gi)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), ".env") || !strings.Contains(string(got), "*.log") {
		t.Errorf("existing entries lost\n%s", got)
	}
	if !strings.Contains(string(got), "/.dotfiles/") {
		t.Errorf("new entry missing\n%s", got)
	}
}

func TestLocalConfig_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	paths := ResolveLocalPaths(tmp)

	original := &LocalConfig{
		MirrorPath:     "/x/mirror",
		Propagation:    PropagationPolicy{Create: true, Update: true, Delete: true},
		MaxDelete:      500,
		Interval:       600,
		PullInterval:   900,
		Paused:         true,
		SharedExcludes: []string{"projects/foo", "projects/bar"},
	}
	if err := SaveLocalConfig(paths, original); err != nil {
		t.Fatalf("SaveLocalConfig: %v", err)
	}

	got, ok, err := LoadLocalConfig(paths)
	if err != nil {
		t.Fatalf("LoadLocalConfig: %v", err)
	}
	if !ok {
		t.Fatal("LoadLocalConfig reported missing after save")
	}
	if got.MirrorPath != original.MirrorPath ||
		got.Propagation != original.Propagation ||
		got.MaxDelete != original.MaxDelete ||
		got.Interval != original.Interval ||
		got.PullInterval != original.PullInterval ||
		got.Paused != original.Paused ||
		len(got.SharedExcludes) != len(original.SharedExcludes) {
		t.Errorf("round-trip mismatch:\n  got %+v\n  want %+v", got, original)
	}
}

func TestLocalConfig_RefusesEmptyPropagationOnSave(t *testing.T) {
	tmp := t.TempDir()
	paths := ResolveLocalPaths(tmp)

	cfg := &LocalConfig{Propagation: PropagationPolicy{}} // all-false
	if err := SaveLocalConfig(paths, cfg); err == nil {
		t.Error("SaveLocalConfig should reject all-false propagation")
	}
}

func TestLoadLocalConfig_MissingReturnsFalse(t *testing.T) {
	tmp := t.TempDir()
	paths := ResolveLocalPaths(tmp)

	got, ok, err := LoadLocalConfig(paths)
	if err != nil {
		t.Fatalf("LoadLocalConfig: %v", err)
	}
	if ok {
		t.Errorf("LoadLocalConfig returned ok=true on missing file: %+v", got)
	}
}

func TestMigrateFromGlobal_PopulatesLocalLayout(t *testing.T) {
	tmp := t.TempDir()
	paths := ResolveLocalPaths(tmp)

	state := &config.UserState{}
	state.Modules.GdriveSync.MirrorPath = "/legacy/mirror"
	state.Modules.GdriveSync.MaxDelete = 250
	state.Modules.GdriveSync.Interval = 600
	state.Modules.GdriveSync.Paused = true
	state.Modules.GdriveSync.SharedExcludes = []string{"projects/legacy"}

	cfg, err := MigrateFromGlobal(state, paths)
	if err != nil {
		t.Fatalf("MigrateFromGlobal: %v", err)
	}
	// Migrated fields:
	if cfg.MirrorPath != "/legacy/mirror" || cfg.MaxDelete != 250 || cfg.Interval != 600 || !cfg.Paused {
		t.Errorf("migrated fields wrong: %+v", cfg)
	}
	if len(cfg.SharedExcludes) != 1 || cfg.SharedExcludes[0] != "projects/legacy" {
		t.Errorf("SharedExcludes not migrated: %+v", cfg.SharedExcludes)
	}
	// Defaults filled in:
	want := DefaultPropagationPolicy()
	if cfg.Propagation != want {
		t.Errorf("Propagation default = %+v, want %+v", cfg.Propagation, want)
	}
	// On-disk artifacts present:
	for _, p := range []string{paths.ConfigFile, paths.ExcludeFile, paths.IgnoreFile, paths.BaselineFile, paths.ImportsFile, paths.TombstonesFile} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing post-migration file %s: %v", p, err)
		}
	}
}

func TestLoadOrMigrateLocalConfig_MigrationRunsOnce(t *testing.T) {
	tmp := t.TempDir()
	paths := ResolveLocalPaths(tmp)

	state := &config.UserState{}
	state.Modules.GdriveSync.MirrorPath = "/legacy/mirror"

	first, err := LoadOrMigrateLocalConfig(state, paths)
	if err != nil {
		t.Fatalf("first LoadOrMigrateLocalConfig: %v", err)
	}
	// Mutate the disk-resident config so we can detect re-migration.
	first.MirrorPath = "/edited/by/operator"
	if err := SaveLocalConfig(paths, first); err != nil {
		t.Fatalf("SaveLocalConfig: %v", err)
	}

	// Second call must see the operator's edit, NOT the global state.
	second, err := LoadOrMigrateLocalConfig(state, paths)
	if err != nil {
		t.Fatalf("second LoadOrMigrateLocalConfig: %v", err)
	}
	if second.MirrorPath != "/edited/by/operator" {
		t.Errorf("re-migration overwrote local edits: MirrorPath = %q", second.MirrorPath)
	}
}

func TestLocalState_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	paths := ResolveLocalPaths(tmp)

	st := &LocalState{LastIntakeTSDir: "2026-05-02T10-00-00Z"}
	if err := SaveLocalState(paths, st); err != nil {
		t.Fatalf("SaveLocalState: %v", err)
	}
	got, err := LoadLocalState(paths)
	if err != nil {
		t.Fatalf("LoadLocalState: %v", err)
	}
	if got.LastIntakeTSDir != "2026-05-02T10-00-00Z" {
		t.Errorf("LastIntakeTSDir round-trip: got %q want %q", got.LastIntakeTSDir, "2026-05-02T10-00-00Z")
	}
}

func TestLoadLocalState_MissingReturnsZero(t *testing.T) {
	tmp := t.TempDir()
	paths := ResolveLocalPaths(tmp)

	got, err := LoadLocalState(paths)
	if err != nil {
		t.Fatalf("LoadLocalState: %v", err)
	}
	if got == nil || !got.LastPush.IsZero() || !got.LastIntake.IsZero() {
		t.Errorf("expected zero state, got %+v", got)
	}
}
