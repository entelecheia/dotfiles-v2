package rclone

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

func TestResolveConfig_Defaults(t *testing.T) {
	state := &config.UserState{}
	cfg, err := ResolveConfig(state)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}

	if cfg.RemotePath != "gdrive:work" {
		t.Errorf("RemotePath = %q, want %q", cfg.RemotePath, "gdrive:work")
	}
	if cfg.Interval != 300 {
		t.Errorf("Interval = %d, want 300", cfg.Interval)
	}
	if cfg.FilterFile == "" {
		t.Error("FilterFile is empty")
	}
	if cfg.LogFile == "" {
		t.Error("LogFile is empty")
	}
}

func TestResolveConfig_CustomState(t *testing.T) {
	state := &config.UserState{}
	state.Modules.Sync.Remote = "mydrive"
	state.Modules.Sync.Path = "projects"
	state.Modules.Sync.Interval = 600

	cfg, err := ResolveConfig(state)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}

	if cfg.RemotePath != "mydrive:projects" {
		t.Errorf("RemotePath = %q, want %q", cfg.RemotePath, "mydrive:projects")
	}
	if cfg.Interval != 600 {
		t.Errorf("Interval = %d, want 600", cfg.Interval)
	}
}

func TestResolvePaths(t *testing.T) {
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}

	if paths.FilterFile == "" {
		t.Error("FilterFile is empty")
	}
	if paths.LogFile == "" {
		t.Error("LogFile is empty")
	}
	if paths.LaunchdPlist == "" {
		t.Error("LaunchdPlist is empty")
	}
	if paths.SystemdService == "" {
		t.Error("SystemdService is empty")
	}
	if paths.SystemdTimer == "" {
		t.Error("SystemdTimer is empty")
	}
}

func TestTailLog(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.log")

	// Write 10 lines with correct integer formatting
	var content string
	for i := 1; i <= 10; i++ {
		content += fmt.Sprintf("line %d\n", i)
	}
	if err := os.WriteFile(logFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := TailLog(logFile, 3)
	if err != nil {
		t.Fatalf("TailLog: %v", err)
	}

	got := strings.Split(result, "\n")
	if len(got) != 3 {
		t.Errorf("got %d lines, want 3", len(got))
	}
	if got[0] != "line 8" || got[1] != "line 9" || got[2] != "line 10" {
		t.Errorf("unexpected tail: %v", got)
	}
}

func TestTailLog_Missing(t *testing.T) {
	_, err := TailLog("/nonexistent/file.log", 10)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestExtractLastError(t *testing.T) {
	log := "2026/04/07 INFO: sync ok\n2026/04/07 ERROR: token expired\n2026/04/07 INFO: done"
	got := extractLastError(log)
	if got != "2026/04/07 ERROR: token expired" {
		t.Errorf("got %q, want ERROR line", got)
	}
}

func TestExtractLastError_NoError(t *testing.T) {
	log := "2026/04/07 INFO: sync ok\n2026/04/07 INFO: done"
	got := extractLastError(log)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestSchedulerState_String(t *testing.T) {
	tests := []struct {
		state SchedulerState
		want  string
	}{
		{SchedulerNotInstalled, "not installed"},
		{SchedulerRunning, "running"},
		{SchedulerStopped, "stopped"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("SchedulerState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestResolveConfig_IntervalClamp(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"zero defaults to 300", 0, 300},
		{"negative defaults to 300", -1, 300},
		{"too small clamps to 60", 10, 60},
		{"valid stays", 600, 600},
		{"too large clamps to 86400", 999999, 86400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &config.UserState{}
			state.Modules.Sync.Interval = tt.input
			cfg, err := ResolveConfig(state)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Interval != tt.expected {
				t.Errorf("interval=%d, want %d", cfg.Interval, tt.expected)
			}
		})
	}
}

func TestParseSyncErrors(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.log")

	log := strings.Join([]string{
		`2026/04/09 18:12:53 ERROR : projects/shared/file1.docx: Failed to copy: googleapi: Error 403: insufficientFilePermissions`,
		`2026/04/09 18:12:54 ERROR : projects/shared/file2.pptx: Failed to copy: googleapi: Error 403: insufficientFilePermissions`,
		`2026/04/09 18:12:55 NOTICE: projects/dir1/.astro: Can't follow symlink without -L/--copy-links`,
		`2026/04/09 18:12:56 NOTICE: Dangling shortcut "broken-link" detected`,
		`2026/04/09 18:12:57 ERROR : projects/shared/file1.docx: Failed to copy: googleapi: Error 403: insufficientFilePermissions`, // duplicate
		`2026/04/09 18:12:58 ERROR : meetings/doc.docx: Failed to copy: can't update google document type without --drive-import-formats`,
	}, "\n")
	if err := os.WriteFile(logFile, []byte(log), 0644); err != nil {
		t.Fatal(err)
	}

	paths := parseSyncErrors(logFile)

	wantSet := map[string]bool{
		"projects/shared/file1.docx": true,
		"projects/shared/file2.pptx": true,
		"projects/dir1/.astro":       true,
		"broken-link":                true,
		"meetings/doc.docx":          true,
	}
	if len(paths) != len(wantSet) {
		t.Errorf("got %d paths, want %d: %v", len(paths), len(wantSet), paths)
	}
	for _, p := range paths {
		if !wantSet[p] {
			t.Errorf("unexpected path: %q", p)
		}
	}
}

func TestUpdateSkipList(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.log")
	skipFile := filepath.Join(dir, "skip.txt")

	// Empty log → no additions
	os.WriteFile(logFile, []byte("2026/04/09 INFO: nothing\n"), 0644)
	added, err := UpdateSkipList(logFile, skipFile)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Errorf("empty log: added=%d, want 0", added)
	}

	// Log with 2 errors → 2 additions
	log := `2026/04/09 ERROR : path/a.docx: Failed to copy: googleapi: Error 403: insufficientFilePermissions
2026/04/09 ERROR : path/b.docx: Failed to copy: googleapi: Error 403: insufficientFilePermissions
`
	os.WriteFile(logFile, []byte(log), 0644)
	added, err = UpdateSkipList(logFile, skipFile)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Errorf("first run: added=%d, want 2", added)
	}

	// Same log again → 0 additions (dedup)
	added, err = UpdateSkipList(logFile, skipFile)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Errorf("second run: added=%d, want 0", added)
	}

	// Verify skip file contents
	data, _ := os.ReadFile(skipFile)
	content := string(data)
	if !strings.Contains(content, "- path/a.docx") || !strings.Contains(content, "- path/b.docx") {
		t.Errorf("skip file missing entries: %s", content)
	}
}

func TestLoadSkipList(t *testing.T) {
	dir := t.TempDir()
	skipFile := filepath.Join(dir, "skip.txt")

	// Missing file → nil, no error
	paths, err := LoadSkipList(skipFile)
	if err != nil {
		t.Errorf("missing file: err=%v, want nil", err)
	}
	if paths != nil {
		t.Errorf("missing file: paths=%v, want nil", paths)
	}

	// Normal file with mixed content
	content := `# header comment
- path/a.docx
not a skip entry
- path/b.docx

- path/c.docx
`
	os.WriteFile(skipFile, []byte(content), 0644)
	paths, err = LoadSkipList(skipFile)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"path/a.docx", "path/b.docx", "path/c.docx"}
	if len(paths) != len(want) {
		t.Errorf("got %d paths, want %d: %v", len(paths), len(want), paths)
	}
	for i, w := range want {
		if i >= len(paths) || paths[i] != w {
			t.Errorf("paths[%d] = %v, want %v", i, paths, want)
		}
	}
}

