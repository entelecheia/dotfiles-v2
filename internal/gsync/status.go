package gsync

import (
	"context"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Status is the snapshot returned by GetStatus for the `status` command.
type Status struct {
	LocalPath            string
	MirrorPath           string
	StoreDir             string // <local>/.dotfiles/gdrive-sync/ — empty if unresolved
	LocalExists          bool
	MirrorExists         bool
	Paused               bool
	FilterMode           FilterMode
	IncludeFile          string
	ExcludeFile          string
	IgnoreFile           string
	Propagation          PropagationPolicy
	LastPull             time.Time
	LastPush             time.Time
	LastIntake           time.Time
	LastIntakeTSDir      string
	RsyncVersion         string // empty if not installed
	LockHeld             bool   // someone has gsync.lock right now
	MaxDelete            int
	Interval             int
	PullInterval         int            // 0 → no pull scheduler
	PushMode             RunMode        // automatic push mode
	PullMode             RunMode        // automatic intake mode
	SchedulerState       SchedulerState // push unit
	IntakeSchedulerState SchedulerState // pull unit (if installed)
	Conflicts            []ConflictEntry
	Shared               []SharedEntry
}

// GetStatus collects current sync state from cfg + state + filesystem.
// Always non-mutating; safe to run while a sync is in progress.
//
// sched may be nil — callers that don't have a Scheduler instance get a
// SchedulerNotInstalled value back rather than a panic.
func GetStatus(ctx context.Context, runner *exec.Runner, cfg *Config, state *config.UserState, sched *Scheduler) (*Status, error) {
	_ = state // legacy global state is no longer authoritative for gsync status.
	storeDir := ""
	var localState LocalState
	if cfg.LocalPaths != nil {
		storeDir = cfg.LocalPaths.StoreDir
		if st, err := LoadLocalState(cfg.LocalPaths); err == nil && st != nil {
			localState = *st
		}
	}
	s := &Status{
		LocalPath:       strings.TrimRight(cfg.LocalPath, "/"),
		MirrorPath:      strings.TrimRight(cfg.MirrorPath, "/"),
		StoreDir:        storeDir,
		LocalExists:     runner.IsDir(cfg.LocalPath),
		MirrorExists:    runner.IsDir(cfg.MirrorPath),
		Paused:          cfg.Paused,
		FilterMode:      cfg.FilterMode,
		IncludeFile:     cfg.IncludeFile,
		ExcludeFile:     cfg.ExcludesFile,
		IgnoreFile:      cfg.IgnoreFile,
		Propagation:     cfg.Propagation,
		LastPull:        localState.LastPull,
		LastPush:        localState.LastPush,
		LastIntake:      localState.LastIntake,
		LastIntakeTSDir: localState.LastIntakeTSDir,
		LockHeld:        pathExists(cfg.LockDir) && !lockIsStale(cfg.LockDir),
		MaxDelete:       cfg.MaxDelete,
		Interval:        cfg.Interval,
		PullInterval:    cfg.PullInterval,
		PushMode:        cfg.PushMode,
		PullMode:        cfg.PullMode,
	}
	if sched != nil {
		s.SchedulerState = sched.StateKind(ctx, SchedulerKindPush)
		s.IntakeSchedulerState = sched.StateKind(ctx, SchedulerKindIntake)
	}

	if runner.CommandExists("rsync") {
		if result, err := runner.RunQuery(ctx, "rsync", "--version"); err == nil {
			if i := strings.IndexByte(result.Stdout, '\n'); i > 0 {
				s.RsyncVersion = strings.TrimSpace(result.Stdout[:i])
			} else {
				s.RsyncVersion = strings.TrimSpace(result.Stdout)
			}
		}
	}

	if confs, err := ListConflicts(s.LocalPath); err == nil {
		s.Conflicts = confs
	}

	// Populate shared entries from a property-detected scan plus the
	// operator's manual list. Errors are non-fatal — status is best-
	// effort, and a permission hiccup mid-tree shouldn't black out the
	// whole snapshot.
	if shared, err := ScanShared(s.MirrorPath, cfg.SharedExcludes); err == nil {
		s.Shared = shared
	}

	return s, nil
}
