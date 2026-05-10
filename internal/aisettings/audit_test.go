package aisettings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendTailAndSummarizeAIEvents(t *testing.T) {
	home := t.TempDir()
	for _, typ := range []string{"ai.backup", "ai.agents.apply", "ai.agents.apply"} {
		if _, err := AppendAIEvent(home, typ, map[string]any{"path": "/tmp/example"}); err != nil {
			t.Fatalf("AppendAIEvent(%s): %v", typ, err)
		}
	}
	tail, malformed, err := TailAIEvents(home, 2)
	if err != nil {
		t.Fatalf("TailAIEvents: %v", err)
	}
	if malformed != 0 {
		t.Fatalf("malformed = %d, want 0", malformed)
	}
	if len(tail) != 2 || tail[0].Type != "ai.agents.apply" || tail[1].Type != "ai.agents.apply" {
		t.Fatalf("tail = %+v, want last two apply events", tail)
	}
	sum, err := SummarizeAIEvents(home)
	if err != nil {
		t.Fatalf("SummarizeAIEvents: %v", err)
	}
	if sum.Total != 3 || sum.ByType["ai.agents.apply"] != 2 || sum.ByType["ai.backup"] != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if sum.Path != filepath.Join(home, ".local", "share", "dotfiles", "ai", "events.jsonl") {
		t.Fatalf("path = %s", sum.Path)
	}
}

func TestReadAIEventsCountsMalformedLines(t *testing.T) {
	home := t.TempDir()
	path := AIEventsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{bad json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	events, malformed, err := ReadAIEvents(home)
	if err != nil {
		t.Fatalf("ReadAIEvents: %v", err)
	}
	if len(events) != 0 || malformed != 1 {
		t.Fatalf("events=%d malformed=%d, want 0/1", len(events), malformed)
	}
}

func TestAppendAIEventRestrictsLogPermissions(t *testing.T) {
	home := t.TempDir()
	path := AIEventsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := AppendAIEvent(home, "ai.backup", map[string]any{"path": "/tmp/example"}); err != nil {
		t.Fatalf("AppendAIEvent: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("audit log mode = %o, want 600", got)
	}
}

func TestTailAIEventsHandlesLargeLines(t *testing.T) {
	home := t.TempDir()
	payload := map[string]any{
		"path": "/tmp/example",
		"note": strings.Repeat("x", 128*1024),
	}
	if _, err := AppendAIEvent(home, "ai.agents.apply", payload); err != nil {
		t.Fatalf("AppendAIEvent: %v", err)
	}
	if _, err := AppendAIEvent(home, "ai.backup", map[string]any{"path": "/tmp/second"}); err != nil {
		t.Fatalf("AppendAIEvent: %v", err)
	}

	tail, malformed, err := TailAIEvents(home, 1)
	if err != nil {
		t.Fatalf("TailAIEvents: %v", err)
	}
	if malformed != 0 {
		t.Fatalf("malformed = %d, want 0", malformed)
	}
	if len(tail) != 1 || tail[0].Type != "ai.backup" {
		t.Fatalf("tail = %+v, want last event only", tail)
	}
}
