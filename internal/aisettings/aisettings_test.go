package aisettings

import (
	"archive/tar"
	"compress/gzip"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func testEngine(t *testing.T) (*Engine, string, string) {
	t.Helper()
	home := t.TempDir()
	root := t.TempDir()
	return &Engine{
		Runner:   exec.NewRunner(false, slog.Default()),
		HomeDir:  home,
		Root:     root,
		Hostname: "testhost",
		User:     "tester",
	}, home, root
}

func TestBackupRestoreSkipsAuthByDefault(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("model = \"gpt\"\n"))
	mustWrite(t, filepath.Join(home, ".codex", "auth.json"), []byte(`{"token":"secret"}`))
	mustWrite(t, filepath.Join(home, ".codex", "skills", "mine", "SKILL.md"), []byte("# mine"))

	snap, err := eng.Backup(BackupOptions{})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snap.Path, "home", ".codex", "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth should be excluded by default, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(snap.Path, "home", ".codex", "skills")); !os.IsNotExist(err) {
		t.Fatalf("skill directories should be excluded, stat err=%v", err)
	}

	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("mutated\n"))
	if _, err := eng.Restore(RestoreOptions{Version: snap.Version}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "model = \"gpt\"\n" {
		t.Fatalf("restored config = %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "auth.json")); err != nil {
		t.Fatalf("excluded auth should not be deleted by restore: %v", err)
	}
}

func TestRestoreLatestAlias(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("model = \"gpt\"\n"))

	snap, err := eng.Backup(BackupOptions{})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("mutated\n"))
	restored, err := eng.Restore(RestoreOptions{Version: "latest"})
	if err != nil {
		t.Fatalf("restore latest alias: %v", err)
	}
	if restored.Version != snap.Version {
		t.Fatalf("restored version = %q, want %q", restored.Version, snap.Version)
	}
	got, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "model = \"gpt\"\n" {
		t.Fatalf("restored config = %q", got)
	}
}

func TestCountTreeUsesRelativeRootExclusion(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "config.json"), []byte(`{"ok":true}`))

	info, err := os.Lstat(src)
	if err != nil {
		t.Fatal(err)
	}
	files, bytes, err := countTree(src, info, ".claude/session-env")
	if err != nil {
		t.Fatalf("count tree: %v", err)
	}
	if files != 0 || bytes != 0 {
		t.Fatalf("excluded relative root counted files=%d bytes=%d", files, bytes)
	}
}

func TestIncludeAuthBackup(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "auth.json"), []byte(`{"token":"secret"}`))
	snap, err := eng.Backup(BackupOptions{IncludeAuth: true})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snap.Path, "home", ".codex", "auth.json")); err != nil {
		t.Fatalf("auth should be included with IncludeAuth: %v", err)
	}
}

func TestEntriesIncludeAntigravityAndKeepAuthOptional(t *testing.T) {
	withoutAuth := Entries(false)
	if !hasEntry(withoutAuth, "antigravity", ".gemini/antigravity-cli/settings.json") {
		t.Fatalf("antigravity CLI settings entry missing: %+v", withoutAuth)
	}
	if !hasEntry(withoutAuth, "antigravity", ".gemini/GEMINI.md") {
		t.Fatalf("antigravity global instructions entry missing: %+v", withoutAuth)
	}
	if hasEntry(withoutAuth, "antigravity", ".gemini/oauth_creds.json") {
		t.Fatalf("antigravity OAuth credentials should be auth-only: %+v", withoutAuth)
	}

	withAuth := Entries(true)
	if !hasEntry(withAuth, "antigravity", ".gemini/oauth_creds.json") {
		t.Fatalf("antigravity OAuth credentials missing with IncludeAuth: %+v", withAuth)
	}
	if !hasEntry(withAuth, "antigravity", ".gemini/google_accounts.json") {
		t.Fatalf("antigravity account cache missing with IncludeAuth: %+v", withAuth)
	}
}

