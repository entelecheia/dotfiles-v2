//go:build !darwin

package rsync

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Install deploys systemd unit files and enables the timer.
func (s *Scheduler) Install(ctx context.Context) error {
	data := s.templateData()

	svcContent, err := s.Engine.Render("rsync/dotfiles-sync.service.tmpl", data)
	if err != nil {
		return fmt.Errorf("rendering service: %w", err)
	}

	timerContent, err := s.Engine.Render("rsync/dotfiles-sync.timer.tmpl", data)
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

	if _, err := s.Runner.Run(ctx, "systemctl", "--user", "enable", "--now", "dotfiles-sync.timer"); err != nil {
		return fmt.Errorf("enabling timer: %w", err)
	}

	return nil
}

// Uninstall disables the timer and removes unit files.
func (s *Scheduler) Uninstall(ctx context.Context) error {
	_, _ = s.Runner.Run(ctx, "systemctl", "--user", "disable", "--now", "dotfiles-sync.timer")
	_ = s.Runner.Remove(s.Paths.SystemdTimer)
	_ = s.Runner.Remove(s.Paths.SystemdService)
	_, _ = s.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
	return nil
}

// Pause stops the auto-sync timer.
func (s *Scheduler) Pause(ctx context.Context) error {
	_, err := s.Runner.Run(ctx, "systemctl", "--user", "stop", "dotfiles-sync.timer")
	return err
}

// Resume starts the auto-sync timer.
func (s *Scheduler) Resume(ctx context.Context) error {
	_, err := s.Runner.Run(ctx, "systemctl", "--user", "start", "dotfiles-sync.timer")
	return err
}

// State returns the current scheduler state.
func (s *Scheduler) State(ctx context.Context) SchedulerState {
	if !s.Runner.FileExists(s.Paths.SystemdTimer) {
		return SchedulerNotInstalled
	}
	result, err := s.Runner.Run(ctx, "systemctl", "--user", "is-active", "dotfiles-sync.timer")
	if err != nil || result.ExitCode != 0 {
		return SchedulerStopped
	}
	if strings.TrimSpace(result.Stdout) == "active" {
		return SchedulerRunning
	}
	return SchedulerStopped
}
