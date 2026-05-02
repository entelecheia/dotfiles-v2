//go:build !darwin

package gdrivesync

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// InstallKind renders the systemd user service + timer for the kind
// and enables them. Idempotent — daemon-reload is safe on every
// invocation, and `enable --now` accepts an already-enabled unit.
func (s *Scheduler) InstallKind(ctx context.Context, kind SchedulerKind) error {
	data := s.templateDataFor(kind)
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

	servicePath := s.Paths.SystemdServiceFor(kind)
	timerPath := s.Paths.SystemdTimerFor(kind)
	dir := filepath.Dir(servicePath)
	if err := s.Runner.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating systemd dir: %w", err)
	}
	if err := s.Runner.WriteFile(servicePath, svcContent, 0644); err != nil {
		return fmt.Errorf("writing service: %w", err)
	}
	if err := s.Runner.WriteFile(timerPath, timerContent, 0644); err != nil {
		return fmt.Errorf("writing timer: %w", err)
	}

	if _, err := s.Runner.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if _, err := s.Runner.Run(ctx, "systemctl", "--user", "enable", "--now", kind.SystemdTimerName()); err != nil {
		return fmt.Errorf("enabling timer: %w", err)
	}
	return nil
}

// UninstallKind disables the timer and removes the systemd unit files
// for the given kind. Missing-file is not an error.
func (s *Scheduler) UninstallKind(ctx context.Context, kind SchedulerKind) error {
	timer := s.Paths.SystemdTimerFor(kind)
	service := s.Paths.SystemdServiceFor(kind)
	_, _ = s.Runner.Run(ctx, "systemctl", "--user", "disable", "--now", kind.SystemdTimerName())
	_ = s.Runner.Remove(timer)
	_ = s.Runner.Remove(service)
	_, _ = s.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
	return nil
}

// PauseKind stops the timer for the kind (units stay on disk; Resume
// restarts).
func (s *Scheduler) PauseKind(ctx context.Context, kind SchedulerKind) error {
	if !s.Runner.FileExists(s.Paths.SystemdTimerFor(kind)) {
		return nil
	}
	_, err := s.Runner.Run(ctx, "systemctl", "--user", "stop", kind.SystemdTimerName())
	return err
}

// ResumeKind starts the timer for the kind.
func (s *Scheduler) ResumeKind(ctx context.Context, kind SchedulerKind) error {
	if !s.Runner.FileExists(s.Paths.SystemdTimerFor(kind)) {
		return nil
	}
	_, err := s.Runner.Run(ctx, "systemctl", "--user", "start", kind.SystemdTimerName())
	return err
}

// StateKind asks systemctl for the timer's runtime status for the kind.
func (s *Scheduler) StateKind(ctx context.Context, kind SchedulerKind) SchedulerState {
	if !s.Runner.FileExists(s.Paths.SystemdTimerFor(kind)) {
		return SchedulerNotInstalled
	}
	result, err := s.Runner.Run(ctx, "systemctl", "--user", "is-active", kind.SystemdTimerName())
	if err != nil || result.ExitCode != 0 {
		return SchedulerStopped
	}
	if strings.TrimSpace(result.Stdout) == "active" {
		return SchedulerRunning
	}
	return SchedulerStopped
}
