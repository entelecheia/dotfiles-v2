// Package aischedule owns the opt-in macOS weekly maintenance LaunchAgent.
package aischedule

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const Label = "com.dotfiles.ai.update"

// WeeklyAttemptLimit includes the initial attempt and survives process restarts.
const WeeklyAttemptLimit = 3

type State struct {
	Enabled       bool              `json:"enabled"`
	Executable    string            `json:"executable,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	LastAttempt   time.Time         `json:"last_attempt,omitempty"`
	LastSuccess   time.Time         `json:"last_success,omitempty"`
	Outcome       string            `json:"outcome,omitempty"`
	AttemptWindow time.Time         `json:"attempt_window,omitempty"`
	Attempts      int               `json:"attempts,omitempty"`
}
type Status struct {
	State
	Supported         bool       `json:"supported"`
	Installed         bool       `json:"installed"`
	Loaded            bool       `json:"loaded"`
	Plist             string     `json:"plist"`
	Schedule          string     `json:"schedule"`
	DryRun            bool       `json:"dry_run,omitempty"`
	AttemptsRemaining int        `json:"attempts_remaining"`
	BudgetExhausted   bool       `json:"budget_exhausted"`
	NextEligible      *time.Time `json:"next_eligible,omitempty"`
}
type Manager struct {
	Home        string
	Executable  string
	Environment map[string]string
	DryRun      bool
	GOOS        string
	CurrentHome string
	UID         int
	Run         func(context.Context, string, ...string) error
}

func New(home string, dryRun bool) *Manager {
	exe, _ := os.Executable()
	exe = stableExecutable(exe, os.Getenv("PATH"))
	u, _ := user.Current()
	current := ""
	if u != nil {
		current = u.HomeDir
	}
	return &Manager{Home: home, Executable: exe, Environment: adoptEnvironment(home), DryRun: dryRun, GOOS: runtime.GOOS, CurrentHome: current, UID: os.Getuid(), Run: func(ctx context.Context, name string, args ...string) error {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, name, args...).Run()
	}}
}

// Keep an installed entry point whose symlink follows package upgrades.
func stableExecutable(executable, path string) string {
	real, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return executable
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, filepath.Base(executable))
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil && resolved == real && !strings.Contains(candidate, "/Cellar/") && !strings.Contains(candidate, "/Caskroom/") && !strings.Contains(candidate, "fnm_multishells") {
			return candidate
		}
	}
	return executable
}

func stateDir(home string) string  { return filepath.Join(home, ".local", "share", "dotfiles", "ai") }
func StatePath(home string) string { return filepath.Join(stateDir(home), "update-schedule.json") }
func (m *Manager) PlistPath() string {
	return filepath.Join(m.Home, "Library", "LaunchAgents", Label+".plist")
}
func load(home string) (State, error) {
	var s State
	b, err := os.ReadFile(StatePath(home))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}
func save(home string, s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(StatePath(home), b)
}
func atomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".schedule-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (m *Manager) domain() string { return "gui/" + strconv.Itoa(m.UID) }
func (m *Manager) checkDomain() error {
	if m.GOOS != "darwin" {
		return fmt.Errorf("AI update scheduling is macOS-only; manual updates remain available")
	}
	a, err := filepath.EvalSymlinks(m.Home)
	if err != nil {
		return err
	}
	b, err := filepath.EvalSymlinks(m.CurrentHome)
	if err != nil {
		return err
	}
	if a != b {
		return fmt.Errorf("refusing launchd user-domain changes for another home")
	}
	return nil
}
func (m *Manager) Status(ctx context.Context) (Status, error) {
	s, err := load(m.Home)
	if err != nil {
		return Status{}, err
	}
	st := Status{State: s, Supported: m.GOOS == "darwin", Plist: m.PlistPath(), Schedule: "Sunday 04:00 local time; bounded hourly and login overdue checks", DryRun: m.DryRun}
	_, err = os.Stat(st.Plist)
	st.Installed = err == nil
	if err != nil && !os.IsNotExist(err) {
		return st, err
	}
	if !m.DryRun && m.checkDomain() == nil {
		st.Loaded = m.Run(ctx, "/bin/launchctl", "print", m.domain()+"/"+Label) == nil
	}
	return retryStatus(st, time.Now()), nil
}
func (m *Manager) Enable(ctx context.Context) (Status, error) {
	if err := m.checkDomain(); err != nil {
		return Status{}, err
	}
	if m.DryRun {
		return m.enable(ctx)
	}
	var status Status
	err := withStateLock(m.Home, func() error {
		var err error
		status, err = m.enable(ctx)
		return err
	})
	return status, err
}
func (m *Manager) enable(ctx context.Context) (Status, error) {
	if err := m.checkDomain(); err != nil {
		return Status{}, err
	}
	if !filepath.IsAbs(m.Executable) {
		return Status{}, fmt.Errorf("absolute installed dot executable required")
	}
	if strings.Contains(m.Executable, "fnm_multishells") || strings.Contains(m.Executable, "/Cellar/") || strings.Contains(m.Executable, "/Caskroom/") {
		return Status{}, fmt.Errorf("temporary or versioned executable path is not schedulable; use a stable installed entry point")
	}
	s, err := load(m.Home)
	if err != nil {
		return Status{}, err
	}
	s.Enabled = true
	s.Executable = m.Executable
	s.Environment = m.Environment
	if m.DryRun {
		return retryStatus(Status{State: s, Supported: true, Plist: m.PlistPath(), Schedule: "Sunday 04:00 local time", DryRun: true}, time.Now()), nil
	}
	body := Render(m.Home, s)
	old, readErr := os.ReadFile(m.PlistPath())
	if readErr != nil && !os.IsNotExist(readErr) {
		return Status{}, readErr
	}
	loaded := m.Run(ctx, "/bin/launchctl", "print", m.domain()+"/"+Label) == nil
	if !bytes.Equal(old, []byte(body)) && loaded {
		if err = m.Run(ctx, "/bin/launchctl", "bootout", m.domain()+"/"+Label); err != nil {
			return Status{}, fmt.Errorf("unloading previous schedule: %w", err)
		}
		loaded = false
	}
	if err = atomicWrite(m.PlistPath(), []byte(body)); err != nil {
		return Status{}, err
	}
	if err = save(m.Home, s); err != nil {
		return Status{}, err
	}
	if !loaded {
		if err = m.Run(ctx, "/bin/launchctl", "bootstrap", m.domain(), m.PlistPath()); err != nil {
			s.Enabled = false
			s.Outcome = "bootstrap-failed"
			_ = save(m.Home, s)
			return Status{}, fmt.Errorf("registering schedule: %w", err)
		}
	}
	return m.Status(ctx)
}
func (m *Manager) Disable(ctx context.Context) (Status, error) {
	if err := m.checkDomain(); err != nil {
		return Status{}, err
	}
	if m.DryRun {
		return m.disable(ctx)
	}
	var status Status
	err := withStateLock(m.Home, func() error {
		var err error
		status, err = m.disable(ctx)
		return err
	})
	return status, err
}
func (m *Manager) disable(ctx context.Context) (Status, error) {
	if err := m.checkDomain(); err != nil {
		return Status{}, err
	}
	s, err := load(m.Home)
	if err != nil {
		return Status{}, err
	}
	s.Enabled = false
	if m.DryRun {
		return retryStatus(Status{State: s, Supported: true, Plist: m.PlistPath(), DryRun: true}, time.Now()), nil
	}
	if m.Run(ctx, "/bin/launchctl", "print", m.domain()+"/"+Label) == nil {
		if err = m.Run(ctx, "/bin/launchctl", "bootout", m.domain()+"/"+Label); err != nil {
			return Status{}, err
		}
	}
	if err = os.Remove(m.PlistPath()); err != nil && !os.IsNotExist(err) {
		return Status{}, err
	}
	if err = save(m.Home, s); err != nil {
		return Status{}, err
	}
	return m.Status(ctx)
}
func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func Render(home string, s State) string {
	var env strings.Builder
	keys := make([]string, 0, len(s.Environment))
	for k := range s.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = fmt.Fprintf(&env, "<key>%s</key><string>%s</string>\n", escape(k), escape(s.Environment[k]))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>ai</string><string>update</string><string>--scheduled</string><string>--json</string></array>
<key>EnvironmentVariables</key><dict>%s</dict>
<key>StartCalendarInterval</key><dict><key>Weekday</key><integer>0</integer><key>Hour</key><integer>4</integer><key>Minute</key><integer>0</integer></dict>
<key>RunAtLoad</key><true/>
<key>StartInterval</key><integer>3600</integer>
<key>ProcessType</key><string>Background</string>
<key>StandardOutPath</key><string>%s/update-schedule.out.log</string>
<key>StandardErrorPath</key><string>%s/update-schedule.err.log</string>
</dict></plist>
`, Label, escape(s.Executable), env.String(), escape(stateDir(home)), escape(stateDir(home)))
}
func adoptEnvironment(home string) map[string]string {
	env := map[string]string{"HOME": home}
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "KIMI_CODE_HOME", "XDG_CONFIG_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG"} {
		if value := os.Getenv(key); value != "" && filepath.IsAbs(value) {
			env[key] = value
		}
	}
	dirs := []string{}
	seen := map[string]bool{}
	add := func(dir string) {
		if !filepath.IsAbs(dir) || strings.Contains(dir, "fnm_multishells") {
			return
		}
		if !seen[dir] {
			dirs = append(dirs, dir)
			seen[dir] = true
		}
	}
	if node, err := exec.LookPath("node"); err == nil {
		if resolved, err := filepath.EvalSymlinks(node); err == nil {
			add(filepath.Dir(resolved))
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			add(real)
		}
	}
	for _, dir := range []string{filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		add(dir)
	}
	env["PATH"] = strings.Join(dirs, string(os.PathListSeparator))
	return env
}
func withStateLock(home string, fn func() error) error {
	if err := os.MkdirAll(stateDir(home), 0700); err != nil {
		return err
	}
	var dirStat unix.Stat_t
	if err := unix.Lstat(stateDir(home), &dirStat); err != nil {
		return err
	}
	if dirStat.Mode&unix.S_IFMT != unix.S_IFDIR || dirStat.Uid != uint32(os.Getuid()) || dirStat.Mode&0022 != 0 {
		return fmt.Errorf("unsafe scheduler state directory")
	}
	fd, err := unix.Open(filepath.Join(stateDir(home), "update-schedule.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "schedule-lock")
	defer func() { _ = f.Close() }()
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}
	defer func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }()
	return fn()
}