func TestExtractLastStats(t *testing.T) {
	log := `Some intro
Transferred:   	    4.735 KiB / 4.735 KiB, 100%, 0 B/s, ETA -
Checks:              7631 / 7631, 100%, Listed 20309
Transferred:            3 / 3, 100%
Errors:                 0
Elapsed time:        5m0s`
	stats := extractLastStats(log)
	if !strings.Contains(stats, "3 files") {
		t.Errorf("missing file count: %q", stats)
	}
	if !strings.Contains(stats, "4.735 KiB") {
		t.Errorf("missing byte count: %q", stats)
	}
}

func TestExtractLastStats_WithErrors(t *testing.T) {
	log := `Transferred:   	   95.984 MiB / 95.984 MiB, 100%, 3.912 MiB/s, ETA 0s
Errors:                 5 (fatal error encountered)
Checks:             56886 / 56886, 100%, Listed 361620
Transferred:            6 / 6, 100%
Elapsed time:     10m55.7s`
	stats := extractLastStats(log)
	if !strings.Contains(stats, "5 errors") {
		t.Errorf("missing error count: %q", stats)
	}
}

func TestExtractLastStats_NoMatch(t *testing.T) {
	log := "Just some info\nno transfer happened"
	stats := extractLastStats(log)
	if stats != "" {
		t.Errorf("expected empty, got %q", stats)
	}
}

func TestIsMounted_NotMounted(t *testing.T) {
	dir := t.TempDir()
	// A regular directory is NOT a mount point
	if IsMounted(dir) {
		t.Errorf("regular directory should not be detected as mounted")
	}
}

func TestIsMounted_Missing(t *testing.T) {
	if IsMounted("/nonexistent/path/xyz") {
		t.Errorf("missing path should not be detected as mounted")
	}
}
