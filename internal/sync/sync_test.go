package sync

import (
	"os"
	"path/filepath"
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

	// Write 10 lines
	var content string
	for i := 1; i <= 10; i++ {
		content += "line " + string(rune('0'+i)) + "\n"
	}
	os.WriteFile(logFile, []byte(content), 0644)

	// Tail last 3 lines
	result, err := TailLog(logFile, 3)
	if err != nil {
		t.Fatalf("TailLog: %v", err)
	}

	lines := 0
	for _, c := range result {
		if c == '\n' {
			lines++
		}
	}
	// TailLog joins with \n, so 3 lines produce 2 newlines in the join
	if lines > 3 {
		t.Errorf("got %d newlines, want <= 3", lines)
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
