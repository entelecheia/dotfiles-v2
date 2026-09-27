package watchdog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Event is one JSON-lines record in the watchdog log.
type Event struct {
	Time   time.Time `json:"ts"`
	Level  string    `json:"level"` // info | warn | critical
	Event  string    `json:"event"` // candidate | reap | notify | error
	PID    int       `json:"pid,omitempty"`
	CPU    float64   `json:"cpu,omitempty"`
	Args   string    `json:"args,omitempty"`
	Action string    `json:"action,omitempty"` // dry-run | sigterm | sigkill | already-gone
	Msg    string    `json:"msg,omitempty"`
}

// AppendEvent adds one JSON line to the log, creating the parent directory
// on first use.
func AppendEvent(path string, e Event) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encoding log event: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing log: %w", err)
	}
	return nil
}

// TailLog returns the last n raw lines of the log, oldest first. A missing
// log is an empty result, not an error.
func TailLog(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening log: %w", err)
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading log: %w", err)
	}
	return lines, nil
}
