package watchdog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Sample is the persisted per-process-incarnation observation. OverSince is
// when the process first exceeded the CPU threshold in the current run of
// consecutive over-threshold samples; it resets to zero when a sample lands
// below the threshold.
type Sample struct {
	OverSince time.Time `json:"over_since"`
	LastSeen  time.Time `json:"last_seen"`
	CPU       float64   `json:"cpu"`
	Args      string    `json:"args"`
}

// Samples maps a Process.StartKey to its observation.
type Samples map[string]Sample

// LoadSamples reads the samples file. A missing file is an empty set, not an
// error: the first run has no history.
func LoadSamples(path string) (Samples, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Samples{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading samples: %w", err)
	}
	var s Samples
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing samples %s: %w", path, err)
	}
	if s == nil {
		s = Samples{}
	}
	return s, nil
}

// SaveSamples writes the samples file atomically (temp + rename), so a
// reaper run that dies mid-write cannot tear the file the next run reads.
func SaveSamples(path string, s Samples) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating samples dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding samples: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".samples-*")
	if err != nil {
		return fmt.Errorf("creating samples temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing samples: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing samples: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing samples: %w", err)
	}
	return nil
}
