package rsync

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// testConfig returns a Config whose lock/log paths live under a tempdir so
// tests never touch the real /tmp lock or ~/.local/log.
func testConfig(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	return &Config{
		LocalPath:      dir + "/work/",
		RemoteHost:     "user@host",
		RemotePath:     "~/workspace/work/",
		ExtensionsFile: filepath.Join(dir, "binary-extensions.conf"),
		LogFile:        filepath.Join(dir, "log", "sync.log"),
		LockDir:        filepath.Join(dir, "sync.lock"),
		Interval:       300,
	}
}

// ── ResolveConfig / ResolvePaths ─────────────────────────────────────────

func TestResolveConfig_Defaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	state := &config.UserState{}
	cfg, err := ResolveConfig(state)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}

	wantLocal := filepath.Join(home, "workspace", "work") + "/"
	if cfg.LocalPath != wantLocal {
		t.Errorf("LocalPath = %q, want %q", cfg.LocalPath, wantLocal)
	}
	if cfg.RemotePath != "~/workspace/work/" {
		t.Errorf("RemotePath = %q, want %q", cfg.RemotePath, "~/workspace/work/")
	}
	if cfg.Interval != 300 {
		t.Errorf("Interval = %d, want 300", cfg.Interval)
	}
}

func TestResolveConfig_CustomState(t *testing.T) {
	state := &config.UserState{}
	state.Modules.Workspace.Path = t.TempDir() + "/ws/work"
	state.Modules.Rsync.RemoteHost = "me@server"
	state.Modules.Rsync.RemotePath = "/srv/work"
	state.Modules.Rsync.Interval = 600

	cfg, err := ResolveConfig(state)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}

	if !strings.HasSuffix(cfg.LocalPath, "/ws/work/") {
		t.Errorf("LocalPath = %q, want trailing slash on custom path", cfg.LocalPath)
	}
	if cfg.RemoteHost != "me@server" {
		t.Errorf("RemoteHost = %q, want %q", cfg.RemoteHost, "me@server")
	}
	if cfg.RemotePath != "/srv/work/" {
		t.Errorf("RemotePath = %q, want trailing slash", cfg.RemotePath)
	}
	if cfg.Interval != 600 {
		t.Errorf("Interval = %d, want 600", cfg.Interval)
	}
}

func TestResolveConfig_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	state := &config.UserState{}
	state.Modules.Workspace.Path = "~/ws/work"

	cfg, err := ResolveConfig(state)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}

	want := filepath.Join(home, "ws", "work") + "/"
	if cfg.LocalPath != want {
		t.Errorf("LocalPath = %q, want %q", cfg.LocalPath, want)
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
		{"too small clamps to 60", 30, 60},
		{"valid stays", 600, 600},
		{"too large clamps to 86400", 100000, 86400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &config.UserState{}
			state.Modules.Rsync.Interval = tt.input
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

func TestResolvePaths(t *testing.T) {
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}

	if !strings.HasSuffix(paths.ExtensionsFile, "binary-extensions.conf") {
		t.Errorf("ExtensionsFile = %q", paths.ExtensionsFile)
	}
	if paths.LogFile == "" || paths.LockDir == "" {
		t.Errorf("LogFile/LockDir empty: %+v", paths)
	}
	if !strings.HasSuffix(paths.LaunchdPlist, ".plist") {
		t.Errorf("LaunchdPlist = %q", paths.LaunchdPlist)
	}
	if !strings.HasSuffix(paths.SystemdTimer, ".timer") {
		t.Errorf("SystemdTimer = %q", paths.SystemdTimer)
	}
}

// ── rsync args ───────────────────────────────────────────────────────────

func TestCommonArgs_RuleOrdering(t *testing.T) {
	cfg := testConfig(t)
	args := commonArgs(cfg)

	idx := func(want string) int {
		for i, a := range args {
			if a == want {
				return i
			}
		}
		t.Fatalf("arg %q not found in %v", want, args)
		return -1
	}

	// rsync evaluates rules first-match-wins: directory excludes must come
	// before --include=*/, and the catch-all --exclude=* must come last.
	gitExclude := idx("--exclude=.git")
	includeDirs := idx("--include=*/")
	excludeAll := idx("--exclude=*")

	if gitExclude > includeDirs {
		t.Errorf("--exclude=.git (%d) must come before --include=*/ (%d)", gitExclude, includeDirs)
	}
	if includeDirs > excludeAll {
		t.Errorf("--include=*/ (%d) must come before --exclude=* (%d)", includeDirs, excludeAll)
	}
	if excludeAll != len(args)-1 {
		t.Errorf("--exclude=* must be the final rule, got index %d of %d", excludeAll, len(args)-1)
	}

	wantInclude := "--include-from=" + cfg.ExtensionsFile
	found := false
	for _, a := range args {
		if a == wantInclude {
			found = true
		}
	}
	if !found {
		t.Errorf("missing %q in %v", wantInclude, args)
	}
}

func TestRemoteSpec(t *testing.T) {
	cfg := &Config{RemoteHost: "user@host", RemotePath: "/srv/work/"}
	if got := remoteSpec(cfg); got != "user@host:/srv/work/" {
		t.Errorf("remoteSpec = %q", got)
	}
}

// ── lock ─────────────────────────────────────────────────────────────────

func TestAcquireLock_Lifecycle(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "sync.lock")

	release, err := AcquireLock(lockDir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	if _, err := AcquireLock(lockDir); err == nil {
		t.Error("second acquire should fail while lock is held")
	} else if !strings.Contains(err.Error(), "another sync is running") {
		t.Errorf("unexpected error: %v", err)
	}

	release()
	if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
		t.Error("release should remove the lock directory")
	}

	release2, err := AcquireLock(lockDir)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	release2()
}