// BeginScheduled suppresses repeated login/launch events and allows only a
// bounded overdue attempt. Failed/deferred runs are not recorded as successes.
func BeginScheduled(home string, now time.Time) (bool, error) {
	due := false
	err := withStateLock(home, func() error {
		s, err := load(home)
		if err != nil {
			return err
		}
		if !s.Enabled {
			return nil
		}
		window := latestSunday(now)
		if !s.LastSuccess.IsZero() && !s.LastSuccess.Before(window) {
			return nil
		}
		attempts := windowAttempts(s, window)
		if attempts >= WeeklyAttemptLimit {
			if s.Outcome != "retry-budget-exhausted" {
				s.Outcome = "retry-budget-exhausted"
				return save(home, s)
			}
			return nil
		}
		if !s.LastAttempt.IsZero() && !s.LastAttempt.Before(window) && now.Sub(s.LastAttempt) < time.Hour {
			return nil
		}
		s.AttemptWindow = window
		s.Attempts = attempts + 1
		s.LastAttempt = now
		s.Outcome = "running"
		due = true
		return save(home, s)
	})
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return due, err
}
func FinishScheduled(home string, now time.Time, success bool) error {
	return withStateLock(home, func() error {
		s, err := load(home)
		if err != nil {
			return err
		}
		if success {
			s.LastSuccess = now
			s.Outcome = "completed"
		} else {
			s.Outcome = "failed-or-deferred"
			if windowAttempts(s, latestSunday(now)) >= WeeklyAttemptLimit {
				s.Outcome = "retry-budget-exhausted"
			}
		}
		return save(home, s)
	})
}

