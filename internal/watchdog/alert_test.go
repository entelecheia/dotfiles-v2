package watchdog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sentMessage records one Notify call for the fake below.
type sentMessage struct {
	Level string
	Msg   string
}

// fakeSender records Notify calls; the Alerter's contract is about when
// Notify is called, not about delivery.
type fakeSender struct {
	sent    []sentMessage
	failErr error // when set, Notify returns it
}

func (f *fakeSender) Notify(_ context.Context, level, msg string) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.sent = append(f.sent, sentMessage{Level: level, Msg: msg})
	return nil
}

func newTestAlerter(t *testing.T, interval time.Duration, now func() time.Time) (*Alerter, *fakeSender, string) {
	t.Helper()
	fake := &fakeSender{}
	statePath := filepath.Join(t.TempDir(), "alert-state.json")
	a := &Alerter{
		Notifier:         fake,
		StatePath:        statePath,
		ReminderInterval: interval,
		Now:              now,
	}
	return a, fake, statePath
}

func TestAlerter_FirstFailureSendsOnce(t *testing.T) {
	a, fake, statePath := newTestAlerter(t, 30*time.Minute, time.Now)
	if err := a.ReportFailure(context.Background(), "critical", "boom"); err != nil {
		t.Fatalf("ReportFailure: %v", err)
	}
	if len(fake.sent) != 1 || fake.sent[0].Level != "critical" || fake.sent[0].Msg != "boom" {
		t.Fatalf("sent = %#v", fake.sent)
	}
	state := readAlertState(t, statePath)
	if !state.Failing || state.Since.IsZero() || state.LastSent.IsZero() || state.LastMessage != "boom" {
		t.Errorf("state = %#v", state)
	}
}

func TestAlerter_RepeatWithinIntervalSendsNothing(t *testing.T) {
	base := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	now := base
	a, fake, statePath := newTestAlerter(t, 30*time.Minute, func() time.Time { return now })
	mustFail(t, a, "first")
	now = base.Add(10 * time.Minute)
	mustFail(t, a, "second")
	if len(fake.sent) != 1 {
		t.Fatalf("a repeat inside the reminder interval must not send, sent = %#v", fake.sent)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("a deduped repeat must still leave the state file intact: %v", err)
	}
}

func TestAlerter_ReminderAfterInterval(t *testing.T) {
	base := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	now := base
	a, fake, statePath := newTestAlerter(t, 30*time.Minute, func() time.Time { return now })
	mustFail(t, a, "first")
	now = base.Add(31 * time.Minute)
	mustFail(t, a, "still broken")
	if len(fake.sent) != 2 {
		t.Fatalf("sent = %#v, want transition + one reminder", fake.sent)
	}
	if got := fake.sent[1].Msg; !strings.Contains(got, "still failing since") || !strings.Contains(got, "still broken") {
		t.Errorf("reminder = %q", got)
	}
	state := readAlertState(t, statePath)
	if !state.Since.Equal(base) {
		t.Errorf("reminder must keep the original Since, got %v", state.Since)
	}
	if !state.LastSent.Equal(now) {
		t.Errorf("LastSent = %v, want %v", state.LastSent, now)
	}
}

func TestAlerter_ZeroIntervalDisablesReminders(t *testing.T) {
	base := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	now := base
	a, fake, _ := newTestAlerter(t, 0, func() time.Time { return now })
	mustFail(t, a, "first")
	now = base.Add(time.Hour)
	mustFail(t, a, "second")
	if len(fake.sent) != 1 {
		t.Fatalf("interval <= 0 must suppress reminders, sent = %#v", fake.sent)
	}
}

func TestAlerter_SuccessWhileHealthyIsSilent(t *testing.T) {
	a, fake, statePath := newTestAlerter(t, 30*time.Minute, time.Now)
	if err := a.ReportSuccess(context.Background(), "recovered"); err != nil {
		t.Fatalf("ReportSuccess: %v", err)
	}
	if len(fake.sent) != 0 {
		t.Fatalf("success without an open episode must not send, sent = %#v", fake.sent)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("no state file must be written, stat err = %v", err)
	}
}

func TestAlerter_RecoverySendsAndClears(t *testing.T) {
	a, fake, statePath := newTestAlerter(t, 30*time.Minute, time.Now)
	mustFail(t, a, "boom")
	if err := a.ReportSuccess(context.Background(), "recovered: baseline committed"); err != nil {
		t.Fatalf("ReportSuccess: %v", err)
	}
	if len(fake.sent) != 2 {
		t.Fatalf("sent = %#v, want failure + recovery", fake.sent)
	}
	if fake.sent[1].Level != "info" || fake.sent[1].Msg != "recovered: baseline committed" {
		t.Errorf("recovery = %#v", fake.sent[1])
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("recovery must remove the state file (absence IS the zero state), stat err = %v", err)
	}
	// A later failure is a NEW episode and sends again.
	mustFail(t, a, "boom again")
	if len(fake.sent) != 3 {
		t.Fatalf("a post-recovery failure must alert as a fresh transition, sent = %#v", fake.sent)
	}
}

func TestAlerter_FailedSendRetriesTransition(t *testing.T) {
	a, fake, statePath := newTestAlerter(t, 30*time.Minute, time.Now)
	fake.failErr = errors.New("delivery down")
	if err := a.ReportFailure(context.Background(), "critical", "boom"); err == nil {
		t.Fatal("a failed send must surface as an error")
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Error("state must not persist before a successful send, or the transition is lost")
	}
	fake.failErr = nil
	mustFail(t, a, "boom")
	if len(fake.sent) != 1 {
		t.Fatalf("the next run must retry the transition (the failed send recorded nothing), sent = %#v", fake.sent)
	}
	state := readAlertState(t, statePath)
	if !state.Failing {
		t.Error("the retried transition must persist its state")
	}
}

func TestAlerter_FailedRecoveryKeepsEpisode(t *testing.T) {
	a, fake, statePath := newTestAlerter(t, 30*time.Minute, time.Now)
	mustFail(t, a, "boom")
	fake.failErr = errors.New("delivery down")
	if err := a.ReportSuccess(context.Background(), "recovered"); err == nil {
		t.Fatal("a failed recovery send must surface as an error")
	}
	state := readAlertState(t, statePath)
	if !state.Failing {
		t.Error("a failed recovery send must leave the episode open so the next run retries it")
	}
}

func TestAlerter_CorruptStateFails(t *testing.T) {
	a, _, statePath := newTestAlerter(t, 30*time.Minute, time.Now)
	if err := os.WriteFile(statePath, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.ReportFailure(context.Background(), "critical", "boom"); err == nil {
		t.Fatal("a corrupt state file must fail loudly, not silently reset the episode")
	}
}

func mustFail(t *testing.T, a *Alerter, msg string) {
	t.Helper()
	if err := a.ReportFailure(context.Background(), "critical", msg); err != nil {
		t.Fatalf("ReportFailure(%q): %v", msg, err)
	}
}

func readAlertState(t *testing.T, path string) AlertState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	var state AlertState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("state is not valid JSON: %v", err)
	}
	return state
}
