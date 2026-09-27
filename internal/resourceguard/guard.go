// Package resourceguard admits one heavyweight job per repository, across all
// worktrees and agent profiles, while enforcing shared host-pressure recovery.
package resourceguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Options struct {
	HomeDir    string
	Purpose    string
	ProjectDir string
	ScopeKey   string
}
type DeferredError struct{ Reason string }

func (e *DeferredError) Error() string { return "deferred-resource-pressure: " + e.Reason }

type Sample struct {
	At             time.Time     `json:"at"`
	MemoryNormal   bool          `json:"memory_normal"`
	ThermalKnown   bool          `json:"thermal_known"`
	ThermalHeavy   bool          `json:"thermal_heavy"`
	CPUIdle        float64       `json:"cpu_idle"`
	Load1          float64       `json:"load1"`
	CPUs           int           `json:"cpus"`
	Uptime         time.Duration `json:"uptime"`
	RecentWatchdog bool          `json:"recent_watchdog"`
	Uncovered      []string      `json:"uncovered,omitempty"`
	Jobs           []HeavyJob    `json:"jobs,omitempty"`
}
type History struct {
	Last         time.Time `json:"last"`
	HealthySince time.Time `json:"healthy_since"`
	BusySince    time.Time `json:"busy_since"`
}
type Owner struct {
	PID           int       `json:"pid"`
	StartIdentity string    `json:"start_identity"`
	Purpose       string    `json:"purpose"`
	Scope         Scope     `json:"scope"`
	Heartbeat     time.Time `json:"heartbeat"`
	Expires       time.Time `json:"expires"`
}

const sampleInterval = 15 * time.Second
const recoveryWindow = 5 * time.Minute
const leaseWindow = 90 * time.Second

type guard struct {
	dir      string
	now      func() time.Time
	probe    func(context.Context) (Sample, error)
	identity func(context.Context, int) (string, error)
}

func nativeGuard() *guard {
	return &guard{dir: filepath.Join("/tmp", fmt.Sprintf("dotfiles-resource-%d", os.Getuid())), now: time.Now, probe: Probe, identity: processIdentity}
}

// Acquire fails promptly on contention, unknown telemetry, or insufficient
// recovery evidence. HomeDir does not partition repository or maintenance locks.
func Acquire(ctx context.Context, opts Options) (func(), error) {
	return nativeGuard().acquire(ctx, opts)
}

// Observe records a bounded lightweight sample; it never launches heavy work.
func Observe(ctx context.Context, _ Options) (Sample, error) {
	g := nativeGuard()
	s, _, err := g.observe(ctx)
	return s, err
}

