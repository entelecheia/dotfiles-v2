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
