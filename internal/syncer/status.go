package syncer

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Status is the snapshot returned by GetStatus for the `status` command.
type Status struct {
	Profile              string
	Owner                string
	LocalPath            string
	MirrorPath           string
	Target               Target // parsed destination (local dir or ssh remote)
	StoreDir             string // <local>/.dotfiles/sync/ — empty if unresolved
	LocalExists          bool
	MirrorExists         bool
	Paused               bool
	FilterMode           FilterMode
	IncludeFile          string
	ExcludeFile          string
	IgnoreFile           string
	AllowCount           int // active allow.txt patterns (secrets opt-in) — warn when > 0
	SensitiveOverrides   []SensitiveOverride
	SubmoduleCount       int // submodules excluded from sync (they sync via Git)
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
	allowCount := 0
	for _, p := range cfg.AllowPatterns {
		p = strings.TrimSpace(p)
		if p != "" && !strings.HasPrefix(p, "#") {
			allowCount++
		}
	}
	// Collect the complete, already-sorted policy finding before constructing the
	// status snapshot. Callers therefore receive either every typed override or
	// no snapshot at all; terminal and JSON renderers never have to assemble a
	// partial report while emitting output.
	sensitiveOverrides := SensitiveOverrides(cfg.AllowPatterns)
	s := &Status{
		Profile:            cfg.Profile,
		Owner:              cfg.Owner,
		LocalPath:          strings.TrimRight(cfg.LocalPath, "/"),
		MirrorPath:         strings.TrimRight(cfg.MirrorPath, "/"),
		Target:             cfg.Target,
		StoreDir:           storeDir,
		LocalExists:        runner.IsDir(cfg.LocalPath),
		MirrorExists:       cfg.Target.IsSSH() || runner.IsDir(cfg.MirrorPath),
		Paused:             cfg.Paused,
		FilterMode:         cfg.FilterMode,
		IncludeFile:        cfg.IncludeFile,
		ExcludeFile:        cfg.ExcludesFile,
		IgnoreFile:         cfg.IgnoreFile,
		AllowCount:         allowCount,
		SensitiveOverrides: sensitiveOverrides,
		SubmoduleCount:     len(gitSubmodulePaths(strings.TrimRight(cfg.LocalPath, "/"))),
		Propagation:        cfg.Propagation,
		LastPull:           localState.LastPull,
		LastPush:           localState.LastPush,
		LastIntake:         localState.LastIntake,
		LastIntakeTSDir:    localState.LastIntakeTSDir,
		LockHeld:           pathExists(cfg.LockDir) && !lockIsStale(cfg.LockDir),
		MaxDelete:          cfg.MaxDelete,
		Interval:           cfg.Interval,
		PullInterval:       cfg.PullInterval,
		PushMode:           cfg.PushMode,
		PullMode:           cfg.PullMode,
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

	// Conflicts accumulate in both trees (pull backups in the workspace,
	// push backups in the mirror) — keep the snapshot consistent with
	// `conflicts list`/`prune`.
	if confs, err := ListConflicts(s.LocalPath); err == nil {
		s.Conflicts = confs
	}
	if s.MirrorPath != "" && !cfg.Target.IsSSH() && filepath.Clean(s.MirrorPath) != filepath.Clean(s.LocalPath) {
		if confs, err := ListConflicts(s.MirrorPath); err == nil {
			s.Conflicts = append(s.Conflicts, confs...)
		}
	}

	// Populate manual shared entries. Errors are non-fatal; status is best-effort.
	if shared, err := ScanShared(s.MirrorPath, cfg.SharedExcludes); err == nil {
		s.Shared = shared
	}

	return s, nil
}