func TestEntriesExcludeSkillRuntimeDirectories(t *testing.T) {
	entries := Entries(true)
	excluded := []string{
		".claude/skills",
		".codex/skills",
		".agents/.skill-lock.json",
		".agents/skills",
		".gemini/skills",
		".gemini/antigravity/skills",
	}
	for _, path := range excluded {
		if hasEntryPath(entries, path) {
			t.Fatalf("skill runtime path %s should not be archived: %+v", path, entries)
		}
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".claude", "commands", "writer.md"), []byte("# writer"))
	archive := filepath.Join(t.TempDir(), "ai.tar.gz")
	if _, err := eng.Export(archive, BackupOptions{}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("archive missing: %v", err)
	}

	newHome := t.TempDir()
	importer := &Engine{Runner: exec.NewRunner(false, slog.Default()), HomeDir: newHome, Hostname: "h", Root: t.TempDir()}
	if _, err := importer.Import(archive, RestoreOptions{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(newHome, ".claude", "commands", "writer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# writer" {
		t.Fatalf("imported content = %q", got)
	}
}

func TestImportRejectsPathTraversal(t *testing.T) {
	eng, _, _ := testEngine(t)
	archive := filepath.Join(t.TempDir(), "bad.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o644, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Import(archive, RestoreOptions{}); err == nil {
		t.Fatal("expected path traversal error")
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasEntry(entries []Entry, tool, path string) bool {
	for _, entry := range entries {
		if entry.Tool == tool && entry.Path == path {
			return true
		}
	}
	return false
}

func hasEntryPath(entries []Entry, path string) bool {
	for _, entry := range entries {
		if entry.Path == path {
			return true
		}
	}
	return false
}

func TestRestorePreservesExcludedLiveFiles(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "prompts", "draft.md"), []byte("v1"))

	sum, err := eng.Backup(BackupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// A session log inside the managed dir: excluded from snapshots, but it
	// must survive restore via the pre-restore backup.
	logPath := filepath.Join(home, ".codex", "prompts", "session.jsonl")
	mustWrite(t, logPath, []byte("precious-session-data"))
	mustWrite(t, filepath.Join(home, ".codex", "prompts", "draft.md"), []byte("v2"))

	rsum, err := eng.Restore(RestoreOptions{Version: sum.Version})
	if err != nil {
		t.Fatal(err)
	}
	if rsum.PreBackupPath == "" {
		t.Fatal("PreBackupPath not set")
	}
	preserved, err := os.ReadFile(filepath.Join(rsum.PreBackupPath, ".codex", "prompts", "session.jsonl"))
	if err != nil || string(preserved) != "precious-session-data" {
		t.Errorf("excluded live file not preserved: %q err=%v", preserved, err)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".codex", "prompts", "draft.md"))
	if string(got) != "v1" {
		t.Errorf("restore content: %q", got)
	}
}

func TestRestoreUsesSinglePreBackupDir(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("a"))
	mustWrite(t, filepath.Join(home, ".codex", "AGENTS.md"), []byte("b"))

	sum, err := eng.Backup(BackupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("a2"))
	mustWrite(t, filepath.Join(home, ".codex", "AGENTS.md"), []byte("b2"))

	rsum, err := eng.Restore(RestoreOptions{Version: sum.Version})
	if err != nil {
		t.Fatal(err)
	}
	if rsum.PreBackupPath == "" {
		t.Fatal("PreBackupPath not set")
	}
	for _, rel := range []string{".codex/config.toml", ".codex/AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(rsum.PreBackupPath, rel)); err != nil {
			t.Errorf("entry %s missing from the single pre-backup dir: %v", rel, err)
		}
	}
	// Exactly one dated dir under backup/ai.
	aiBackup := filepath.Join(home, ".local", "share", "dotfiles", "backup", "ai")
	entries, err := os.ReadDir(aiBackup)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("pre-restore backups fragmented across %d dirs", len(entries))
	}
}

func TestResolveLatestFallsBackOnDanglingPointer(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("x"))

	s1, err := eng.Backup(BackupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Point latest.txt at a deleted version.
	mustWrite(t, eng.LatestPointerPath(), []byte("19990101T000000Z\n"))
	got, err := eng.ResolveLatest()
	if err != nil {
		t.Fatal(err)
	}
	if got != s1.Version {
		t.Errorf("dangling pointer: got %q want %q", got, s1.Version)
	}

	// Empty pointer must not resolve to the host root.
	mustWrite(t, eng.LatestPointerPath(), []byte("\n"))
	got, err = eng.ResolveLatest()
	if err != nil {
		t.Fatal(err)
	}
	if got != s1.Version {
		t.Errorf("empty pointer: got %q want %q", got, s1.Version)
	}
}

