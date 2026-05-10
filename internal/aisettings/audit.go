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

const maxAIEventLineBytes = 10 * 1024 * 1024

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
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return AIEventV1{}, fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return AIEventV1{}, fmt.Errorf("chmod audit log: %w", err)
	}
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
	var events []AIEventV1
	malformed, err := scanAIEvents(homeDir, func(event AIEventV1) {
		events = append(events, event)
	})
	if err != nil {
		return nil, malformed, err
	}
	return events, malformed, nil
}

// TailAIEvents returns the last n parseable audit events.
func TailAIEvents(homeDir string, n int) ([]AIEventV1, int, error) {
	if n <= 0 {
		n = 20
	}
	ring := make([]AIEventV1, n)
	total := 0
	malformed, err := scanAIEvents(homeDir, func(event AIEventV1) {
		ring[total%n] = event
		total++
	})
	if err != nil {
		return nil, malformed, err
	}
	count := total
	if count > n {
		count = n
	}
	events := make([]AIEventV1, 0, count)
	start := total - count
	for i := 0; i < count; i++ {
		events = append(events, ring[(start+i)%n])
	}
	return events, malformed, nil
}

// SummarizeAIEvents counts audit events by type.
func SummarizeAIEvents(homeDir string) (*AIEventSummary, error) {
	sum := &AIEventSummary{
		Path:   AIEventsPath(homeDir),
		ByType: map[string]int{},
	}
	malformed, err := scanAIEvents(homeDir, func(event AIEventV1) {
		sum.Total++
		sum.ByType[event.Type]++
		last := event
		sum.LastEvent = &last
	})
	if err != nil {
		return nil, err
	}
	sum.Malformed = malformed
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

func scanAIEvents(homeDir string, visit func(AIEventV1)) (int, error) {
	path := AIEventsPath(homeDir)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	malformed := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxAIEventLineBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		var event AIEventV1
		if err := json.Unmarshal(line, &event); err != nil {
			malformed++
			continue
		}
		visit(event)
	}
	if err := scanner.Err(); err != nil {
		return malformed, fmt.Errorf("read audit log: %w", err)
	}
	return malformed, nil
}
