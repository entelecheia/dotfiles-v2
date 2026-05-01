//go:build !darwin

package gdrivesync

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Install renders the systemd user service + timer and enables them.
// Idempotent — daemon-reload is safe on every invocation, and `enable
// --now` accepts an already-enabled unit.
func (s *Scheduler) Install(ctx context.Context) error {
	data := s.templateData()
	if data.DotfilesPath == "" {
		return fmt.Errorf("cannot find dotfiles binary in PATH; run `make install` first")
	}

	svcContent, err := s.Engine.Render("gdrivesync/dotfiles-gdrive-sync.service.tmpl", data)
	if err != nil {
		return fmt.Errorf("rendering service: %w", err)
	}
	timerContent, err := s.Engine.Render("gdrivesync/dotfiles-gdrive-sync.timer.tmpl", data)
	if err != nil {
		return fmt.Errorf("rendering timer: %w", err)
	}

	dir := filepath.Dir(s.Paths.SystemdService)
	if err := s.Runner.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating systemd dir: %w", err)
	}
	if err := s.Runner.WriteFile(s.Paths.SystemdService, svcContent, 0644); err != nil {
		return fmt.Errorf("writing service: %w", err)
	}
	if err := s.Runner.WriteFile(s.Paths.SystemdTimer, timerContent, 0644); err != nil {
		return fmt.Errorf("writing timer: %w", err)
	}

	if _, err := s.Runner.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if _, err := s.Runner.Run(ctx, "systemctl", "--user", "enable", "--now", systemdTimerName); err != nil {
		return fmt.Errorf("enabling timer: %w", err)
	}
	return nil
}

// Uninstall disables the timer and removes the systemd unit files.
func (s *Scheduler) Uninstall(ctx context.Context) error {
	_, _ = s.Runner.Run(ctx, "systemctl", "--user", "disable", "--now", systemdTimerName)
	_ = s.Runner.Remove(s.Paths.SystemdTimer)
	_ = s.Runner.Remove(s.Paths.SystemdService)
	_, _ = s.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
	return nil
}

// Pause stops the timer (units stay on disk; Resume restarts).
func (s *Scheduler) Pause(ctx context.Context) error {
	_, err := s.Runner.Run(ctx, "systemctl", "--user", "stop", systemdTimerName)
	return err
}

// Resume starts the timer.
func (s *Scheduler) Resume(ctx context.Context) error {
	_, err := s.Runner.Run(ctx, "systemctl", "--user", "start", systemdTimerName)
	return err
}

// State asks systemctl for the timer's runtime status.
func (s *Scheduler) State(ctx context.Context) SchedulerState {
	if !s.Runner.FileExists(s.Paths.SystemdTimer) {
		return SchedulerNotInstalled
	}
	result, err := s.Runner.Run(ctx, "systemctl", "--user", "is-active", systemdTimerName)
	if err != nil || result.ExitCode != 0 {
		return SchedulerStopped
	}
	if strings.TrimSpace(result.Stdout) == "active" {
		return SchedulerRunning
	}
	return SchedulerStopped
}
