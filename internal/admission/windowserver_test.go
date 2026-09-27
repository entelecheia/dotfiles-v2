package admission

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const watchdogIPS = `{"app_name":"WindowServer","timestamp":"2026-09-27 14:42:20.00 +0900","app_version":"1.0"}
{"captureTime" : "2026-09-27 14:42:13.0000 +0900","name" : "WindowServer","bug_type" : "309","termination" : {"code" : 999,"namespace" : "WATCHDOG","reasons" : ["( monitored): unresponsive for 40s"]}}
`

const cleanIPS = `{"app_name":"WindowServer","timestamp":"2026-09-27 10:00:00.00 +0900","app_version":"1.0"}
{"captureTime" : "2026-09-27 10:00:00.0000 +0900","name" : "WindowServer","bug_type" : "298"}
`

// cleanIPSLate is a non-watchdog report captured after the watchdog one.
const cleanIPSLate = `{"app_name":"WindowServer","timestamp":"2026-09-27 15:00:00.00 +0900","app_version":"1.0"}
{"captureTime" : "2026-09-27 15:00:00.0000 +0900","name" : "WindowServer","bug_type" : "298"}
`

func writeIPS(t *testing.T, dir, name, content string, mtime time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestScanWindowServerWatchdog(t *testing.T) {
	dir := t.TempDir()
	event := time.Date(2026, 9, 27, 14, 42, 13, 0, time.FixedZone("", 9*3600))
	writeIPS(t, dir, "WindowServer-2026-09-27-144213.ips", watchdogIPS, event)
	writeIPS(t, dir, "WindowServer-2026-09-27-100000.ips", cleanIPS, event.Add(-time.Hour))
	writeIPS(t, dir, "kernel-2026-09-27.ips", watchdogIPS, event.Add(time.Hour)) // wrong prefix
	if err := os.Mkdir(filepath.Join(dir, "WindowServer-subdir.ips"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ScanWindowServerWatchdog(dir, wsScanNewest)
	if err != nil || !ok {
		t.Fatalf("scan = %v, %v, %v", got, ok, err)
	}
	if !got.Equal(event) {
		t.Errorf("event = %v, want %v (the captureTime of the watchdog report)", got, event)
	}
}

func TestScanWindowServerWatchdogBoundedNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	// Ten watchdog reports, newest by mtime last. The scan bound must keep
	// the oldest ones out.
	for i := 0; i < 10; i++ {
		mtime := base.Add(time.Duration(i) * time.Minute)
		content := fmt.Sprintf(`{"captureTime" : "2026-09-27 12:%02d:00.0000 +0000","termination":{"namespace":"WATCHDOG"}}`, i)
		writeIPS(t, dir, fmt.Sprintf("WindowServer-%02d.ips", i), content, mtime)
	}
	got, ok, err := ScanWindowServerWatchdog(dir, 3)
	if err != nil || !ok {
		t.Fatalf("scan = %v, %v, %v", got, ok, err)
	}
	// Newest three carry captureTimes of minute 7, 8, 9.
	want := time.Date(2026, 9, 27, 12, 9, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("bounded scan event = %v, want %v", got, want)
	}
}

func TestScanWindowServerWatchdogFallsBackToMtime(t *testing.T) {
	dir := t.TempDir()
	mtime := time.Date(2026, 9, 27, 14, 42, 13, 0, time.UTC)
	writeIPS(t, dir, "WindowServer-noct.ips", `{"termination":{"namespace":"WATCHDOG"}}`, mtime)
	got, ok, err := ScanWindowServerWatchdog(dir, wsScanNewest)
	if err != nil || !ok {
		t.Fatalf("scan = %v, %v, %v", got, ok, err)
	}
	if !got.Equal(mtime) {
		t.Errorf("event = %v, want mtime fallback %v", got, mtime)
	}
}

func TestScanWindowServerWatchdogMissingDir(t *testing.T) {
	got, ok, err := ScanWindowServerWatchdog(filepath.Join(t.TempDir(), "nope"), wsScanNewest)
	if err != nil || !ok {
		t.Fatalf("missing dir = %v, %v, %v; want empty successful scan", got, ok, err)
	}
	if !got.IsZero() {
		t.Errorf("event = %v, want zero", got)
	}
}

func TestParseIPSCaptureTime(t *testing.T) {
	ts, ok := ParseIPSCaptureTime([]byte(watchdogIPS))
	if !ok {
		t.Fatal("captureTime not found in fixture")
	}
	want := time.Date(2026, 9, 27, 14, 42, 13, 0, time.FixedZone("", 9*3600))
	if !ts.Equal(want) {
		t.Errorf("captureTime = %v, want %v (fractional seconds dropped)", ts, want)
	}
	if _, ok := ParseIPSCaptureTime([]byte(`{"timestamp":"2026-09-27 14:42:20.00 +0900"}`)); ok {
		t.Error("metadata timestamp must not parse as captureTime")
	}
}

func TestIsWatchdogTermination(t *testing.T) {
	if !IsWatchdogTermination([]byte(watchdogIPS)) {
		t.Error("watchdog fixture not detected")
	}
	if IsWatchdogTermination([]byte(cleanIPS)) {
		t.Error("clean fixture detected as watchdog")
	}
}

// TestScanWindowServerWatchdogByName: spin reports carry the cause in the
// file name, not a WATCHDOG termination block; other WindowServer reports
// (cpu_resource.diag) are not evidence (#165).
func TestScanWindowServerWatchdogByName(t *testing.T) {
	dir := t.TempDir()
	spin := time.Date(2026, 9, 27, 13, 14, 18, 0, time.UTC)
	writeIPS(t, dir, "WindowServer_2026-09-27-131418_Mac.userspace_watchdog_timeout.spin", "not json", spin)
	writeIPS(t, dir, "WindowServer_2026-09-27-150000_Mac.cpu_resource.diag", "not json", spin.Add(time.Hour))
	got, ok, err := ScanWindowServerWatchdog(dir, wsScanNewest)
	if err != nil || !ok {
		t.Fatalf("scan = %v, %v", ok, err)
	}
	if !got.Equal(spin) {
		t.Errorf("event = %v, want the spin report's mtime %v", got, spin)
	}
}

// TestScanWindowServerDirs: the newest event across the system folder, its
// Retired/ subfolder and a missing user folder (#165).
func TestScanWindowServerDirs(t *testing.T) {
	system := t.TempDir()
	retired := filepath.Join(system, "Retired")
	if err := os.Mkdir(retired, 0o755); err != nil {
		t.Fatal(err)
	}
	writeIPS(t, system, "WindowServer_2026-09-27-131418_Mac.userspace_watchdog_timeout.spin", "x", time.Date(2026, 9, 27, 4, 14, 18, 0, time.UTC))
	writeIPS(t, retired, "WindowServer-2026-09-27-134223.ips", watchdogIPS, time.Date(2026, 9, 27, 5, 42, 23, 0, time.UTC))
	// A newer .ips without WATCHDOG must not win: it is not evidence.
	writeIPS(t, retired, "WindowServer-2026-09-27-150000.ips", cleanIPSLate, time.Date(2026, 9, 27, 6, 0, 0, 0, time.UTC))
	got, ok, err := ScanWindowServerDirs([]string{system, retired, filepath.Join(t.TempDir(), "missing")}, wsScanNewest)
	if err != nil || !ok {
		t.Fatalf("scan = %v, %v", ok, err)
	}
	want := time.Date(2026, 9, 27, 14, 42, 13, 0, time.FixedZone("", 9*3600))
	if !got.Equal(want) {
		t.Errorf("event = %v, want the Retired watchdog report's captureTime %v", got, want)
	}
}

// TestScanWindowServerDirsUnreadable: an unreadable report in any folder
// leaves the scan incomplete, so the gate defers.
func TestScanWindowServerDirsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads mode-000 files")
	}
	dir := t.TempDir()
	writeIPS(t, dir, "WindowServer-2026-09-27-134223.ips", watchdogIPS, time.Now())
	if err := os.Chmod(filepath.Join(dir, "WindowServer-2026-09-27-134223.ips"), 0); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ScanWindowServerDirs([]string{t.TempDir(), dir}, wsScanNewest); ok {
		t.Error("an unreadable report must leave the scan incomplete")
	}
}

