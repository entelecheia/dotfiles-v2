package gdrivesync

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Status is the snapshot returned by GetStatus for the `status` command.
type Status struct {
	LocalPath      string
	MirrorPath     string
	LocalExists    bool
	MirrorExists   bool
	Paused         bool
	LastPull       time.Time
	LastPush       time.Time
	LastSync       time.Time
	RsyncVersion   string // empty if not installed
	LockHeld       bool   // someone has gdrive-sync.lock right now
	MaxDelete      int
	Interval       int
	SchedulerState SchedulerState
	Conflicts      []ConflictEntry // local-tree backups (oldest first)
	Shared         []SharedEntry   // detected shortcuts + manual list
}

// GetStatus collects current sync state from cfg + state + filesystem.
// Always non-mutating; safe to run while a sync is in progress.
//
// sched may be nil — callers that don't have a Scheduler instance get a
// SchedulerNotInstalled value back rather than a panic.
func GetStatus(ctx context.Context, runner *exec.Runner, cfg *Config, state *config.UserState, sched *Scheduler) (*Status, error) {
	gs := state.Modules.GdriveSync
	s := &Status{
		LocalPath:    strings.TrimRight(cfg.LocalPath, "/"),
		MirrorPath:   strings.TrimRight(cfg.MirrorPath, "/"),
		LocalExists:  runner.IsDir(cfg.LocalPath),
		MirrorExists: runner.IsDir(cfg.MirrorPath),
		Paused:       cfg.Paused,
		LastPull:     gs.LastPull,
		LastPush:     gs.LastPush,
		LastSync:     gs.LastSync,
		LockHeld:     pathExists(cfg.LockDir),
		MaxDelete:    cfg.MaxDelete,
		Interval:     cfg.Interval,
	}
	if sched != nil {
		s.SchedulerState = sched.State(ctx)
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

// ── pretty-printers (used by CLI status / migrate handlers) ─────────────

// PrintPreflight writes the migration preflight summary to stdout.
// Called by both the `migrate` command and tests via captured stdout.
func PrintPreflight(info *PreflightInfo) {
	fmt.Println("Migration preflight:")
	fmt.Printf("  Local:        %s (size: %s)\n", info.LocalPath, humanBytes(info.LocalSize))
	fmt.Printf("  Mirror:       %s (size: %s)\n", info.MirrorPath, humanBytes(info.MirrorSize))
	fmt.Printf("  Free on local: %s\n", humanBytes(info.FreeOnLocalPart))
	fmt.Printf("  Estimated need: %s (delta × 1.2)\n", humanBytes(info.EstimatedNeed))
	if info.FreeOnLocalPart < info.EstimatedNeed {
		fmt.Println("  ⚠ free space below estimated need — migration may fail")
	}
	if info.HasUncommitted {
		fmt.Println("  ⚠ workspace has uncommitted git changes (informational)")
	}
	fmt.Println("  Symlinks to convert:")
	for _, st := range info.Symlinks {
		switch {
		case st.Missing:
			fmt.Printf("    - %s: missing\n", st.Rel)
		case st.IsDir && !st.IsSymlink:
			fmt.Printf("    - %s: already a real dir\n", st.Rel)
		case st.IsSymlink:
			fmt.Printf("    - %s: symlink (will %s)\n", st.Rel, st.Action)
		}
	}
}

func printNextSteps(info *PreflightInfo) {
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Verify sizes and structure:")
	fmt.Printf("     du -sh %s %s\n", info.LocalPath, info.MirrorPath)
	fmt.Printf("     ls -la %s/inbox\n", info.LocalPath)
	fmt.Printf("     test ! -L %s/.gdrive\n", info.LocalPath)
	fmt.Println("  2. When happy, activate two-way sync:")
	fmt.Println("     dot gdrive-sync resume")
	fmt.Println("  3. Sanity check (should be a no-op):")
	fmt.Println("     dot gdrive-sync sync --dry-run")
}

// humanBytes formats a byte count as a short human-readable string
// (e.g. 1.2 GB). Mirrors the convention used by `du -h`.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
