package aisettings

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// AIEventV1 is the append-only audit envelope for dot ai mutations.
type AIEventV1 struct {
	ID            string         `json:"id"`
	Timestamp     string         `json:"ts"`
	Type          string         `json:"type"`
	Actor         string         `json:"actor"`
	SchemaVersion string         `json:"schema_version"`
	Payload       map[string]any `json:"payload,omitempty"`
}

// AIEventSummary is a compact view over the append-only AI audit log.
type AIEventSummary struct {
	Path      string         `json:"path"`
	Total     int            `json:"total"`
	ByType    map[string]int `json:"by_type"`
	LastEvent *AIEventV1     `json:"last_event,omitempty"`
	Malformed int            `json:"malformed,omitempty"`
}

// AIEventsPath returns the dot ai append-only audit log path for a home dir.
func AIEventsPath(homeDir string) string {
	homeDir = normalizeHomeDir(homeDir)
	return filepath.Join(homeDir, ".local", "share", "dotfiles", "ai", "events.jsonl")
}

// AppendAIEvent appends one audit event as a JSONL record.
func AppendAIEvent(homeDir, typ string, payload map[string]any) (AIEventV1, error) {
	event := AIEventV1{
		ID:            newAIEventID(),
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Type:          typ,
		Actor:         "dot",
		SchemaVersion: "v1",
		Payload:       payload,
	}
	path := AIEventsPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return AIEventV1{}, fmt.Errorf("create audit dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return AIEventV1{}, fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()
	line, err := json.Marshal(event)
	if err != nil {
		return AIEventV1{}, fmt.Errorf("marshal audit event: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return AIEventV1{}, fmt.Errorf("append audit event: %w", err)
	}
	return event, nil
}

// ReadAIEvents returns parseable audit events and a malformed-line count.
func ReadAIEvents(homeDir string) ([]AIEventV1, int, error) {
	path := AIEventsPath(homeDir)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	var events []AIEventV1
	malformed := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		var event AIEventV1
		if err := json.Unmarshal(line, &event); err != nil {
			malformed++
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, malformed, fmt.Errorf("read audit log: %w", err)
	}
	return events, malformed, nil
}

// TailAIEvents returns the last n parseable audit events.
func TailAIEvents(homeDir string, n int) ([]AIEventV1, int, error) {
	if n <= 0 {
		n = 20
	}
	events, malformed, err := ReadAIEvents(homeDir)
	if err != nil {
		return nil, malformed, err
	}
	if len(events) <= n {
		return events, malformed, nil
	}
	return events[len(events)-n:], malformed, nil
}

// SummarizeAIEvents counts audit events by type.
func SummarizeAIEvents(homeDir string) (*AIEventSummary, error) {
	events, malformed, err := ReadAIEvents(homeDir)
	if err != nil {
		return nil, err
	}
	sum := &AIEventSummary{
		Path:      AIEventsPath(homeDir),
		Total:     len(events),
		ByType:    map[string]int{},
		Malformed: malformed,
	}
	for i := range events {
		sum.ByType[events[i].Type]++
	}
	if len(events) > 0 {
		last := events[len(events)-1]
		sum.LastEvent = &last
	}
	return sum, nil
}

// SortedAIEventTypes returns deterministic summary keys for printing.
func SortedAIEventTypes(sum *AIEventSummary) []string {
	if sum == nil {
		return nil
	}
	var keys []string
	for k := range sum.ByType {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func newAIEventID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%013d_%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}
