package watchdog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AlertState is the persisted ok/fail position of one alert source. A
// missing state file is the zero state (healthy, nothing sent).
type AlertState struct {
	Failing     bool      `json:"failing"`
	Since       time.Time `json:"since,omitempty"`
	LastSent    time.Time `json:"last_sent,omitempty"`
	LastMessage string    `json:"last_message,omitempty"`
}

// Alerter adds state-transition dedup and reminder rate limiting in front of
// a Notifier, so any channel the notifier grows gets the same behavior: one
// message at the ok→fail transition, at most one reminder per
// ReminderInterval while the condition persists, and one recovery message
// when a success is reported after a failure.
//
// State is persisted only after a successful send. A failed send returns the
// error and leaves the prior state untouched, so the next run retries the
// transition instead of losing it — the alert path failing silently is worse
// than a duplicate after a flaky send. A run that neither sends nor
// transitions writes nothing.
type Alerter struct {
	Notifier         NotifySender
	StatePath        string
	ReminderInterval time.Duration // <= 0 disables reminders (transitions and recovery still send)
	Now              func() time.Time
}

// NotifySender is the one method of Notifier the Alerter needs, named so
// tests can substitute a recording fake.
type NotifySender interface {
	Notify(ctx context.Context, level, msg string) error
}

func (a *Alerter) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// ReportFailure records a failure observation, sending when the state
// transitions to failing or the reminder interval has elapsed.
func (a *Alerter) ReportFailure(ctx context.Context, level, msg string) error {
	state, err := a.load()
	if err != nil {
		return err
	}
	now := a.now()
	if !state.Failing {
		if err := a.Notifier.Notify(ctx, level, msg); err != nil {
			return err
		}
		return a.save(AlertState{Failing: true, Since: now, LastSent: now, LastMessage: msg})
	}
	if a.ReminderInterval > 0 && now.Sub(state.LastSent) >= a.ReminderInterval {
		reminder := fmt.Sprintf("still failing since %s: %s", state.Since.Format(time.RFC3339), msg)
		if err := a.Notifier.Notify(ctx, level, reminder); err != nil {
			return err
		}
		state.LastSent = now
		state.LastMessage = msg
		return a.save(state)
	}
	return nil
}

// ReportSuccess records a healthy observation. After a failure episode it
// sends one recovery message and clears the episode by removing the state
// file — absence IS the zero state, so no tombstone is kept. While healthy
// it sends and writes nothing.
func (a *Alerter) ReportSuccess(ctx context.Context, msg string) error {
	state, err := a.load()
	if err != nil {
		return err
	}
	if !state.Failing {
		return nil
	}
	if err := a.Notifier.Notify(ctx, "info", msg); err != nil {
		return err
	}
	if err := os.Remove(a.StatePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clearing alert state %s: %w", a.StatePath, err)
	}
	return nil
}

func (a *Alerter) load() (AlertState, error) {
	data, err := os.ReadFile(a.StatePath)
	if os.IsNotExist(err) {
		return AlertState{}, nil
	}
	if err != nil {
		return AlertState{}, fmt.Errorf("reading alert state %s: %w", a.StatePath, err)
	}
	var state AlertState
	if err := json.Unmarshal(data, &state); err != nil {
		return AlertState{}, fmt.Errorf("parsing alert state %s: %w (corrupt; delete the file to reset the alert episode)", a.StatePath, err)
	}
	return state, nil
}

// save persists the state atomically (0600 temp + rename) so an interrupted
// write cannot truncate the previous position.
func (a *Alerter) save(state AlertState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.StatePath), 0o755); err != nil {
		return fmt.Errorf("creating alert state dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(a.StatePath), ".alert-state-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, a.StatePath); err != nil {
		return fmt.Errorf("replacing alert state %s: %w", a.StatePath, err)
	}
	return nil
}
