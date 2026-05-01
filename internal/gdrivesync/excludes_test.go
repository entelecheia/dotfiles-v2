package gdrivesync

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadExcludePatterns_ContainsCriticalRules(t *testing.T) {
	patterns, err := LoadExcludePatterns()
	if err != nil {
		t.Fatalf("LoadExcludePatterns: %v", err)
	}
	if len(patterns) == 0 {
		t.Fatal("LoadExcludePatterns returned no patterns")
	}

	// Critical rules — losing these would break correctness or sync the wrong things.
	// Note: shared-folder exclusions are NOT name-based; they're handled by the
	// dynamic excludes pipeline (DetectSharedEntry + manual list). Don't add
	// `shared/`, `_shared/`, etc. back here.
	required := []string{
		".git",             // submodule gitlink files
		".git/",            // .git directories
		".gitmodules",      // submodule manifest
		".sync-conflicts/", // self-protection
		".DS_Store",        // mac noise
		"*.gdoc",           // Drive native pointer
		"*.gsheet",
		".tmp.driveupload",
		".tmp.drivedownload",
		"node_modules/",
		"_sys/env/.venv/",
	}
	for _, want := range required {
		// .tmp.* patterns are listed as `*.tmp.driveupload` so check via suffix.
		if strings.HasPrefix(want, ".tmp.") {
			want = "*" + want
		}
		if !slices.Contains(patterns, want) {
			t.Errorf("excludes.txt missing required pattern %q\nGot patterns: %v", want, patterns)
		}
	}
}

func TestLoadExcludePatterns_NoCommentsOrBlanks(t *testing.T) {
	patterns, err := LoadExcludePatterns()
	if err != nil {
		t.Fatalf("LoadExcludePatterns: %v", err)
	}
	for _, p := range patterns {
		if p == "" {
			t.Error("blank pattern leaked through filter")
		}
		if strings.HasPrefix(p, "#") {
			t.Errorf("comment leaked through filter: %q", p)
		}
	}
}

func TestMaterializeExcludesFile_Idempotent(t *testing.T) {
	dir := t.TempDir()

	path1, err := MaterializeExcludesFile(dir)
	if err != nil {
		t.Fatalf("first MaterializeExcludesFile: %v", err)
	}
	if path1 != filepath.Join(dir, excludesDiskName) {
		t.Errorf("unexpected materialized path: %s", path1)
	}
	stat1, err := os.Stat(path1)
	if err != nil {
		t.Fatalf("stat materialized file: %v", err)
	}
	mtime1 := stat1.ModTime()

	// Second call should not rewrite (content matches).
	path2, err := MaterializeExcludesFile(dir)
	if err != nil {
		t.Fatalf("second MaterializeExcludesFile: %v", err)
	}
	if path2 != path1 {
		t.Errorf("idempotent call returned different path: %s vs %s", path2, path1)
	}
	stat2, err := os.Stat(path2)
	if err != nil {
		t.Fatalf("stat after second call: %v", err)
	}
	if !stat2.ModTime().Equal(mtime1) {
		t.Errorf("file mtime changed on idempotent call: %v -> %v", mtime1, stat2.ModTime())
	}
}

func TestMaterializeExcludesFile_RewritesIfStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, excludesDiskName)
	if err := os.WriteFile(path, []byte("# stale stub\n"), 0644); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}

	out, err := MaterializeExcludesFile(dir)
	if err != nil {
		t.Fatalf("MaterializeExcludesFile: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read materialized: %v", err)
	}
	if strings.Contains(string(body), "stale stub") {
		t.Error("MaterializeExcludesFile failed to overwrite stale content")
	}
	if !strings.Contains(string(body), ".git") {
		t.Errorf("materialized content missing expected rules; got:\n%s", body)
	}
}

func TestCommonArgs_AlwaysOnRules(t *testing.T) {
	args := commonArgs([]string{"/tmp/excludes.conf"}, false)

	wantContains := []string{
		"-a",
		"--no-links",
		"--filter=:- .gitignore",
		"--exclude-from=/tmp/excludes.conf",
	}
	for _, w := range wantContains {
		if !slices.Contains(args, w) {
			t.Errorf("commonArgs missing %q\nargs: %v", w, args)
		}
	}

	// Without verbose: --progress should NOT be present.
	if slices.Contains(args, "--progress") {
		t.Error("commonArgs added --progress without verbose=true")
	}

	// With verbose=true: --progress should be present.
	args = commonArgs([]string{"/tmp/x.conf"}, true)
	if !slices.Contains(args, "--progress") {
		t.Error("commonArgs(verbose=true) did not add --progress")
	}
}

func TestCommonArgs_NoDeleteFlags(t *testing.T) {
	// commonArgs is shared between pull and push; the actual --delete-after / --update
	// must be added by the caller. Guard: commonArgs must NOT inject either of those.
	args := commonArgs([]string{"/tmp/x.conf"}, false)
	for _, forbidden := range []string{"--delete", "--delete-after", "--delete-excluded", "--update"} {
		if slices.Contains(args, forbidden) {
			t.Errorf("commonArgs leaked direction-specific flag %q (must be added by Pull/Push only)", forbidden)
		}
	}
}
