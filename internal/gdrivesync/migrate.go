package gdrivesync

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// MigrateOptions controls one-shot migration behavior.
type MigrateOptions struct {
	DryRun bool
}

// migrationLink describes one symlink that the migration converts.
// Action is either "remove" (drop the symlink, no replacement) or
// "convert" (drop the symlink and mkdir an empty real directory).
type migrationLink struct {
	Rel    string
	Action string
}

// migrationLinks lists the workspace-relative symlinks that the legacy
// dual-path policy created and that the new policy reverses.
//
//	.gdrive          → just remove (was a bookmark to gdrive-workspace)
//	inbox/downloads  → real dir (will receive content from mirror)
//	inbox/incoming   → real dir (will receive content from mirror)
var migrationLinks = []migrationLink{
	{Rel: ".gdrive", Action: "remove"},
	{Rel: "inbox/downloads", Action: "convert"},
	{Rel: "inbox/incoming", Action: "convert"},
}

// SymlinkState captures the current on-disk state of a migration target.
// IsSymlink + IsDir are mutually exclusive; Missing means absent entirely.
type SymlinkState struct {
	Rel       string
	Action    string
	Path      string
	IsSymlink bool
	IsDir     bool
	Missing   bool
}

// PreflightInfo summarizes the migration preconditions for the user.
type PreflightInfo struct {
	LocalPath       string
	MirrorPath      string
	LocalExists     bool
	MirrorExists    bool
	LocalSize       int64
	MirrorSize      int64
	FreeOnLocalPart int64
	EstimatedNeed   int64
	HasUncommitted  bool
	Symlinks        []SymlinkState
}

// Preflight inspects the workspace + mirror trees and reports what the
// migration would do. Returns an error only for hard failures (paths
// missing, stat failures); cosmetic concerns (dirty git tree, low disk)
// are surfaced via fields and left to the caller to act on.
func Preflight(cfg *Config) (*PreflightInfo, error) {
	info := &PreflightInfo{
		LocalPath:  strings.TrimRight(cfg.LocalPath, "/"),
		MirrorPath: strings.TrimRight(cfg.MirrorPath, "/"),
	}
	info.LocalExists = pathExists(info.LocalPath)
	info.MirrorExists = pathExists(info.MirrorPath)
	if !info.MirrorExists {
		return info, fmt.Errorf("mirror path does not exist: %s", info.MirrorPath)
	}
	if !info.LocalExists {
		return info, fmt.Errorf("local path does not exist: %s", info.LocalPath)
	}

	var err error
	info.LocalSize, err = walkSize(info.LocalPath)
	if err != nil {
		return info, fmt.Errorf("computing local size: %w", err)
	}
	info.MirrorSize, err = walkSize(info.MirrorPath)
	if err != nil {
		return info, fmt.Errorf("computing mirror size: %w", err)
	}

	// Estimated need: difference × 1.2 safety margin. Negative diffs
	// (workspace already larger) clamp to zero — pull won't grow disk.
	delta := info.MirrorSize - info.LocalSize
	if delta < 0 {
		delta = 0
	}
	info.EstimatedNeed = delta * 12 / 10

	info.FreeOnLocalPart, err = freeBytes(info.LocalPath)
	if err != nil {
		return info, fmt.Errorf("checking free space: %w", err)
	}

	info.HasUncommitted = hasUncommittedGit(info.LocalPath)
	info.Symlinks = symlinkStates(info.LocalPath)

	return info, nil
}

