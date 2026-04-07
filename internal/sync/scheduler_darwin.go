//go:build darwin

package sync

import (
	"context"
	"fmt"
	"path/filepath"
)

// Install deploys the launchd plist and loads the timer.
func (s *Scheduler) Install(ctx context.Context) error {
	data := s.templateData()

	content, err := s.Engine.Render("sync/com.rclone.workspace-bisync.plist.tmpl", data)
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

	// Unload first (ignore error if not loaded)
	_, _ = s.Runner.Run(ctx, "launchctl", "unload", s.Paths.LaunchdPlist)

	_, err = s.Runner.Run(ctx, "launchctl", "load", s.Paths.LaunchdPlist)
	if err != nil {
		return fmt.Errorf("loading plist: %w", err)
	}

	return nil
}

// Uninstall removes the launchd plist and unloads the timer.
func (s *Scheduler) Uninstall(ctx context.Context) error {
	_, _ = s.Runner.Run(ctx, "launchctl", "unload", s.Paths.LaunchdPlist)
	return s.Runner.Remove(s.Paths.LaunchdPlist)
}

// Pause stops the auto-sync timer.
func (s *Scheduler) Pause(ctx context.Context) error {
	_, err := s.Runner.Run(ctx, "launchctl", "unload", s.Paths.LaunchdPlist)
	return err
}

// Resume starts the auto-sync timer.
func (s *Scheduler) Resume(ctx context.Context) error {
	_, err := s.Runner.Run(ctx, "launchctl", "load", s.Paths.LaunchdPlist)
	return err
}

// State returns the current scheduler state.
func (s *Scheduler) State(ctx context.Context) SchedulerState {
	if !s.Runner.FileExists(s.Paths.LaunchdPlist) {
		return SchedulerNotInstalled
	}
	result, err := s.Runner.Run(ctx, "launchctl", "list", "com.rclone.workspace-bisync")
	if err != nil || result.ExitCode != 0 {
		return SchedulerStopped
	}
	return SchedulerRunning
}
