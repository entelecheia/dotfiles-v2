package sync

import (
	"bufio"
	"context"
	"os"
	"strings"
	"time"
)

// Status holds sync status information.
type Status struct {
	RcloneVersion  string
	RclonePath     string
	LastSyncTime   *time.Time
	SchedulerState SchedulerState
	LastError      string
	LocalPath      string
	RemotePath     string
	FilterFile     string
	Interval       int
}

// GetStatus assembles the current sync status.
func GetStatus(ctx context.Context, sched *Scheduler, cfg *Config) (*Status, error) {
	st := &Status{
		RclonePath: cfg.RclonePath,
		LocalPath:  cfg.LocalPath,
		RemotePath: cfg.RemotePath,
		FilterFile: cfg.FilterFile,
		Interval:   cfg.Interval,
	}

	// Rclone version
	if ver, ok := CheckRclone(sched.Runner); ok {
		st.RcloneVersion = ver
	}

	// Scheduler state
	st.SchedulerState = sched.State(ctx)

	// Last sync time from log file mod time
	if info, err := os.Stat(cfg.LogFile); err == nil {
		t := info.ModTime()
		st.LastSyncTime = &t
	}

	// Last error from log
	if lines, err := TailLog(cfg.LogFile, 50); err == nil {
		st.LastError = extractLastError(lines)
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

// extractLastError scans log output for the last ERROR line.
func extractLastError(logContent string) string {
	lines := strings.Split(logContent, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "ERROR") {
			return strings.TrimSpace(lines[i])
		}
	}
	return ""
}
