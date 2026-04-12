package rsync

import (
	"bufio"
	"context"
	"os"
	"regexp"
	"strings"
	"time"
)

// Status holds rsync sync status information.
type Status struct {
	RsyncVersion   string
	RsyncPath      string
	LastSyncTime   *time.Time
	SchedulerState SchedulerState
	LastResult     string // e.g. "pull=0 push=0"
	LocalPath      string
	RemoteHost     string
	RemotePath     string
	Interval       int
}

// GetStatus assembles the current rsync sync status.
func GetStatus(ctx context.Context, sched *Scheduler, cfg *Config) (*Status, error) {
	st := &Status{
		RsyncPath:  cfg.RsyncPath,
		LocalPath:  cfg.LocalPath,
		RemoteHost: cfg.RemoteHost,
		RemotePath: cfg.RemotePath,
		Interval:   cfg.Interval,
	}

	if ver, ok := CheckRsync(sched.Runner); ok {
		st.RsyncVersion = ver
	}

	st.SchedulerState = sched.State(ctx)

	if info, err := os.Stat(cfg.LogFile); err == nil {
		t := info.ModTime()
		st.LastSyncTime = &t
	}

	if content, err := TailLog(cfg.LogFile, 20); err == nil {
		st.LastResult = extractLastResult(content)
	}

	return st, nil
}

// TailLog returns the last n lines from the sync log file.
func TailLog(logPath string, n int) (string, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

// resultRegex matches log lines like: "2026-04-12 15:30:00 pull=0 push=0"
var resultRegex = regexp.MustCompile(`pull=(\d+)\s+push=(\d+)`)

// extractLastResult finds the most recent pull/push result from log content.
func extractLastResult(logContent string) string {
	lines := strings.Split(logContent, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if m := resultRegex.FindString(lines[i]); m != "" {
			return m
		}
	}
	return ""
}
