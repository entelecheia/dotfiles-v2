package admission

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DefaultWSGrace is how long a WindowServer watchdog termination defers new
// heavy work. The 2026-09-27 incident showed four terminations inside forty
// minutes before the reboot, so the window must span the storm, not the
// single event.
const DefaultWSGrace = 30 * time.Minute

// wsScanNewest bounds the DiagnosticReports scan to the newest few reports:
// WindowServer storms write several .ips files, and anything older than the
// newest handful is outside the grace window anyway.
const wsScanNewest = 20

// wsReadLimit bounds each report read. The termination block and captureTime
// sit at the top of the report JSON; multi-megabyte stack content below them
// is irrelevant to the scan.
const wsReadLimit = 256 * 1024

// ScanWindowServerWatchdog scans one DiagnosticReports folder for WindowServer
// watchdog evidence and returns the timestamp of the newest event. Evidence is
// a WindowServer*.ips report recording a WATCHDOG termination (timestamped by
// its captureTime) or a WindowServer* file whose name contains "watchdog"
// (timestamped by mtime). Only the newest newestN candidates by mtime are
// examined. No recursion. A missing directory is an empty scan, not an error.
// The bool reports whether the scan completed; any read failure makes it
// false so the gate defers instead of assuming quiet.
func ScanWindowServerWatchdog(dir string, newestN int) (time.Time, bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return time.Time{}, true, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	type report struct {
		path   string
		mtime  time.Time
		byName bool // evidence is the file name, not the content
	}
	var reports []report
	for _, e := range entries {
		name := e.Name()
		isIPS := strings.HasSuffix(name, ".ips")
		// Spin and other watchdog reports name the cause in the file name
		// (WindowServer_..._userspace_watchdog_timeout.spin).
		byName := !isIPS && strings.Contains(strings.ToLower(name), "watchdog")
		if e.IsDir() || !strings.HasPrefix(name, "WindowServer") || (!isIPS && !byName) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return time.Time{}, false, err
		}
		reports = append(reports, report{path: filepath.Join(dir, name), mtime: info.ModTime(), byName: byName})
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].mtime.After(reports[j].mtime) })
	if newestN > 0 && len(reports) > newestN {
		reports = reports[:newestN]
	}
	var latest time.Time
	for _, r := range reports {
		if r.byName {
			if r.mtime.After(latest) {
				latest = r.mtime
			}
			continue
		}
		content, err := readBounded(r.path, wsReadLimit)
		if err != nil {
			return time.Time{}, false, err
		}
		if !IsWatchdogTermination(content) {
			continue
		}
		ts, ok := ParseIPSCaptureTime(content)
		if !ok {
			ts = r.mtime
		}
		if ts.After(latest) {
			latest = ts
		}
	}
	return latest, true, nil
}

// WindowServerReportDirs lists where macOS writes WindowServer diagnostics:
// the system folder, its Retired/ subfolder (processed .ips reports move
// there), and the user folder (#165).
func WindowServerReportDirs(home string) []string {
	const system = "/Library/Logs/DiagnosticReports"
	return []string{system, filepath.Join(system, "Retired"), filepath.Join(home, "Library", "Logs", "DiagnosticReports")}
}

// ScanWindowServerDirs scans each folder with ScanWindowServerWatchdog and
// returns the newest event across them. The scan is complete only when every
// folder scanned completely.
func ScanWindowServerDirs(dirs []string, newestN int) (time.Time, bool, error) {
	var latest time.Time
	for _, dir := range dirs {
		ts, ok, err := ScanWindowServerWatchdog(dir, newestN)
		if err != nil || !ok {
			return time.Time{}, false, err
		}
		if ts.After(latest) {
			latest = ts
		}
	}
	return latest, true, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// IsWatchdogTermination reports whether an .ips report records a watchdog
// kill (a "WATCHDOG" termination namespace). The match is a plain substring:
// a stray hit only defers admission, which is the safe side, while a miss
// would admit work onto a machine that just killed its window server.
func IsWatchdogTermination(content []byte) bool {
	return bytes.Contains(content, []byte("WATCHDOG"))
}

var captureTimeRE = regexp.MustCompile(`"captureTime"\s*:\s*"([^"]+)"`)

// ParseIPSCaptureTime extracts the report's captureTime. An .ips file writes
// a metadata JSON object on line 1 (which uses "timestamp") and the report
// JSON from line 2 (which carries "captureTime" as
// "2026-09-27 14:42:13.0000 +0900"), so the first captureTime match is the
// report's. The fractional seconds are dropped before parsing.
func ParseIPSCaptureTime(content []byte) (time.Time, bool) {
	m := captureTimeRE.FindSubmatch(content)
	if m == nil {
		return time.Time{}, false
	}
	raw := string(m[1])
	if dot := strings.IndexByte(raw, '.'); dot >= 0 {
		rest := raw[dot+1:]
		if i := strings.IndexByte(rest, ' '); i >= 0 {
			raw = raw[:dot] + rest[i:]
		} else {
			raw = raw[:dot]
		}
	}
	ts, err := time.Parse("2006-01-02 15:04:05 -0700", raw)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}