// WaitAcquire collects recovery evidence with bounded, cancellable waits.
func WaitAcquire(ctx context.Context, opts Options, maxWait time.Duration) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	var last error
	for {
		release, err := Acquire(ctx, opts)
		if err == nil {
			return release, nil
		}
		var deferred *DeferredError
		if !errors.As(err, &deferred) {
			return nil, err
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (recovery wait ended: %v)", last, ctx.Err())
		case <-time.After(sampleInterval):
		}
	}
}
func secureDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("unsafe resource state directory %s", dir)
	}
	var stat unix.Stat_t
	if err := unix.Lstat(dir, &stat); err != nil {
		return err
	}
	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("resource state directory has another owner")
	}
	return nil
}
func lockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
func unlock(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".resource-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (g *guard) observe(ctx context.Context) (Sample, History, error) {
	if err := secureDir(g.dir); err != nil {
		return Sample{}, History{}, err
	}
	// Probe before taking the short state lock: concurrent observers do not hold
	// this lock across top/osascript or contend with a running job's heartbeat.
	s, err := g.probe(ctx)
	if err != nil {
		// Failed observation breaks continuity even if the next sample arrives
		// promptly. Missing telemetry must not bridge a healthy interval.
		if f, lockErr := lockFile(filepath.Join(g.dir, "history.lock")); lockErr == nil {
			_ = writeJSON(filepath.Join(g.dir, "health.json"), History{Last: g.now()})
			unlock(f)
		}
		return s, History{}, &DeferredError{Reason: "telemetry unavailable: " + err.Error()}
	}
	s.At = g.now()
	f, err := lockFile(filepath.Join(g.dir, "history.lock"))
	if err != nil {
		return s, History{}, &DeferredError{Reason: "another health sample is being recorded"}
	}
	defer unlock(f)
	var h History
	if data, err := os.ReadFile(filepath.Join(g.dir, "health.json")); err == nil {
		if err = json.Unmarshal(data, &h); err != nil {
			return s, h, fmt.Errorf("invalid resource history: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return s, h, err
	}
	if h.Last.IsZero() || s.At.Sub(h.Last) > 2*sampleInterval+5*time.Second || s.At.Before(h.Last) {
		h.HealthySince = time.Time{}
		h.BusySince = time.Time{}
	}
	healthy := s.MemoryNormal && s.ThermalKnown && !s.ThermalHeavy && s.CPUIdle >= 30 && s.CPUs > 0 && s.Load1 < 0.7*float64(s.CPUs) && !s.RecentWatchdog && s.Uptime >= recoveryWindow
	if healthy {
		if h.HealthySince.IsZero() {
			h.HealthySince = s.At
		}
	} else {
		h.HealthySince = time.Time{}
	}
	busy := s.CPUIdle < 15 || s.CPUs <= 0 || s.Load1 >= float64(s.CPUs)
	if busy {
		if h.BusySince.IsZero() {
			h.BusySince = s.At
		}
	} else {
		h.BusySince = time.Time{}
	}
	h.Last = s.At
	return s, h, writeJSON(filepath.Join(g.dir, "health.json"), h)
}
func admission(s Sample, h History) error {
	reason := ""
	switch {
	case !s.MemoryNormal:
		reason = "memory warning, critical, or unknown"
	case !s.ThermalKnown:
		reason = "thermal state unknown"
	case s.ThermalHeavy:
		reason = "heavy thermal pressure"
	case s.RecentWatchdog:
		reason = "recent WindowServer watchdog event"
	case s.Uptime < recoveryWindow:
		reason = "post-boot startup grace"
	case len(s.Uncovered) > 0:
		reason = "uncovered heavyweight work: " + fmt.Sprint(s.Uncovered)
	case !h.BusySince.IsZero() && s.At.Sub(h.BusySince) >= time.Minute:
		reason = "CPU/load pressure sustained for 60 seconds"
	case h.HealthySince.IsZero() || s.At.Sub(h.HealthySince) < recoveryWindow:
		reason = "five minutes of continuously sampled healthy recovery required; use dot ai run --wait 6m -- COMMAND"
	}
	if reason != "" {
		return &DeferredError{Reason: reason}
	}
	return nil
}
func (g *guard) acquire(ctx context.Context, opts Options) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := secureDir(g.dir); err != nil {
		return nil, err
	}
	scope, err := ResolveScope(opts)
	if err != nil {
		return nil, &DeferredError{Reason: err.Error()}
	}
	scopeDir := scopeDirectory(g.dir, scope)
	if err := secureDir(scopeDir); err != nil {
		return nil, err
	}
	f, err := lockFile(filepath.Join(scopeDir, "heavy.lock"))
	if err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, &DeferredError{Reason: "another heavyweight job owns scope " + scope.Key}
		}
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			unlock(f)
		}
	}()
	s, h, err := g.observe(ctx)
	if err != nil {
		return nil, err
	}
	s = filterUncovered(s, scope)
	if err = admission(s, h); err != nil {
		return nil, err
	}
	identity, err := g.identity(ctx, os.Getpid())
	if err != nil {
		return nil, err
	}
	owner := Owner{PID: os.Getpid(), StartIdentity: identity, Purpose: opts.Purpose, Scope: scope}
	save := func() error {
		owner.Heartbeat = g.now()
		owner.Expires = owner.Heartbeat.Add(leaseWindow)
		return writeJSON(filepath.Join(scopeDir, "owner.json"), owner)
	}
	if err := save(); err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = save()
			}
		}
	}()
	var once sync.Once
	release := func() {
		once.Do(func() { close(stop); <-done; _ = os.Remove(filepath.Join(scopeDir, "owner.json")); unlock(f) })
	}
	// The kernel flock, rather than expiry alone, prevents stealing a live job.
	// On process death it releases automatically; stale owner metadata is replaced
	// on the next successful admission, without signalling the former PID.
	ok = true
	return release, nil
}
