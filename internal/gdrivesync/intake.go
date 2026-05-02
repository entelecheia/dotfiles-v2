package gdrivesync

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// IntakeOptions controls one Intake run.
type IntakeOptions struct {
	Strict bool // sha256 mode (default: size+mtime fast mode)
	DryRun bool // skip staging copy + manifest writes
}

// IntakeResult summarizes one Intake run for the CLI/status layer.
type IntakeResult struct {
	StagingDir     string // <local>/inbox/gdrive/<ts>/ — empty if no files staged
	TimestampDir   string // <ts> alone, e.g. 2026-05-02T10-00-00Z
	Pull           *PullResult
	Intaked        []string
	SkippedBase    []string
	SkippedImports []string
	SkippedTracked []string
	Tombstones     []Tombstone
	Strict         bool
}

// driveMetadataExt are sentinel files written by the Drive client to
// point at cloud-side resources (Docs/Sheets/etc.). They are not
// content; intake skips them so the staging dir doesn't fill with
// stubs.
var driveMetadataExt = map[string]bool{
	".gdoc":      true,
	".gsheet":    true,
	".gslides":   true,
	".gform":     true,
	".gdraw":     true,
	".gtable":    true,
	".gnote":     true,
	".gjam":      true,
	".gmaplink":  true,
	".gsite":     true,
	".gshortcut": true,
}

