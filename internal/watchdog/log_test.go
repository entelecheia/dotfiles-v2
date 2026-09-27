package watchdog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLog_AppendAndTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Logs", "dot", "watchdog.log")
	base := time.Unix(1_800_000_000, 0)
	for i := 0; i < 5; i++ {
		e := Event{
			Time:   base.Add(time.Duration(i) * time.Minute),
			Level:  "warn",
			Event:  "candidate",
			PID:    100 + i,
			CPU:    95.5,
			Args:   "/tmp/x/bun",
			Action: ModeDryRun,
		}
		if err := AppendEvent(path, e); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	// Every line is valid JSON carrying the fields the CLI prints raw.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(raw) != 5 {
		t.Fatalf("log has %d lines, want 5", len(raw))
	}
	var decoded Event
	if err := json.Unmarshal([]byte(raw[0]), &decoded); err != nil {
		t.Fatalf("line 1 is not JSON: %v", err)
	}
	if decoded.PID != 100 || decoded.Action != ModeDryRun || decoded.CPU != 95.5 {
		t.Fatalf("line 1 round trip mismatch: %#v", decoded)
	}

	// Tail returns the newest N, oldest first.
	lines, err := TailLog(path, 2)
	if err != nil {
		t.Fatalf("TailLog: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("TailLog(2) = %d lines, want 2", len(lines))
	}
	var last Event
	if err := json.Unmarshal([]byte(lines[1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.PID != 104 {
		t.Fatalf("newest tailed PID = %d, want 104", last.PID)
	}

	// N larger than the file returns everything.
	all, err := TailLog(path, 50)
	if err != nil || len(all) != 5 {
		t.Fatalf("TailLog(50) = %d lines, %v; want 5", len(all), err)
	}
}

func TestLog_MissingFileTailsEmpty(t *testing.T) {
	lines, err := TailLog(filepath.Join(t.TempDir(), "absent.log"), 10)
	if err != nil || lines != nil {
		t.Fatalf("missing log = %v, %v; want nil, nil", lines, err)
	}
}
