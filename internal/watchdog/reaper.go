package watchdog

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// Candidate is a matched process whose CPU has stayed at or above the
// threshold for at least the sustain window.
type Candidate struct {
	Process   Process
	Key       string
	OverSince time.Time
	Reason    string
}

// Evaluate folds one ps sample run into the persisted history and returns
// the processes that are now candidates, plus the next samples set to
// persist. Pure: clock, environment expansion, and history all come in as
// arguments, so the whole decision table is testable without a live process
// table.
//
// History bookkeeping: only matched processes are tracked. An over-threshold
// sample starts (or continues) an OverSince streak; a below-threshold sample
// clears it, because "sustained" means continuously over. Processes absent
// from this run drop out with the map rebuild, so PID-reuse stale entries
// and exited processes never accumulate.
func Evaluate(procs []Process, prev Samples, s ReaperSettings, now time.Time, expandEnv func(string) string) ([]Candidate, Samples) {
	next := Samples{}
	var candidates []Candidate
	for _, p := range procs {
		match, reason := MatchCandidate(p, s, expandEnv)
		if !match {
			continue
		}
		key := p.StartKey(now)
		if p.CPU < s.CPUThreshold {
			continue // streak broken; dropping the key resets it
		}
		sample := Sample{LastSeen: now, CPU: p.CPU, Args: p.Args}
		if old, ok := prev[key]; ok && !old.OverSince.IsZero() {
			sample.OverSince = old.OverSince
		} else {
			sample.OverSince = now
		}
		next[key] = sample
		if now.Sub(sample.OverSince) >= s.Sustain {
			candidates = append(candidates, Candidate{
				Process:   p,
				Key:       key,
				OverSince: sample.OverSince,
				Reason:    reason,
			})
		}
	}
	return candidates, next
}

// Kill outcomes.
const (
	KillSIGTERM     = "sigterm"
	KillSIGKILL     = "sigkill"
	KillAlreadyGone = "already-gone"
)

// Killer abstracts signal delivery so enforce-mode tests never touch a real
// process table.
type Killer interface {
	// Alive reports whether pid exists (zombies count; the reaper targets
	// orphans, which launchd reaps promptly).
	Alive(pid int) bool
	Term(pid int) error
	Kill(pid int) error
}

// SystemKiller delivers real signals. unix-only by construction; the repo
// targets darwin and linux and has no windows build.
type SystemKiller struct{}

// Alive probes with signal 0: a nil or EPERM result both mean the pid exists.
func (SystemKiller) Alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// Term sends SIGTERM.
func (SystemKiller) Term(pid int) error {
	return signalErr(syscall.Kill(pid, syscall.SIGTERM), pid, "SIGTERM")
}

// Kill sends SIGKILL.
func (SystemKiller) Kill(pid int) error {
	return signalErr(syscall.Kill(pid, syscall.SIGKILL), pid, "SIGKILL")
}

func signalErr(err error, pid int, sig string) error {
	if err != nil {
		return fmt.Errorf("sending %s to pid %d: %w", sig, pid, err)
	}
	return nil
}

// Enforce kills one candidate: SIGTERM, a grace wait, then SIGKILL if it
// ignored the term (the incident runaways did). sleep is injectable so tests
// do not wait out the grace period.
func Enforce(k Killer, pid int, grace time.Duration, sleep func(time.Duration)) (string, error) {
	if !k.Alive(pid) {
		return KillAlreadyGone, nil
	}
	if err := k.Term(pid); err != nil {
		if os.IsNotExist(err) {
			return KillAlreadyGone, nil
		}
		return "", err
	}
	sleep(grace)
	if !k.Alive(pid) {
		return KillSIGTERM, nil
	}
	if err := k.Kill(pid); err != nil {
		return "", err
	}
	return KillSIGKILL, nil
}
