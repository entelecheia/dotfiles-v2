package aischedule

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	home := t.TempDir()
	return &Manager{Home: home, CurrentHome: home, GOOS: "darwin", UID: 501, Executable: "/opt/bin/dot", Environment: map[string]string{"HOME": home, "PATH": "/bin"}, Run: func(context.Context, string, ...string) error { return nil }}
}
func TestRenderStableSundayAndEscaping(t *testing.T) {
	body := Render("/tmp/a&b", State{Executable: "/opt/a&b/dot", Environment: map[string]string{"PATH": "/a&b"}})
	var parsed any
	if err := xml.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--scheduled", "<integer>0</integer>", "<integer>4</integer>", "/opt/a&amp;b/dot"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing%q", want)
		}
	}
	if strings.Contains(body, "KeepAlive") {
		t.Fatal("unbounded retries")
	}
}
func TestDryRunNeverWritesOrLaunches(t *testing.T) {
	m := testManager(t)
	m.DryRun = true
	m.Run = func(context.Context, string, ...string) error { t.Fatal("dry-run invoked launchctl"); return nil }
	if _, err := m.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(m.Home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("dry-run wrote files: %v %v", entries, err)
	}
}
func TestDifferentHomeCannotMutateUserDomain(t *testing.T) {
	m := testManager(t)
	m.CurrentHome = t.TempDir()
	if _, err := m.Enable(context.Background()); err == nil {
		t.Fatal("cross-home enabled")
	}
	m.GOOS = "linux"
	if _, err := m.Disable(context.Background()); err == nil {
		t.Fatal("Linux scheduled")
	}
}
func TestEnableIdempotentAndDisable(t *testing.T) {
	m := testManager(t)
	loaded := false
	bootstraps := 0
	m.Run = func(_ context.Context, _ string, args ...string) error {
		switch args[0] {
		case "print":
			if !loaded {
				return fmt.Errorf("not loaded")
			}
		case "bootstrap":
			loaded = true
			bootstraps++
		case "bootout":
			loaded = false
		}
		return nil
	}
	for i := 0; i < 2; i++ {
		st, err := m.Enable(context.Background())
		if err != nil || !st.Enabled || !st.Loaded {
			t.Fatalf("%+v %v", st, err)
		}
	}
	if bootstraps != 1 {
		t.Fatalf("bootstraps=%d", bootstraps)
	}
	for i := 0; i < 2; i++ {
		if _, err := m.Disable(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(m.PlistPath()); !os.IsNotExist(err) {
		t.Fatal("plist remains")
	}
}
func TestScheduledAttemptReceipts(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 9, 27, 4, 0, 0, 0, time.Local)
	if err := save(home, State{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	due, err := BeginScheduled(home, now)
	if err != nil || !due {
		t.Fatalf("%t %v", due, err)
	}
	due, err = BeginScheduled(home, now.Add(time.Minute))
	if err != nil || due {
		t.Fatal("login storm admitted")
	}
	if err := FinishScheduled(home, now, false); err != nil {
		t.Fatal(err)
	}
	s, _ := load(home)
	if !s.LastSuccess.IsZero() {
		t.Fatal("deferral marked current")
	}
	if err := FinishScheduled(home, now, true); err != nil {
		t.Fatal(err)
	}
	due, err = BeginScheduled(home, now.Add(24*time.Hour))
	if err != nil || due {
		t.Fatal("recent success reran")
	}
}
func TestPersistentPATHDropsTemporaryMultishell(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", filepath.Join(home, "fnm_multishells", "temp")+":/usr/bin:/bin")
	env := adoptEnvironment(home)
	if strings.Contains(env["PATH"], "fnm_multishells") {
		t.Fatal(env["PATH"])
	}
	if env["HOME"] != home {
		t.Fatal("home missing")
	}
}

func TestAdoptEnvironmentPreservesSelectedProfilePathsOnly(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "KIMI_CODE_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	t.Setenv("ANTHROPIC_API_KEY", "never-copy-this-secret")
	env := adoptEnvironment(home)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "KIMI_CODE_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG"} {
		if env[key] != filepath.Join(home, key) {
			t.Fatalf("profile %s missing", key)
		}
	}
	if _, exists := env["ANTHROPIC_API_KEY"]; exists {
		t.Fatal("credential persisted")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "relative-profile")
	if _, exists := adoptEnvironment(home)["CLAUDE_CONFIG_DIR"]; exists {
		t.Fatal("relative profile adopted")
	}
}

func TestWeeklyRetryBudgetSurvivesReloadAndResetsAtCalendarBoundary(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 9, 27, 4, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	if err := save(home, State{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < WeeklyAttemptLimit; attempt++ {
		at := now.Add(time.Duration(attempt) * time.Hour)
		due, err := BeginScheduled(home, at)
		if err != nil || !due {
			t.Fatalf("attempt %d due=%t err=%v", attempt, due, err)
		}
		if err := FinishScheduled(home, at, false); err != nil {
			t.Fatal(err)
		}
		// Reload persisted state, as a new hourly invocation after process restart.
		s, err := load(home)
		if err != nil || s.Attempts != attempt+1 {
			t.Fatalf("receipt=%+v err=%v", s, err)
		}
	}
	due, err := BeginScheduled(home, now.Add(20*time.Hour))
	if err != nil || due {
		t.Fatalf("budget retry allowed due=%t err=%v", due, err)
	}
	s, _ := load(home)
	st := retryStatus(Status{State: s}, now.Add(20*time.Hour))
	next := now.AddDate(0, 0, 7)
	if !st.BudgetExhausted || st.AttemptsRemaining != 0 || st.NextEligible == nil || !st.NextEligible.Equal(next) {
		t.Fatalf("status=%+v", st)
	}
	due, err = BeginScheduled(home, next)
	if err != nil || !due {
		t.Fatalf("new week due=%t err=%v", due, err)
	}
	s, _ = load(home)
	if s.Attempts != 1 || !s.AttemptWindow.Equal(next) {
		t.Fatalf("new window=%+v", s)
	}
}
func TestConcurrentScheduledChecksConsumeOneAttempt(t *testing.T) {
	home := t.TempDir()
	if err := save(home, State{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	type result struct {
		due bool
		err error
	}
	results := make(chan result, 8)
	for i := 0; i < 8; i++ {
		go func() { due, err := BeginScheduled(home, now); results <- result{due, err} }()
	}
	admitted := 0
	for i := 0; i < 8; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.due {
			admitted++
		}
	}
	s, _ := load(home)
	if admitted != 1 || s.Attempts != 1 {
		t.Fatalf("admitted=%d receipt=%+v", admitted, s)
	}
}
func TestFreshAndDisabledBudgetStatusDoesNotInventNextTime(t *testing.T) {
	now := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	for _, enabled := range []bool{true, false} {
		st := retryStatus(Status{State: State{Enabled: enabled}}, now)
		if st.AttemptsRemaining != 3 || st.BudgetExhausted || st.NextEligible != nil {
			t.Fatalf("%+v", st)
		}
	}
	old := State{Enabled: true, LastAttempt: now}
	st := retryStatus(Status{State: old}, now)
	if st.AttemptsRemaining != 2 || st.NextEligible == nil || !st.NextEligible.Equal(now.Add(time.Hour)) {
		t.Fatalf("old receipt migration=%+v", st)
	}
}
