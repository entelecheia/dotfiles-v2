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

// ScanWindowServerWatchdog scans dir (~/Library/Logs/DiagnosticReports) for
// WindowServer*.ips crash reports, examines the newest newestN by mtime, and
// returns the timestamp of the newest watchdog termination found. No
// recursion. A missing directory is an empty scan, not an error: a machine
// with no reports directory simply has no WindowServer evidence. The bool
// reports whether the scan completed; any read failure makes it false so the
// gate defers instead of assuming quiet.
func ScanWindowServerWatchdog(dir string, newestN int) (time.Time, bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return time.Time{}, true, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	type report struct {
		path  string
		mtime time.Time
	}
	var reports []report
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "WindowServer") || !strings.HasSuffix(name, ".ips") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return time.Time{}, false, err
		}
		reports = append(reports, report{path: filepath.Join(dir, name), mtime: info.ModTime()})
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].mtime.After(reports[j].mtime) })
	if newestN > 0 && len(reports) > newestN {
		reports = reports[:newestN]
	}
	var latest time.Time
	for _, r := range reports {
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
