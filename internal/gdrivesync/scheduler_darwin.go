//go:build darwin

package gdrivesync

import (
	"context"
	"fmt"
	"path/filepath"
)

// Install renders and loads the launchd plist for periodic gdrive-sync.
// Idempotent — unloads any pre-existing copy before reload so config
// changes (e.g. new Interval) take effect on rerun.
func (s *Scheduler) Install(ctx context.Context) error {
	data := s.templateData()
	if data.DotfilesPath == "" {
		return fmt.Errorf("cannot find dotfiles binary in PATH; run `make install` first")
	}

	content, err := s.Engine.Render("gdrivesync/com.dotfiles.gdrive-sync.plist.tmpl", data)
	if err != nil {
		return fmt.Errorf("rendering plist: %w", err)
	}

	dir := filepath.Dir(s.Paths.LaunchdPlist)
	if err := s.Runner.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating LaunchAgents dir: %w", err)
	}
	if err := s.Runner.WriteFile(s.Paths.LaunchdPlist, content, 0644); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	// Unload first (ignore the error if it wasn't loaded already).
	_, _ = s.Runner.Run(ctx, "launchctl", "unload", s.Paths.LaunchdPlist)

	if _, err := s.Runner.Run(ctx, "launchctl", "load", s.Paths.LaunchdPlist); err != nil {
		return fmt.Errorf("loading plist: %w", err)
	}
	return nil
}

// Uninstall unloads the launchd job and removes the plist file.
func (s *Scheduler) Uninstall(ctx context.Context) error {
	_, _ = s.Runner.Run(ctx, "launchctl", "unload", s.Paths.LaunchdPlist)
	if err := s.Runner.Remove(s.Paths.LaunchdPlist); err != nil {
		return err
	}
	return nil
}

// Pause unloads the launchd job (file stays on disk so Resume re-attaches).
func (s *Scheduler) Pause(ctx context.Context) error {
	_, err := s.Runner.Run(ctx, "launchctl", "unload", s.Paths.LaunchdPlist)
	return err
}

// Resume re-loads the launchd job from its persisted plist.
func (s *Scheduler) Resume(ctx context.Context) error {
	_, err := s.Runner.Run(ctx, "launchctl", "load", s.Paths.LaunchdPlist)
	return err
}

// State queries launchctl to report the current scheduler status.
func (s *Scheduler) State(ctx context.Context) SchedulerState {
	if !s.Runner.FileExists(s.Paths.LaunchdPlist) {
		return SchedulerNotInstalled
	}
	result, err := s.Runner.Run(ctx, "launchctl", "list", launchdLabel)
	if err != nil || result.ExitCode != 0 {
		return SchedulerStopped
	}
	return SchedulerRunning
}
