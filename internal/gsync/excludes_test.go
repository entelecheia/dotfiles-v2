package gsync

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func loadTestExcludePatterns(t *testing.T) []string {
	t.Helper()
	paths := ResolveLocalPaths(t.TempDir())
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("EnsureLocalLayout: %v", err)
	}
	content, err := os.ReadFile(paths.ExcludeFile)
	if err != nil {
		t.Fatalf("read exclude file: %v", err)
	}
	patterns, err := parsePatternLines(content)
	if err != nil {
		t.Fatalf("parsePatternLines: %v", err)
	}
	return patterns
}

func TestExcludePatterns_ContainsCriticalRules(t *testing.T) {
	patterns := loadTestExcludePatterns(t)
	if len(patterns) == 0 {
		t.Fatal("exclude file returned no patterns")
	}

	// Critical rules — losing these would break correctness or sync the wrong things.
	// Note: shared-folder exclusions are NOT name-based; they're handled by the
	// dynamic excludes pipeline (manual list + Git-tracked paths). Don't add
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

func TestExcludePatterns_NoCommentsOrBlanks(t *testing.T) {
	patterns := loadTestExcludePatterns(t)
	for _, p := range patterns {
		if p == "" {
			t.Error("blank pattern leaked through filter")
		}
		if strings.HasPrefix(p, "#") {
			t.Errorf("comment leaked through filter: %q", p)
		}
	}
}

func TestLoadDefaultIncludePatterns_ContainsBinaryPayloadRules(t *testing.T) {
	patterns, err := LoadDefaultIncludePatterns()
	if err != nil {
		t.Fatalf("LoadDefaultIncludePatterns: %v", err)
	}
	for _, want := range []string{"*.tgz", "*.gz", "*.rar", "*.zst", "*.mp3", "*.mp4", "*.png", "*.jpg", "*.heic", "*.wmf", "*.ai", "*.key", "*.pdf", "*.hwp*", "*.docx", "*.pptx", "*.xls*", "*.xlsx"} {
		if !slices.Contains(patterns, want) {
			t.Errorf("includes.txt missing required pattern %q\nGot patterns: %v", want, patterns)
		}
	}
}

func TestEnsureLocalLayout_MaterializesExcludeFile(t *testing.T) {
	paths := ResolveLocalPaths(t.TempDir())
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("EnsureLocalLayout: %v", err)
	}
	body, err := os.ReadFile(paths.ExcludeFile)
	if err != nil {
		t.Fatalf("read materialized exclude file: %v", err)
	}
	if !strings.Contains(string(body), ".git") {
		t.Errorf("materialized content missing expected rules; got:\n%s", body)
	}
	stat1, err := os.Stat(paths.ExcludeFile)
	if err != nil {
		t.Fatalf("stat materialized file: %v", err)
	}
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("second EnsureLocalLayout: %v", err)
	}
	stat2, err := os.Stat(paths.ExcludeFile)
	if err != nil {
		t.Fatalf("stat after second layout: %v", err)
	}
	if !stat2.ModTime().Equal(stat1.ModTime()) {
		t.Errorf("exclude file mtime changed on idempotent layout: %v -> %v", stat1.ModTime(), stat2.ModTime())
	}
}

func TestEnsureLocalLayout_PreservesExistingExcludeFile(t *testing.T) {
	paths := ResolveLocalPaths(t.TempDir())
	if err := os.MkdirAll(paths.StoreDir, 0755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	const custom = "# operator override\n/custom-cache/\n"
	if err := os.WriteFile(paths.ExcludeFile, []byte(custom), 0644); err != nil {
		t.Fatalf("seed exclude file: %v", err)
	}
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("EnsureLocalLayout: %v", err)
	}
	body, err := os.ReadFile(paths.ExcludeFile)
	if err != nil {
		t.Fatalf("read exclude file: %v", err)
	}
	if string(body) != custom {
		t.Errorf("EnsureLocalLayout overwrote existing exclude file:\n%s", body)
	}
}

func TestCommonArgs_AlwaysOnRules(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeExclude
	cfg.ExcludesFile = "/tmp/excludes.conf"
	args := commonArgs(cfg, "")

	wantContains := []string{
		"-a",
		"--stats",
		"--no-links",
		"--exclude=/.dotfiles/",
		"--exclude=/inbox/gdrive/",
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
	for _, a := range args {
		if strings.HasPrefix(a, "--info=") {
			t.Errorf("commonArgs leaked Apple-rsync-incompatible flag %q", a)
		}
	}

	// With verbose=true: --progress should be present.
	cfg.Verbose = true
	args = commonArgs(cfg, "")
	if !slices.Contains(args, "--progress") {
		t.Error("commonArgs(verbose=true) did not add --progress")
	}
}

func TestCommonArgs_IncludeModeAddsCaseInsensitiveIncludes(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeInclude
	cfg.IncludePatterns = []string{"*.pdf", "*.hwp*"}

	args := commonArgs(cfg, "")

	for _, want := range []string{
		"--include=*/",
		"--include=*.[pP][dD][fF]",
		"--include=*.[hH][wW][pP]*",
		"--exclude=*",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("include-mode commonArgs missing %q\nargs: %v", want, args)
		}
	}
}

func TestCommonArgs_ExcludeModeDoesNotAddIncludeCatchall(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeExclude

	args := commonArgs(cfg, "")

	for _, forbidden := range []string{"--include=*/", "--exclude=*"} {
		if slices.Contains(args, forbidden) {
			t.Errorf("exclude-mode commonArgs leaked %q\nargs: %v", forbidden, args)
		}
	}
}

func TestCommonArgs_NoDeleteFlags(t *testing.T) {
	// commonArgs is shared between pull and push; the actual --delete-after / --update
	// must be added by the caller. Guard: commonArgs must NOT inject either of those.
	cfg := newTestConfig(t)
	args := commonArgs(cfg, "")
	for _, forbidden := range []string{"--delete", "--delete-after", "--delete-excluded", "--update"} {
		if slices.Contains(args, forbidden) {
			t.Errorf("commonArgs leaked direction-specific flag %q (must be added by Pull/Push only)", forbidden)
		}
	}
}
