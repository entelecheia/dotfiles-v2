package syncer

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGitSubmodulePaths_ParsesGitmodules(t *testing.T) {
	root := t.TempDir()
	gitmodules := `[submodule "dev"]
	path = dev
	url = https://example.com/dev.git
[submodule "sites/a"]
	path = sites/a
	url = https://example.com/a.git
[submodule "vault"]
	path = vault
	url = https://example.com/vault.git
`
	if err := os.WriteFile(filepath.Join(root, ".gitmodules"), []byte(gitmodules), 0o644); err != nil {
		t.Fatal(err)
	}
	got := gitSubmodulePaths(root)
	want := []string{"dev", "sites/a", "vault"}
	if !slices.Equal(got, want) {
		t.Errorf("gitSubmodulePaths = %v, want %v", got, want)
	}
}

func TestGitSubmodulePaths_MissingFileReturnsNil(t *testing.T) {
	if got := gitSubmodulePaths(t.TempDir()); got != nil {
		t.Errorf("expected nil for missing .gitmodules, got %v", got)
	}
}

func TestGitTrackedForSync_SkipsGitlinks(t *testing.T) {
	root := t.TempDir()
	mustGit := func(args ...string) {
		cmd := osexec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v unavailable: %v\n%s", args, err, out)
		}
	}
	mustGit("init")
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit("add", "notes.md")
	// Fake a gitlink entry (mode 160000) without needing a real submodule.
	mustGit("update-index", "--add", "--cacheinfo", "160000",
		"0000000000000000000000000000000000000001", "fakesub")

	got := gitTrackedForSync(root)
	if !got["notes.md"] {
		t.Errorf("tracked file missing: %v", got)
	}
	if got["fakesub"] {
		t.Errorf("gitlink leaked into tracked set: %v", got)
	}
}

func TestUnionTrackedWithBaseline_DeletePropagation(t *testing.T) {
	// A tracked file deleted locally leaves git ls-files but must stay in
	// the include layer via its baseline key so rsync --delete can remove
	// the mirror copy (excluded dest files are protected from --delete).
	tracked := map[string]bool{"kept.md": true}
	baseline := map[string]Fingerprint{
		"kept.md":    {},
		"deleted.md": {},
	}
	got := unionTrackedWithBaseline(tracked, baseline)
	want := []string{"deleted.md", "kept.md"}
	if !slices.Equal(got, want) {
		t.Errorf("union = %v, want %v", got, want)
	}
}

func TestMaterializeSubmodulesAndTrackedFiles(t *testing.T) {
	paths := ResolveLocalPaths(t.TempDir())
	subPath, err := MaterializeSubmodulesDynFile(paths.StoreDir, []string{"dev", "sites/a"})
	if err != nil {
		t.Fatal(err)
	}
	subBody, _ := os.ReadFile(subPath)
	for _, want := range []string{"/dev\n", "/dev/\n", "/sites/a\n", "/sites/a/\n"} {
		if !strings.Contains(string(subBody), want) {
			t.Errorf("submodules dyn missing %q:\n%s", want, subBody)
		}
	}

	trackedPath, err := MaterializeTrackedIncludesFile(paths.StoreDir, []string{"a/b.md", "c.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	trackedBody, _ := os.ReadFile(trackedPath)
	for _, want := range []string{"/a/b.md\n", "/c.pdf\n"} {
		if !strings.Contains(string(trackedBody), want) {
			t.Errorf("tracked dyn missing %q:\n%s", want, trackedBody)
		}
	}
}

func TestMigrateLegacyStore_RenamesAndRewritesGitignore(t *testing.T) {
	root := t.TempDir()
	oldStore := filepath.Join(root, ".dotfiles", "gdrive-sync")
	if err := os.MkdirAll(filepath.Join(oldStore, "log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldStore, "config.yaml"), []byte("paused: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldStore, "log", "gdrive-sync.log"), []byte("line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitignore := "!/.dotfiles/gdrive-sync/\n/.dotfiles/gdrive-sync/*\n!/.dotfiles/gdrive-sync/exclude.txt\n"
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateLegacyStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Fatal("expected migration to run")
	}
	if _, err := os.Stat(filepath.Join(root, ".dotfiles", "sync", "config.yaml")); err != nil {
		t.Errorf("config not at new store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".dotfiles", "sync", "log", "sync.log")); err != nil {
		t.Errorf("log not renamed: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if strings.Contains(string(body), "gdrive-sync") {
		t.Errorf(".gitignore still references gdrive-sync:\n%s", body)
	}
	if !strings.Contains(string(body), "!/.dotfiles/sync/exclude.txt") {
		t.Errorf("operator whitelist line lost:\n%s", body)
	}

	// Idempotent: second call is a no-op.
	if again, err := MigrateLegacyStore(root); err != nil || again {
		t.Errorf("second migration = (%v, %v), want (false, nil)", again, err)
	}
}
