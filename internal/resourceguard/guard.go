// Package resourceguard admits one heavyweight job per repository, across all
// worktrees and agent profiles, while enforcing shared host-pressure recovery.
// It is a thin adapter over internal/admission, the engine behind `dot admit`,
// so `dot ai run`, the tooling updates and `dot admit` contend for the same
// slots and read the same pressure history (#162).
package resourceguard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/admission"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Options selects the slot. HomeDir is accepted for callers' symmetry but
// never partitions a slot: slots belong to the real user and repository.
type Options struct {
	HomeDir    string
	Purpose    string
	ProjectDir string
	ScopeKey   string // "tooling" selects the host-wide maintenance slot
}

// DeferredError reports that admission deferred the job; callers retry later.
type DeferredError struct{ Reason string }

func (e *DeferredError) Error() string { return "deferred-resource-pressure: " + e.Reason }

// sampleInterval paces WaitAcquire's retries.
const sampleInterval = 15 * time.Second

type adapter struct {
	root      func() (string, error)
	runner    *exec.Runner
	monitor   func() *admission.Monitor
	uncovered func(ctx context.Context, dir string, maintenance bool) ([]string, error)
	lease     func(ctx context.Context, runner *exec.Runner, dir string) (admission.Lease, error)
	resolve   func(ctx context.Context, runner *exec.Runner, dir string) (string, error)
	heartbeat time.Duration
}

func native() *adapter {
	runner := exec.NewProbeRunner()
	return &adapter{
		root:   admission.UserStateRoot,
		runner: runner,
		monitor: func() *admission.Monitor {
			home, _ := os.UserHomeDir()
			return &admission.Monitor{Runner: runner, Home: home}
		},
		uncovered: admission.FindUncovered,
		lease:     admission.SelfLease,
		resolve:   admission.ResolveScope,
		heartbeat: admission.DefaultHeartbeatInterval,
	}
}

// Acquire fails promptly on contention, pressure, unknown telemetry, or
// uncovered heavy work.
func Acquire(ctx context.Context, opts Options) (func(), error) {
	return native().acquire(ctx, opts)
}

// WaitAcquire retries deferrals with bounded, cancellable waits.
func WaitAcquire(ctx context.Context, opts Options, maxWait time.Duration) (func(), error) {
	return native().waitAcquire(ctx, opts, maxWait)
}

func (a *adapter) waitAcquire(ctx context.Context, opts Options, maxWait time.Duration) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	for {
		release, err := a.acquire(ctx, opts)
		if err == nil {
			return release, nil
		}
		var deferred *DeferredError
		if !errors.As(err, &deferred) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (wait ended: %v)", err, ctx.Err())
		case <-time.After(sampleInterval):
		}
	}
}

func (a *adapter) acquire(ctx context.Context, opts Options) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir := opts.ProjectDir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dir = wd
	}
	scope, class := "", admission.ClassHeavy
	switch opts.ScopeKey {
	case "":
		s, err := a.resolve(ctx, a.runner, dir)
		if err != nil {
			return nil, &DeferredError{Reason: err.Error()}
		}
		scope = s
	case "tooling":
		scope, class = admission.MaintenanceScope, admission.ClassMaintenance
	default:
		return nil, fmt.Errorf("unknown shared resource scope %q", opts.ScopeKey)
	}
	// A child of a job that already holds this slot runs inside it.
	if admission.NestedScope(os.Getenv(admission.NestedEnv), scope, class) {
		return func() {}, nil
	}
	root, err := a.root()
	if err != nil {
		return nil, err
	}
	store := admission.NewStore(root, a.runner)
	d, err := store.Gate(ctx, a.monitor(), admission.DefaultThresholds())
	if err != nil {
		return nil, err
	}
	if !d.Admit {
		return nil, &DeferredError{Reason: strings.Join(d.Reasons, "; ")}
	}
	jobs, err := a.uncovered(ctx, dir, class == admission.ClassMaintenance)
	if err != nil {
		return nil, &DeferredError{Reason: "heavy-work inventory unavailable: " + err.Error()}
	}
	if len(jobs) > 0 {
		return nil, &DeferredError{Reason: "uncovered heavyweight work: " + strings.Join(jobs, ", ")}
	}
	lease, err := a.lease(ctx, a.runner, dir)
	if err != nil {
		return nil, err
	}
	if lease.Session == "" {
		lease.Session = opts.Purpose // names the job in `dot admit status`
	}
	slot, holder, err := store.Acquire(ctx, scope, class, lease)
	if err != nil {
		return nil, err
	}
	if slot == nil {
		reason := "another heavyweight job owns scope " + scope
		if holder != nil {
			reason += fmt.Sprintf(" (%s, pid %d)", holder.Owner, holder.PID)
		}
		return nil, &DeferredError{Reason: reason}
	}

	// Children inherit the nested marker, so a `dot admit` or `dot ai run`
	// inside this job runs in the slot instead of deadlocking on it.
	// ponytail: process-wide env; fine while one guarded job runs per process.
	prev, hadPrev := os.LookupEnv(admission.NestedEnv)
	_ = os.Setenv(admission.NestedEnv, admission.NestedEnvValue(scope, class))
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(a.heartbeat)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = slot.Heartbeat()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
			if hadPrev {
				_ = os.Setenv(admission.NestedEnv, prev)
			} else {
				_ = os.Unsetenv(admission.NestedEnv)
			}
			_ = slot.Release()
		})
	}, nil
}
