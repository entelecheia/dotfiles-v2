//go:build darwin

package gdrivesync

import (
	"context"
	"fmt"
	"path/filepath"
)

// InstallKind renders and loads the launchd plist for the given kind.
// Idempotent — unloads any pre-existing copy before reload so config
// changes (e.g. new Interval) take effect on rerun.
func (s *Scheduler) InstallKind(ctx context.Context, kind SchedulerKind) error {
	data := s.templateDataFor(kind)
	if data.DotfilesPath == "" {
		return fmt.Errorf("cannot find dotfiles binary in PATH; run `make install` first")
	}

	content, err := s.Engine.Render("gdrivesync/com.dotfiles.gdrive-sync.plist.tmpl", data)
	if err != nil {
		return fmt.Errorf("rendering plist: %w", err)
	}

	plist := s.Paths.PlistFor(kind)
	dir := filepath.Dir(plist)
	if err := s.Runner.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating LaunchAgents dir: %w", err)
	}
	if err := s.Runner.WriteFile(plist, content, 0644); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	// Unload first (ignore the error if it wasn't loaded already).
	_, _ = s.Runner.Run(ctx, "launchctl", "unload", plist)

	if _, err := s.Runner.Run(ctx, "launchctl", "load", plist); err != nil {
		return fmt.Errorf("loading plist: %w", err)
	}
	return nil
}

// UninstallKind unloads the launchd job and removes the plist file
// for the given kind. Missing-file is not an error.
func (s *Scheduler) UninstallKind(ctx context.Context, kind SchedulerKind) error {
	plist := s.Paths.PlistFor(kind)
	_, _ = s.Runner.Run(ctx, "launchctl", "unload", plist)
	if !s.Runner.FileExists(plist) {
		return nil
	}
	return s.Runner.Remove(plist)
}

// PauseKind unloads the launchd job (file stays on disk so Resume
// re-attaches).
func (s *Scheduler) PauseKind(ctx context.Context, kind SchedulerKind) error {
	plist := s.Paths.PlistFor(kind)
	if !s.Runner.FileExists(plist) {
		return nil
	}
	_, err := s.Runner.Run(ctx, "launchctl", "unload", plist)
	return err
}

// ResumeKind re-loads the launchd job from its persisted plist.
func (s *Scheduler) ResumeKind(ctx context.Context, kind SchedulerKind) error {
	plist := s.Paths.PlistFor(kind)
	if !s.Runner.FileExists(plist) {
		return nil
	}
	_, err := s.Runner.Run(ctx, "launchctl", "load", plist)
	return err
}

// StateKind queries launchctl to report the unit's runtime status for
// the given kind.
func (s *Scheduler) StateKind(ctx context.Context, kind SchedulerKind) SchedulerState {
	plist := s.Paths.PlistFor(kind)
	if !s.Runner.FileExists(plist) {
		return SchedulerNotInstalled
	}
	result, err := s.Runner.Run(ctx, "launchctl", "list", kind.LaunchdLabel())
	if err != nil || result.ExitCode != 0 {
		return SchedulerStopped
	}
	return SchedulerRunning
}