// Intake first applies Drive-side changes for baseline-tracked files via
// PullTracked, then compares mirror against baseline + imports manifests and
// stages only baseline-unknown GDrive-origin files into
// <local>/inbox/gdrive/<intake-ts>/. Mirror-side deletions for tracked files
// become tombstones in the pull phase; intake itself never deletes local files.
//
// Idempotency: once a file is in imports.manifest with a matching
// fingerprint, the next intake skips it — even if the operator moved
// it out of inbox/gdrive/ in the meantime. Use `inbox forget` to
// revoke that entry.
func Intake(ctx context.Context, runner *exec.Runner, cfg *Config, opts IntakeOptions) (*IntakeResult, error) {
	if cfg.LocalPaths == nil {
		return nil, fmt.Errorf("intake: local paths unresolved")
	}
	if err := refuseSharedDriveMirror(cfg); err != nil {
		return nil, err
	}

	paths := cfg.LocalPaths
	mirror := strings.TrimRight(cfg.MirrorPath, "/")
	local := strings.TrimRight(cfg.LocalPath, "/")
	tracked := gitTrackedRelPaths(local)

	pullRes, err := PullTracked(ctx, runner, cfg, PullOptions{DryRun: opts.DryRun})
	if err != nil {
		return nil, err
	}

	baseline, err := LoadBaselineManifest(paths.BaselineFile)
	if err != nil {
		return nil, fmt.Errorf("loading baseline: %w", err)
	}
	imports, err := LoadImportsManifest(paths.ImportsFile)
	if err != nil {
		return nil, fmt.Errorf("loading imports: %w", err)
	}

	mode := FingerprintFast
	if opts.Strict {
		mode = FingerprintStrict
	}
	filter, err := newSyncFilter(cfg, mirror)
	if err != nil {
		return nil, fmt.Errorf("loading filters: %w", err)
	}

	now := time.Now().UTC()
	intakeTS := newSubSecondTimestamp()

	importsToWrite := make(map[string]ImportEntry, len(imports))
	for k, v := range imports {
		importsToWrite[k] = v
	}

	var (
		intaked        []string
		skippedBase    []string
		skippedImports []string
		skippedTracked []string
		toCopy         []string
	)

	walkErr := filepath.WalkDir(mirror, func(absPath string, d fs.DirEntry, err error) error {
		if err != nil {
			// A permission hiccup on a single file shouldn't abort
			// the whole intake. Bubble dir-level errors so the operator
			// sees them.
			if d != nil && !d.IsDir() {
				return nil
			}
			return err
		}
		if absPath == mirror {
			return nil
		}
		rel, err := filepath.Rel(mirror, absPath)
		if err != nil {
			return err
		}
		rel = normalizeRel(rel)
		if tracked[rel] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if filter.shouldSkip(absPath, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if isDriveMetadata(rel) {
			return nil
		}
		// Symlinks: stat returned by WalkDir's fs.DirEntry doesn't
		// follow them; treat them as non-content and skip — push has
		// --no-links anyway, so they shouldn't be there.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		fp, err := FingerprintFile(absPath, mode)
		if err != nil {
			// Unreadable file — skip silently. Best-effort intake.
			return nil
		}

		if base, ok := baseline[rel]; ok && FingerprintsCompatible(base, fp, absPath) {
			skippedBase = append(skippedBase, rel)
			return nil
		} else if ok {
			skippedTracked = append(skippedTracked, rel)
			return nil
		}
		if imp, ok := imports[rel]; ok && FingerprintsCompatible(imp.FP, fp, absPath) {
			skippedImports = append(skippedImports, rel)
			return nil
		}

		intaked = append(intaked, rel)
		toCopy = append(toCopy, rel)
		importsToWrite[rel] = ImportEntry{FP: fp, ImportedAt: now}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking mirror: %w", walkErr)
	}

	sort.Strings(intaked)
	sort.Strings(skippedBase)
	sort.Strings(skippedImports)
	sort.Strings(skippedTracked)
	sort.Strings(toCopy)

	result := &IntakeResult{
		Pull:           pullRes,
		Intaked:        intaked,
		SkippedBase:    skippedBase,
		SkippedImports: skippedImports,
		SkippedTracked: skippedTracked,
		Tombstones:     pullRes.Tombstones,
		Strict:         opts.Strict,
	}

	if opts.DryRun {
		return result, nil
	}

	if len(toCopy) > 0 {
		stagingDir := filepath.Join(local, "inbox", "gdrive", intakeTS)
		if err := os.MkdirAll(stagingDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating staging dir: %w", err)
		}
		if err := stageFilesToInbox(ctx, runner, cfg, mirror, stagingDir, toCopy); err != nil {
			return nil, fmt.Errorf("staging files: %w", err)
		}
		if err := SaveImportsManifest(paths.ImportsFile, importsToWrite); err != nil {
			return nil, fmt.Errorf("saving imports manifest: %w", err)
		}
		result.StagingDir = stagingDir
		result.TimestampDir = intakeTS
	}

	if len(toCopy) > 0 {
		if err := UpdateLocalState(paths, func(s *LocalState) {
			s.LastIntake = now
			if len(toCopy) > 0 {
				s.LastIntakeTSDir = intakeTS
			}
		}); err != nil {
			return nil, fmt.Errorf("updating local state: %w", err)
		}
	}

	return result, nil
}

// isAlwaysExcluded matches the workspace-anchored excludes that push
// enforces unconditionally — they must not round-trip back via intake.
func isAlwaysExcluded(rel string) bool {
	if rel == ".dotfiles" || strings.HasPrefix(rel, ".dotfiles/") {
		return true
	}
	if rel == "inbox/gdrive" || strings.HasPrefix(rel, "inbox/gdrive/") {
		return true
	}
	return false
}

func isDriveMetadata(rel string) bool {
	return driveMetadataExt[strings.ToLower(filepath.Ext(rel))]
}

// stageFilesToInbox runs a single rsync invocation with --files-from
// to copy the chosen relpaths into stagingDir, preserving subtree
// structure. Falls back to a Go-side copy if rsync is unavailable
// (e.g., during tests on a setup that doesn't have rsync installed).
func stageFilesToInbox(ctx context.Context, runner *exec.Runner, cfg *Config, mirror, stagingDir string, rels []string) error {
	if len(rels) == 0 {
		return nil
	}

	if runner != nil && runner.CommandExists("rsync") {
		return rsyncStage(ctx, runner, cfg, mirror, stagingDir, rels)
	}
	return goCopyStage(mirror, stagingDir, rels)
}

func rsyncStage(ctx context.Context, runner *exec.Runner, cfg *Config, mirror, stagingDir string, rels []string) error {
	listFile, err := os.CreateTemp("", "gdrive-intake-files-from-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(listFile.Name())
	for _, r := range rels {
		if _, err := fmt.Fprintln(listFile, r); err != nil {
			listFile.Close()
			return err
		}
	}
	if err := listFile.Close(); err != nil {
		return err
	}
	args := []string{
		"-a", "--no-links",
		"--files-from=" + listFile.Name(),
		mirror + "/", stagingDir + "/",
	}
	if cfg.Verbose {
		return runner.RunAttached(ctx, "rsync", args...)
	}
	_, err = runner.Run(ctx, "rsync", args...)
	return err
}

// goCopyStage is the rsync-less fallback. Preserves mtime; doesn't try
// to be clever about hardlinks or special files (intake stages content
// only).
func goCopyStage(mirror, stagingDir string, rels []string) error {
	for _, rel := range rels {
		src := filepath.Join(mirror, rel)
		dst := filepath.Join(stagingDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFilePreservingMtime(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFilePreservingMtime(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// RefreshBaseline rebuilds <baseline.manifest> as the Git-shared Drive payload
// index. It records files that exist on both mirror and local and are not
// Git-tracked. Mirror-only files stay out of baseline so GDrive-origin new
// files continue to flow through inbox/gdrive until an operator accepts them.
func RefreshBaseline(cfg *Config, mode FingerprintMode) error {
	if cfg.LocalPaths == nil {
		return fmt.Errorf("refresh baseline: local paths unresolved")
	}
	mirror := strings.TrimRight(cfg.MirrorPath, "/")
	local := strings.TrimRight(cfg.LocalPath, "/")
	filter, err := newSyncFilter(cfg, mirror)
	if err != nil {
		return fmt.Errorf("loading filters: %w", err)
	}
	tracked := gitTrackedRelPaths(local)
	entries := map[string]Fingerprint{}
	err = filepath.WalkDir(mirror, func(absPath string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && !d.IsDir() {
				return nil
			}
			return err
		}
		if absPath == mirror {
			return nil
		}
		rel, err := filepath.Rel(mirror, absPath)
		if err != nil {
			return err
		}
		rel = normalizeRel(rel)
		if tracked[rel] {
			return nil
		}
		if filter.shouldSkip(absPath, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if isDriveMetadata(rel) {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		localAbs := filepath.Join(local, rel)
		localInfo, err := os.Lstat(localAbs)
		if err != nil || localInfo.IsDir() || localInfo.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		fp, err := FingerprintFile(absPath, mode)
		if err != nil {
			return nil
		}
		entries[rel] = fp
		return nil
	})
	if err != nil {
		return err
	}
	return SaveBaselineManifest(cfg.LocalPaths.BaselineFile, entries)
}