// walkSize sums the size of all regular files under root. Skips
// .sync-conflicts/ (we don't want to count our own backups against the
// estimate) and silently ignores per-file errors so a single permission
// hiccup doesn't abort the whole walk.
func walkSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() && d.Name() == conflictsDirName {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// freeBytes returns available bytes on the filesystem holding path.
func freeBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// hasUncommittedGit reports whether `git -C path status --porcelain` finds
// any pending changes. Returns false for non-git trees (no git repo, git
// missing, etc.) — migration's git check is informational, not blocking.
func hasUncommittedGit(path string) bool {
	if _, err := osexec.LookPath("git"); err != nil {
		return false
	}
	cmd := osexec.Command("git", "-C", path, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// HasPendingMigration reports whether any legacy migration symlink still
// lives under localBase. Pull/push/sync refuse to run while any of these
// remain because the symlinks would route writes back into the mirror
// tree, defeating the workspace-authoritative model.
func HasPendingMigration(localBase string) bool {
	for _, st := range symlinkStates(localBase) {
		if st.IsSymlink {
			return true
		}
	}
	return false
}

// symlinkStates inspects each migration target under localBase and
// reports its current shape (symlink / real dir / missing).
func symlinkStates(localBase string) []SymlinkState {
	out := make([]SymlinkState, 0, len(migrationLinks))
	for _, link := range migrationLinks {
		full := filepath.Join(localBase, link.Rel)
		st := SymlinkState{Rel: link.Rel, Action: link.Action, Path: full}
		fi, err := os.Lstat(full)
		switch {
		case err != nil:
			st.Missing = true
		case fi.Mode()&os.ModeSymlink != 0:
			st.IsSymlink = true
		case fi.IsDir():
			st.IsDir = true
		}
		out = append(out, st)
	}
	return out
}

// ConvertSymlinks performs the symlink → real-dir conversion described by
// migrationLinks under localBase. Idempotent: already-converted entries
// (real dirs) and missing entries are no-ops. Refuses to overwrite a
// regular file or unrecognized non-symlink at any of the target paths.
func ConvertSymlinks(runner *exec.Runner, localBase string) error {
	for _, st := range symlinkStates(localBase) {
		switch {
		case st.Missing:
			fmt.Printf("  • %s: missing — skip\n", st.Rel)
		case st.IsDir && !st.IsSymlink:
			fmt.Printf("  • %s: already a real dir — skip\n", st.Rel)
		case !st.IsSymlink:
			return fmt.Errorf("%s exists but is neither symlink nor dir — refusing to overwrite", st.Path)
		default:
			fmt.Printf("  • %s: removing symlink\n", st.Rel)
			if err := runner.Remove(st.Path); err != nil {
				return fmt.Errorf("removing %s: %w", st.Path, err)
			}
			if st.Action == "convert" {
				fmt.Printf("  • %s: creating real directory\n", st.Rel)
				if err := runner.MkdirAll(st.Path, 0755); err != nil {
					return fmt.Errorf("creating %s: %w", st.Path, err)
				}
			}
		}
	}
	return nil
}

// Migrate runs the one-shot migration: preflight inspection → symlink
// conversion → additive pull from mirror → local config save with Paused=true.
//
// The Paused gate is on purpose: the migration brings everything down
// from gdrive but does NOT auto-enable push-first sync. The user
// inspects the result (du, sha256, git status) and runs `dot
// gdrive-sync resume` to clear the gate when satisfied.
//
// Idempotent. Re-running migrate after a partial failure is safe.
func Migrate(ctx context.Context, runner *exec.Runner, cfg *Config, state *config.UserState, opts MigrateOptions) error {
	info, err := Preflight(cfg)
	if err != nil {
		return err
	}
	PrintPreflight(info)

	// Defensive: ensure paused stays true through the migration window.
	if !opts.DryRun {
		if err := setLocalPausedForMigration(cfg, state, true); err != nil {
			return err
		}
	}

	if opts.DryRun {
		fmt.Println()
		fmt.Println("(dry-run) symlink plan:")
		for _, st := range info.Symlinks {
			fmt.Printf("  • %s\n", describeSymlinkPlan(st))
		}
	} else {
		fmt.Println()
		fmt.Println("Converting symlinks:")
		if err := ConvertSymlinks(runner, info.LocalPath); err != nil {
			return fmt.Errorf("converting symlinks: %w", err)
		}
	}

	fmt.Println()
	fmt.Println("Pulling mirror content into workspace (additive, no --delete):")
	if err := MigratePull(ctx, runner, cfg, opts.DryRun); err != nil {
		return fmt.Errorf("migrate pull: %w", err)
	}

	printNextSteps(info)
	return nil
}

func setLocalPausedForMigration(cfg *Config, state *config.UserState, paused bool) error {
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	localCfg, ok, err := LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return err
	}
	if !ok {
		localCfg = localConfigFromGlobal(state)
	}
	localCfg.Paused = paused
	if err := SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
		return fmt.Errorf("saving local config: %w", err)
	}
	cfg.Paused = paused
	return nil
}

func describeSymlinkPlan(st SymlinkState) string {
	switch {
	case st.Missing:
		return fmt.Sprintf("%s: missing — would skip", st.Rel)
	case st.IsDir && !st.IsSymlink:
		return fmt.Sprintf("%s: already a real dir — would skip", st.Rel)
	case st.IsSymlink && st.Action == "remove":
		return fmt.Sprintf("%s: would remove symlink", st.Rel)
	case st.IsSymlink && st.Action == "convert":
		return fmt.Sprintf("%s: would remove symlink + mkdir real directory", st.Rel)
	default:
		return fmt.Sprintf("%s: unexpected state at %s", st.Rel, st.Path)
	}
}