func TestAIListSkipsDirsWithoutMeta(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("x"))
	s1, err := eng.Backup(BackupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(eng.VersionPath("99999999T999999Z"), 0o755); err != nil {
		t.Fatal(err)
	}
	snaps, err := eng.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].Version != s1.Version {
		t.Errorf("List must skip meta-less dirs: %+v", snaps)
	}
}

func TestAIPruneKeepsNewest(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("x"))

	s1, err := eng.Backup(BackupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Synthesize two older snapshots with valid meta.
	for _, v := range []string{"20200101T000000Z", "20210101T000000Z"} {
		dir := eng.VersionPath(v)
		mustWrite(t, filepath.Join(dir, "meta.yaml"), []byte("version: "+v+"\nhostname: testhost\n"))
	}
	removed, err := eng.Prune(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %v", removed)
	}
	snaps, err := eng.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].Version != s1.Version {
		t.Errorf("prune kept wrong snapshot: %+v", snaps)
	}
}

func TestExportArchiveIsOwnerOnly(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("x"))
	mustWrite(t, filepath.Join(home, ".codex", "auth.json"), []byte("secret"))

	archive := filepath.Join(home, "out", "ai.tar.gz")
	// Pre-create world-readable to verify the heal path.
	mustWrite(t, archive, []byte("old"))
	if err := os.Chmod(archive, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Export(archive, BackupOptions{IncludeAuth: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("archive mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestAIBackupFailureLeavesNoOrphanDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based failure injection is ineffective as root")
	}
	eng, home, _ := testEngine(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	mustWrite(t, cfg, []byte("x"))
	if err := os.Chmod(cfg, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfg, 0o644) })

	if _, err := eng.Backup(BackupOptions{}); err == nil {
		t.Fatal("expected backup to fail on unreadable entry")
	}
	entries, err := os.ReadDir(eng.HostRoot())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, en := range entries {
		if en.IsDir() {
			t.Errorf("orphan version dir left behind: %s", en.Name())
		}
	}
}

func TestEntriesIncludeAnchorSettingsOnly(t *testing.T) {
	var anchorPaths []string
	for _, e := range Entries(false) {
		if e.Tool == "anchor" {
			anchorPaths = append(anchorPaths, e.Path)
			if isExcluded(e.Path) {
				t.Errorf("anchor entry %q is filtered by isExcluded", e.Path)
			}
		}
	}
	want := []string{".anchor/settings.json", ".anchor/sites.json"}
	if len(anchorPaths) != len(want) {
		t.Fatalf("anchor entries = %v, want %v", anchorPaths, want)
	}
	for i, p := range want {
		if anchorPaths[i] != p {
			t.Errorf("anchor entry %d = %q, want %q", i, anchorPaths[i], p)
		}
	}
}

func TestAnchorSettingsBackupRestoreRoundtrip(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".anchor", "settings.json"), []byte(`{"theme":"dark"}`))
	mustWrite(t, filepath.Join(home, ".anchor", "sites.json"), []byte(`[{"name":"halla-ai"}]`))

	sum, err := eng.Backup(BackupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".anchor/settings.json", ".anchor/sites.json"} {
		if _, err := os.Stat(filepath.Join(sum.Path, "home", rel)); err != nil {
			t.Errorf("snapshot missing %s: %v", rel, err)
		}
	}

	mustWrite(t, filepath.Join(home, ".anchor", "settings.json"), []byte(`{"theme":"mutated"}`))
	if _, err := eng.Restore(RestoreOptions{Version: sum.Version}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".anchor", "settings.json"))
	if string(got) != `{"theme":"dark"}` {
		t.Errorf("anchor settings not restored: %q", got)
	}
}

func TestAIListHosts(t *testing.T) {
	root := t.TempDir()
	if hosts, err := ListHosts(root); err != nil || hosts != nil {
		t.Fatalf("missing tree should be (nil, nil): %v %v", hosts, err)
	}
	for _, h := range []string{"b-host", "a-host"} {
		if err := os.MkdirAll(filepath.Join(root, "ai-config", h), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hosts, err := ListHosts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 || hosts[0] != "a-host" || hosts[1] != "b-host" {
		t.Errorf("hosts = %v", hosts)
	}
}