// Calendar boundaries avoid skipping the next Sunday because last week's
// healthy-recovery wait finished a few minutes after 04:00.
func latestSunday(now time.Time) time.Time {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, now.Location())
	due := midnight.AddDate(0, 0, -int(now.Weekday()))
	if due.After(now) {
		due = due.AddDate(0, 0, -7)
	}
	return due
}

func windowAttempts(s State, window time.Time) int {
	if s.AttemptWindow.Equal(window) {
		return s.Attempts
	}
	// Upgrade old receipts conservatively: their last scheduled attempt counts.
	if s.AttemptWindow.IsZero() && !s.LastAttempt.IsZero() && !s.LastAttempt.Before(window) {
		return 1
	}
	return 0
}
func retryStatus(st Status, now time.Time) Status {
	window := latestSunday(now)
	used := windowAttempts(st.State, window)
	st.AttemptsRemaining = WeeklyAttemptLimit - used
	if st.AttemptsRemaining < 0 {
		st.AttemptsRemaining = 0
	}
	completed := !st.LastSuccess.IsZero() && !st.LastSuccess.Before(window)
	st.BudgetExhausted = used >= WeeklyAttemptLimit && !completed
	if !st.Enabled {
		return st
	}
	if st.BudgetExhausted || completed {
		next := window.AddDate(0, 0, 7)
		st.NextEligible = &next
	} else if !st.LastAttempt.IsZero() && !st.LastAttempt.Before(window) {
		next := st.LastAttempt.Add(time.Hour)
		if next.After(now) {
			st.NextEligible = &next
		}
	}
	return st
}