func TestAcquireLock_ReclaimsDeadPID(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "sync.lock")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatalf("mkdir lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "lock.pid"), []byte("99999999\n"), 0644); err != nil {
		t.Fatalf("write lock pid: %v", err)
	}
	release, err := AcquireLock(lockDir)
	if err != nil {
		t.Fatalf("acquire stale lock: %v", err)
	}
	release()
}

// ── pull / push / sync (fake rsync on PATH) ──────────────────────────────

// installFakeRsync puts an rsync stub on PATH that appends its argv to
// argsFile, one invocation per line.
func installFakeRsync(t *testing.T, argsFile string) {
	t.Helper()
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "rsync"),
		"#!/bin/sh\necho \"$@\" >> "+argsFile+"\n")
	t.Setenv("PATH", bin)
}

func readArgLines(t *testing.T, argsFile string) []string {
	t.Helper()
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading args file: %v", err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func TestPull_ArgConstruction(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dryRun=%v", dryRun), func(t *testing.T) {
			cfg := testConfig(t)
			argsFile := filepath.Join(t.TempDir(), "args.txt")
			installFakeRsync(t, argsFile)
			runner := exec.NewRunner(false, quietLogger())

			if err := Pull(context.Background(), runner, cfg, dryRun); err != nil {
				t.Fatalf("Pull: %v", err)
			}

			line := readArgLines(t, argsFile)[0]
			if !strings.Contains(line, "--update") {
				t.Errorf("pull args missing --update: %s", line)
			}
			if strings.Contains(line, "--delete-after") {
				t.Errorf("pull args must not contain --delete-after: %s", line)
			}
			if got, want := strings.Contains(line, "--dry-run"), dryRun; got != want {
				t.Errorf("--dry-run present=%v, want %v: %s", got, want, line)
			}
			// Remote → local transfer order.
			wantSuffix := remoteSpec(cfg) + " " + cfg.LocalPath
			if !strings.HasSuffix(line, wantSuffix) {
				t.Errorf("pull args = %q, want suffix %q", line, wantSuffix)
			}
		})
	}
}

func TestPush_ArgConstruction(t *testing.T) {
	cfg := testConfig(t)
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	installFakeRsync(t, argsFile)
	runner := exec.NewRunner(false, quietLogger())

	if err := Push(context.Background(), runner, cfg, false); err != nil {
		t.Fatalf("Push: %v", err)
	}

	line := readArgLines(t, argsFile)[0]
	if !strings.Contains(line, "--delete-after") {
		t.Errorf("push args missing --delete-after: %s", line)
	}
	if strings.Contains(line, "--update") {
		t.Errorf("push args must not contain --update: %s", line)
	}
	// Local → remote transfer order.
	wantSuffix := cfg.LocalPath + " " + remoteSpec(cfg)
	if !strings.HasSuffix(line, wantSuffix) {
		t.Errorf("push args = %q, want suffix %q", line, wantSuffix)
	}
}

func TestSync_PullFailureStillPushes(t *testing.T) {
	cfg := testConfig(t)
	marker := filepath.Join(t.TempDir(), "pushed")
	bin := t.TempDir()
	// Fail the pull pass (--update), succeed the push pass. PATH contains
	// only the stub dir, so use the ':' builtin instead of touch.
	writeExecutable(t, filepath.Join(bin, "rsync"),
		"#!/bin/sh\ncase \"$@\" in\n  *--update*) exit 1 ;;\n  *) : > "+marker+" ;;\nesac\n")
	t.Setenv("PATH", bin)
	runner := exec.NewRunner(false, quietLogger())

	err := Sync(context.Background(), runner, cfg, false)
	if err == nil {
		t.Fatal("Sync should report the pull error")
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("push should still run after a pull failure")
	}
}

