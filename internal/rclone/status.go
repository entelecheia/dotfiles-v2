package rclone

import (
	"bufio"
	"context"
	"os"
	"regexp"
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
	LastStats      string // e.g. "12 files, 4.2 MiB, 0 errors"
	LocalPath      string
	RemotePath     string
	FilterFile     string
	MountPoint     string
	Mounted        bool
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

	if ver, ok := CheckRclone(sched.Runner); ok {
		st.RcloneVersion = ver
	}

	st.SchedulerState = sched.State(ctx)

	if sched.Paths != nil {
		st.MountPoint = sched.Paths.MountPoint
		st.Mounted = IsMounted(sched.Paths.MountPoint)
	}

	if info, err := os.Stat(cfg.LogFile); err == nil {
		t := info.ModTime()
		st.LastSyncTime = &t
	}

	if lines, err := TailLog(cfg.LogFile, 100); err == nil {
		st.LastError = extractLastError(lines)
		st.LastStats = extractLastStats(lines)
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
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)
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

// transferredRegex matches rclone's summary line:
// "Transferred: 4.735 KiB / 4.735 KiB, 100%, 0 B/s, ETA -"
var transferredRegex = regexp.MustCompile(`Transferred:\s+(\S+\s\S+)\s*/\s*(\S+\s\S+),`)

// errorsRegex matches: "Errors:                 2 (fatal error encountered)"
var errorsRegex = regexp.MustCompile(`Errors:\s+(\d+)`)

// filesRegex matches: "Transferred:            1 / 1, 100%"
var filesRegex = regexp.MustCompile(`Transferred:\s+(\d+)\s*/\s*(\d+),\s+\d+%`)

// extractLastStats scans for the last rclone transfer summary block.
func extractLastStats(logContent string) string {
	lines := strings.Split(logContent, "\n")
	var bytes, files, errs string
	for i := len(lines) - 1; i >= 0; i-- {
		if bytes == "" {
			if m := transferredRegex.FindStringSubmatch(lines[i]); len(m) >= 2 {
				bytes = strings.TrimSpace(m[1])
			}
		}
		if files == "" {
			if m := filesRegex.FindStringSubmatch(lines[i]); len(m) >= 2 {
				files = m[1]
			}
		}
		if errs == "" {
			if m := errorsRegex.FindStringSubmatch(lines[i]); len(m) >= 2 {
				errs = m[1]
			}
		}
		if bytes != "" && files != "" && errs != "" {
			break
		}
	}
	if bytes == "" && files == "" && errs == "" {
		return ""
	}
	parts := []string{}
	if files != "" {
		parts = append(parts, files+" files")
	}
	if bytes != "" {
		parts = append(parts, bytes)
	}
	if errs != "" && errs != "0" {
		parts = append(parts, errs+" errors")
	}
	return strings.Join(parts, ", ")
}
