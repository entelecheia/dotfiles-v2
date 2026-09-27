package watchdog

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func evalSettings() ReaperSettings {
	return ReaperSettings{
		Mode:         ModeEnforce,
		CPUThreshold: 50,
		Sustain:      30 * time.Minute,
		OrphanOnly:   true,
		Args:         []string{"ahub-recovery-cli-"},
	}
}

const runawayStart = "Thu Sep 24 10:15:30 2026"

func runawayProc(pid int, cpu float64, started string) Process {
	return Process{PID: pid, PPID: 1, CPU: cpu, Started: started, Args: "/tmp/ahub-recovery-cli-x/bun run"}
}

func TestEvaluate_FirstSightingIsNotACandidate(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cands, next := Evaluate([]Process{runawayProc(100, 95, runawayStart)}, Samples{}, evalSettings(), now, os.ExpandEnv)
	if len(cands) != 0 {
		t.Fatalf("first over-threshold sighting must not be a candidate: %#v", cands)
	}
	key := runawayProc(100, 95, runawayStart).StartKey()
	if next[key].OverSince != now {
		t.Fatalf("OverSince = %v, want %v (streak starts at first sighting)", next[key].OverSince, now)
	}
}

func TestEvaluate_SustainedAcrossRunsBecomesACandidate(t *testing.T) {
	s := evalSettings()
	start := time.Unix(1_800_000_000, 0)
	_, next := Evaluate([]Process{runawayProc(100, 95, runawayStart)}, Samples{}, s, start, os.ExpandEnv)
	// One interval later: still over threshold, streak now 5m old — not yet.
	later := start.Add(5 * time.Minute)
	cands, next := Evaluate([]Process{runawayProc(100, 95, runawayStart)}, next, s, later, os.ExpandEnv)
	if len(cands) != 0 {
		t.Fatalf("streak under sustain must not be a candidate: %#v", cands)
	}
	// Streak at 30 minutes — exactly the boundary counts (>= sustain).
	end := start.Add(30 * time.Minute)
	cands, _ = Evaluate([]Process{runawayProc(100, 95, runawayStart)}, next, s, end, os.ExpandEnv)
	if len(cands) != 1 || cands[0].Process.PID != 100 {
		t.Fatalf("sustained process must be a candidate at the boundary: %#v", cands)
	}
}

func TestEvaluate_ThresholdBoundary(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	at := runawayProc(100, 50.0, runawayStart)
	below := runawayProc(101, 49.9, runawayStart)
	cands, next := Evaluate([]Process{at, below}, Samples{}, evalSettings(), now, os.ExpandEnv)
	if len(cands) != 0 {
		t.Fatalf("no history yet, want no candidates: %#v", cands)
	}
	if len(next) != 1 {
		t.Fatalf("only the at-threshold process may be tracked, got %d entries", len(next))
	}
}

func TestEvaluate_BelowThresholdResetsTheStreak(t *testing.T) {
	s := evalSettings()
	start := time.Unix(1_800_000_000, 0)
	_, next := Evaluate([]Process{runawayProc(100, 95, runawayStart)}, Samples{}, s, start, os.ExpandEnv)
	dip := start.Add(5 * time.Minute)
	calm := runawayProc(100, 3.0, runawayStart)
	_, next = Evaluate([]Process{calm}, next, s, dip, os.ExpandEnv)
	if len(next) != 0 {
		t.Fatalf("a below-threshold sample must clear the streak, got %#v", next)
	}
	// Back over threshold: the streak restarts from zero.
	back := dip.Add(5 * time.Minute)
	cands, next := Evaluate([]Process{runawayProc(100, 95, runawayStart)}, next, s, back, os.ExpandEnv)
	if len(cands) != 0 {
		t.Fatalf("restarted streak must not be a candidate immediately: %#v", cands)
	}
	key := runawayProc(100, 95, runawayStart).StartKey()
	if next[key].OverSince != back {
		t.Fatalf("OverSince = %v, want streak restarted at %v", next[key].OverSince, back)
	}
}

func TestEvaluate_PIDReuseKeepsSeparateHistories(t *testing.T) {
	s := evalSettings()
	now := time.Unix(1_800_000_000, 0)
	// Old incarnation sampled earlier with an established streak.
	old := runawayProc(100, 95, runawayStart)
	prev := Samples{old.StartKey(): {OverSince: now.Add(-31 * time.Minute), LastSeen: now.Add(-time.Minute)}}
	// The PID was reused: same pid, a new incarnation. It must not inherit
	// the dead one's streak.
	fresh := runawayProc(100, 95, "Sun Sep 27 08:00:00 2026")
	cands, next := Evaluate([]Process{fresh}, prev, s, now, os.ExpandEnv)
	if len(cands) != 0 {
		t.Fatalf("PID reuse must not inherit the old streak: %#v", cands)
	}
	if next[fresh.StartKey()].OverSince != now {
		t.Fatal("fresh incarnation must start its own streak")
	}
	// And the old incarnation, absent from this run, is pruned.
	if _, ok := next[old.StartKey()]; ok {
		t.Fatal("stale incarnation must be pruned")
	}
}

func TestEvaluate_StaleProcessesArePruned(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	prev := Samples{"999@100": {OverSince: now.Add(-time.Hour)}}
	_, next := Evaluate(nil, prev, evalSettings(), now, os.ExpandEnv)
	if len(next) != 0 {
		t.Fatalf("history for gone processes must be pruned: %#v", next)
	}
}