func TestWindowServerReportDirs(t *testing.T) {
	got := WindowServerReportDirs("/Users/x")
	want := []string{"/Library/Logs/DiagnosticReports", "/Library/Logs/DiagnosticReports/Retired", "/Users/x/Library/Logs/DiagnosticReports", "/Users/x/Library/Logs/DiagnosticReports/Retired"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("dirs = %v, want %v", got, want)
	}
}

// TestScanWindowServerWatchdogNamedIPS: a watchdog-named .ips counts by its
// name even when its content lacks WATCHDOG.
func TestScanWindowServerWatchdogNamedIPS(t *testing.T) {
	dir := t.TempDir()
	mtime := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	writeIPS(t, dir, "WindowServer_2026-09-27_Mac.watchdog.ips", cleanIPS, mtime)
	got, ok, err := ScanWindowServerWatchdog(dir, wsScanNewest)
	if err != nil || !ok || !got.Equal(mtime) {
		t.Fatalf("scan = %v, %v, %v; want the named report's mtime %v", got, ok, err, mtime)
	}
}

// TestScanWindowServerDirsUnreadableFolder: a folder that exists but cannot be
// listed (the standard-account case for the system folders) keeps the scan
// incomplete; it is not an empty scan.
func TestScanWindowServerDirsUnreadableFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists mode-000 folders")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, ok, _ := ScanWindowServerDirs([]string{t.TempDir(), dir}, wsScanNewest); ok {
		t.Error("an unreadable folder must leave the scan incomplete")
	}
}

// TestScanWindowServerDirsNewestWins: the newest event wins even when an
// earlier folder holds it; a later folder's older event must not replace it.
func TestScanWindowServerDirsNewestWins(t *testing.T) {
	system, retired := t.TempDir(), t.TempDir()
	fresh := time.Date(2026, 9, 27, 6, 30, 0, 0, time.UTC)
	writeIPS(t, system, "WindowServer_2026-09-27_Mac.userspace_watchdog_timeout.spin", "x", fresh)
	writeIPS(t, retired, "WindowServer-2026-09-27-134223.ips", watchdogIPS, time.Date(2026, 9, 27, 5, 42, 23, 0, time.UTC))
	got, ok, err := ScanWindowServerDirs([]string{system, retired}, wsScanNewest)
	if err != nil || !ok || !got.Equal(fresh) {
		t.Fatalf("event = %v, %v, %v; want the newer top-folder report %v", got, ok, err, fresh)
	}
}