// ── detection helpers ────────────────────────────────────────────────────

func TestCheckRsync(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		wantVer  string
		wantOK   bool
		noBinary bool
	}{
		{
			name:    "reports first version line",
			script:  "#!/bin/sh\necho 'rsync  version 3.3.0  protocol version 31'\necho 'more output'\n",
			wantVer: "rsync  version 3.3.0  protocol version 31",
			wantOK:  true,
		},
		{
			name:   "command failure",
			script: "#!/bin/sh\nexit 1\n",
		},
		{
			name:     "missing binary",
			noBinary: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := t.TempDir()
			if !tt.noBinary {
				writeExecutable(t, filepath.Join(bin, "rsync"), tt.script)
			}
			t.Setenv("PATH", bin)
			runner := exec.NewRunner(false, quietLogger())

			ver, ok := CheckRsync(runner)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ver != tt.wantVer {
				t.Errorf("version = %q, want %q", ver, tt.wantVer)
			}
		})
	}
}

func TestCheckSSH_Failure(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "ssh"), "#!/bin/sh\nexit 255\n")
	t.Setenv("PATH", bin)
	runner := exec.NewRunner(false, quietLogger())

	err := CheckSSH(context.Background(), runner, "user@host")
	if err == nil {
		t.Fatal("expected error from failing ssh")
	}
	if !strings.Contains(err.Error(), "SSH to user@host failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ── log helpers ──────────────────────────────────────────────────────────

func TestAppendLog(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "sync.log")

	AppendLog(logFile, 0, 1)

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if !strings.Contains(string(data), "pull=0 push=1") {
		t.Errorf("log line = %q, want pull=0 push=1", string(data))
	}
}

func TestRotateLog(t *testing.T) {
	t.Run("over max keeps last keepLines", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "sync.log")
		var content strings.Builder
		for i := 1; i <= 30; i++ {
			fmt.Fprintf(&content, "line %d\n", i)
		}
		if err := os.WriteFile(logFile, []byte(content.String()), 0644); err != nil {
			t.Fatal(err)
		}

		RotateLog(logFile, 20, 10)

		data, _ := os.ReadFile(logFile)
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) != 10 {
			t.Fatalf("got %d lines, want 10", len(lines))
		}
		if lines[0] != "line 21" || lines[9] != "line 30" {
			t.Errorf("unexpected window: first=%q last=%q", lines[0], lines[9])
		}
	})

	t.Run("under max is a no-op", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "sync.log")
		original := "line 1\nline 2\n"
		if err := os.WriteFile(logFile, []byte(original), 0644); err != nil {
			t.Fatal(err)
		}

		RotateLog(logFile, 20, 10)

		data, _ := os.ReadFile(logFile)
		if string(data) != original {
			t.Errorf("file changed: %q", string(data))
		}
	})

	t.Run("keepLines larger than file keeps everything", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "sync.log")
		if err := os.WriteFile(logFile, []byte("a\nb\nc\nd\ne\n"), 0644); err != nil {
			t.Fatal(err)
		}

		RotateLog(logFile, 3, 10)

		data, _ := os.ReadFile(logFile)
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) != 5 {
			t.Errorf("got %d lines, want 5", len(lines))
		}
	})
}

func TestTailLog(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.log")

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
	if len(got) != 3 || got[0] != "line 8" || got[2] != "line 10" {
		t.Errorf("unexpected tail: %v", got)
	}

	all, err := TailLog(logFile, 100)
	if err != nil {
		t.Fatalf("TailLog: %v", err)
	}
	if len(strings.Split(all, "\n")) != 10 {
		t.Errorf("want all 10 lines when n exceeds file length")
	}
}

func TestTailLog_Missing(t *testing.T) {
	if _, err := TailLog(filepath.Join(t.TempDir(), "missing.log"), 10); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestExtractLastResult(t *testing.T) {
	tests := []struct {
		name string
		log  string
		want string
	}{
		{
			name: "picks the most recent result",
			log:  "2026-01-01 10:00:00 pull=0 push=0\n2026-01-01 10:05:00 pull=1 push=23",
			want: "pull=1 push=23",
		},
		{
			name: "ignores trailing junk lines",
			log:  "2026-01-01 10:00:00 pull=0 push=0\nsome warning\n",
			want: "pull=0 push=0",
		},
		{
			name: "empty when no result lines",
			log:  "no results here\nstill nothing",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractLastResult(tt.log); got != tt.want {
				t.Errorf("extractLastResult = %q, want %q", got, tt.want)
			}
		})
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