// fakeKiller records signal delivery for Enforce tests.
type fakeKiller struct {
	alive   bool
	dieOn   string // "term" makes Term flip alive false; anything else needs Kill
	signals []string
	termErr error
	killErr error
}

func (f *fakeKiller) Alive(int) bool { return f.alive }
func (f *fakeKiller) Term(int) error {
	if f.termErr != nil {
		return f.termErr
	}
	f.signals = append(f.signals, "term")
	if f.dieOn == "term" {
		f.alive = false
	}
	return nil
}
func (f *fakeKiller) Kill(int) error {
	if f.killErr != nil {
		return f.killErr
	}
	f.signals = append(f.signals, "kill")
	f.alive = false
	return nil
}

func noSleep(time.Duration) {}

func sameProcess() bool { return true }

func TestEnforce_TermIgnoredEscalatesToKill(t *testing.T) {
	k := &fakeKiller{alive: true, dieOn: "never"} // the incident shape: SIGTERM ignored
	action, err := Enforce(k, 100, 10*time.Second, noSleep, sameProcess)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if action != KillSIGKILL {
		t.Fatalf("action = %q, want %q", action, KillSIGKILL)
	}
	if len(k.signals) != 2 || k.signals[0] != "term" || k.signals[1] != "kill" {
		t.Fatalf("signals = %v, want [term kill]", k.signals)
	}
}

func TestEnforce_TermSuffices(t *testing.T) {
	k := &fakeKiller{alive: true, dieOn: "term"}
	action, err := Enforce(k, 100, 10*time.Second, noSleep, sameProcess)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if action != KillSIGTERM {
		t.Fatalf("action = %q, want %q", action, KillSIGTERM)
	}
	if len(k.signals) != 1 {
		t.Fatalf("SIGKILL must not follow a successful SIGTERM: %v", k.signals)
	}
}

func TestEnforce_AlreadyGone(t *testing.T) {
	k := &fakeKiller{alive: false}
	action, err := Enforce(k, 100, 10*time.Second, noSleep, sameProcess)
	if err != nil || action != KillAlreadyGone {
		t.Fatalf("action = %q err = %v, want %q", action, err, KillAlreadyGone)
	}
	if len(k.signals) != 0 {
		t.Fatalf("no signal may be sent to a dead pid: %v", k.signals)
	}
}

func TestEnforce_TermErrorPropagates(t *testing.T) {
	k := &fakeKiller{alive: true, termErr: errors.New("operation not permitted")}
	if _, err := Enforce(k, 100, 10*time.Second, noSleep, sameProcess); err == nil {
		t.Fatal("a term failure must surface")
	}
}

// ESRCH is not os.IsNotExist, but it means the same thing here: the candidate
// died between the liveness probe and the signal.
func TestEnforce_ESRCHIsAlreadyGone(t *testing.T) {
	k := &fakeKiller{alive: true, termErr: syscall.ESRCH}
	action, err := Enforce(k, 100, 10*time.Second, noSleep, sameProcess)
	if err != nil || action != KillAlreadyGone {
		t.Fatalf("term ESRCH = %q, %v; want %q", action, err, KillAlreadyGone)
	}
	k2 := &fakeKiller{alive: true, dieOn: "never", killErr: syscall.ESRCH}
	action, err = Enforce(k2, 100, 10*time.Second, noSleep, sameProcess)
	if err != nil || action != KillAlreadyGone {
		t.Fatalf("kill ESRCH = %q, %v; want %q", action, err, KillAlreadyGone)
	}
}

// PID reuse during the grace window: the candidate died, a new process took
// its pid, and the SIGKILL belongs to the dead one.
func TestEnforce_PIDReuseDuringGraceSparesReplacement(t *testing.T) {
	k := &fakeKiller{alive: true, dieOn: "never"}
	action, err := Enforce(k, 100, 10*time.Second, noSleep, func() bool { return false })
	if err != nil || action != KillAlreadyGone {
		t.Fatalf("action = %q err = %v, want %q", action, err, KillAlreadyGone)
	}
	if len(k.signals) != 1 || k.signals[0] != "term" {
		t.Fatalf("SIGKILL must not reach a recycled pid: %v", k.signals)
	}
}

func TestSystemKiller_CompilesAndProbes(t *testing.T) {
	k := SystemKiller{}
	if !k.Alive(syscall.Getpid()) {
		t.Fatal("own pid must probe alive")
	}
	// MaxPID+ is never a live process.
	if k.Alive(1 << 30) {
		t.Fatal("impossible pid must probe dead")
	}
}

func TestSamples_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "samples.json")
	now := time.Unix(1_800_000_000, 0).UTC()
	in := Samples{"100@42": {OverSince: now, LastSeen: now, CPU: 95.5, Args: "/tmp/x/bun"}}
	if err := SaveSamples(path, in); err != nil {
		t.Fatalf("SaveSamples: %v", err)
	}
	out, err := LoadSamples(path)
	if err != nil {
		t.Fatalf("LoadSamples: %v", err)
	}
	got, ok := out["100@42"]
	if !ok || got.CPU != 95.5 || !got.OverSince.Equal(now) || got.Args != "/tmp/x/bun" {
		t.Fatalf("round trip mismatch: %#v", out)
	}
}

func TestSamples_MissingFileIsEmpty(t *testing.T) {
	s, err := LoadSamples(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil || len(s) != 0 {
		t.Fatalf("missing file = %#v, %v; want empty, nil", s, err)
	}
}

func TestSamples_CorruptFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSamples(path); err == nil {
		t.Fatal("corrupt samples must error")
	}
}
