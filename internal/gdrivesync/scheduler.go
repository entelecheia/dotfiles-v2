package gdrivesync

import (
	"context"
	osexec "os/exec"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// Stable identifiers used across launchd / systemd integrations.
// Distinct from the existing rsync sync labels so both schedulers can
// coexist on the same machine without colliding.
const (
	launchdLabel         = "com.dotfiles.gdrive-sync"
	launchdLabelIntake   = "com.dotfiles.gdrive-sync-intake"
	systemdServiceName   = "dotfiles-gdrive-sync.service"
	systemdTimerName     = "dotfiles-gdrive-sync.timer"
	systemdServiceIntake = "dotfiles-gdrive-sync-intake.service"
	systemdTimerIntake   = "dotfiles-gdrive-sync-intake.timer"
)

// SchedulerKind selects which periodic action a Scheduler call targets.
// Push is the always-on default; Intake runs tracked pull + new-file intake and
// is opt-in via PullInterval > 0.
type SchedulerKind int

const (
	SchedulerKindPush SchedulerKind = iota
	SchedulerKindIntake
)

// Action returns the gdrive-sync subcommand the unit should invoke.
func (k SchedulerKind) Action() string {
	if k == SchedulerKindIntake {
		return "intake"
	}
	return "push"
}

// LaunchdLabel returns the launchd Label for this kind.
func (k SchedulerKind) LaunchdLabel() string {
	if k == SchedulerKindIntake {
		return launchdLabelIntake
	}
	return launchdLabel
}

// SystemdServiceName returns the systemd service unit filename.
func (k SchedulerKind) SystemdServiceName() string {
	if k == SchedulerKindIntake {
		return systemdServiceIntake
	}
	return systemdServiceName
}

// SystemdTimerName returns the systemd timer unit filename.
func (k SchedulerKind) SystemdTimerName() string {
	if k == SchedulerKindIntake {
		return systemdTimerIntake
	}
	return systemdTimerName
}

// Description is the human-readable Description= line written into
// systemd units (and used for log/status banners).
func (k SchedulerKind) Description() string {
	if k == SchedulerKindIntake {
		return "gdrive-sync pull+intake (tracked payload restore + inbox/gdrive staging)"
	}
	return "gdrive-sync push (workspace → mirror)"
}

// SchedulerState is the runtime status of an auto-sync timer.
type SchedulerState int

const (
	SchedulerNotInstalled SchedulerState = iota
	SchedulerRunning
	SchedulerStopped
)

func (s SchedulerState) String() string {
	switch s {
	case SchedulerRunning:
		return "running"
	case SchedulerStopped:
		return "stopped"
	default:
		return "not installed"
	}
}

// SchedulerTemplateData feeds the launchd plist + systemd unit templates.
type SchedulerTemplateData struct {
	DotfilesPath string // absolute path to the `dot` (or `dotfiles`) binary
	LogFile      string
	Interval     int

	// Per-kind fields: the templates render the same file twice with
	// distinct labels/actions so push and intake units don't collide.
	Label       string // launchd Label
	Action      string // gdrive-sync subcommand to run
	Description string // systemd Description= line
	ServiceName string // systemd Unit= reference (timer → service)
}

// Scheduler manages the platform-specific periodic gdrive-sync timers.
//
// One Scheduler instance can install, pause, resume, or query either
// kind (push or intake). The high-level entry points (Install, Pause,
// Resume, State) act on the push unit by default and on both when
// cfg.PullInterval > 0; *Kind variants target a specific kind.
//
// Methods Install*Kind / Uninstall*Kind / Pause*Kind / Resume*Kind /
// StateKind are defined per platform in scheduler_darwin.go and
// scheduler_other.go.
type Scheduler struct {
	Runner *exec.Runner
	Paths  *Paths
	Config *Config
	Engine *template.Engine
}

// NewScheduler wires a Scheduler with all the things it needs to render
// templates and execute platform commands.
func NewScheduler(runner *exec.Runner, paths *Paths, cfg *Config, engine *template.Engine) *Scheduler {
	return &Scheduler{Runner: runner, Paths: paths, Config: cfg, Engine: engine}
}

// Install installs the push unit always; the intake unit is installed
// when cfg.PullInterval > 0 and uninstalled otherwise. Idempotent.
func (s *Scheduler) Install(ctx context.Context) error {
	if err := s.InstallKind(ctx, SchedulerKindPush); err != nil {
		return err
	}
	if s.Config.PullInterval > 0 {
		return s.InstallKind(ctx, SchedulerKindIntake)
	}
	// Operator may have flipped pull_interval to 0 (or removed the
	// field) — drop a previously installed intake unit so the system
	// reflects current config.
	return s.UninstallKind(ctx, SchedulerKindIntake)
}

// Uninstall removes both the push and intake units. Missing units are
// silently skipped (handled by the per-kind helpers).
func (s *Scheduler) Uninstall(ctx context.Context) error {
	if err := s.UninstallKind(ctx, SchedulerKindPush); err != nil {
		return err
	}
	return s.UninstallKind(ctx, SchedulerKindIntake)
}

// Pause stops both units (intake only if installed).
func (s *Scheduler) Pause(ctx context.Context) error {
	if err := s.PauseKind(ctx, SchedulerKindPush); err != nil {
		return err
	}
	return s.PauseKind(ctx, SchedulerKindIntake)
}

// Resume restarts both units (intake only if installed).
func (s *Scheduler) Resume(ctx context.Context) error {
	if err := s.ResumeKind(ctx, SchedulerKindPush); err != nil {
		return err
	}
	return s.ResumeKind(ctx, SchedulerKindIntake)
}

// State reports the push unit's status — the always-on default that
// status displays headline. Use StateKind(ctx, SchedulerKindIntake)
// for the optional intake unit's state.
func (s *Scheduler) State(ctx context.Context) SchedulerState {
	return s.StateKind(ctx, SchedulerKindPush)
}

// templateDataFor resolves the binary path (preferring `dotfiles` over
// `dot` for clarity in plist/unit ProgramArguments) and bundles the
// per-kind template inputs.
func (s *Scheduler) templateDataFor(kind SchedulerKind) SchedulerTemplateData {
	dotfilesPath, _ := osexec.LookPath("dotfiles")
	if dotfilesPath == "" {
		dotfilesPath, _ = osexec.LookPath("dot")
	}
	interval := s.Config.Interval
	if kind == SchedulerKindIntake {
		interval = s.Config.PullInterval
	}
	return SchedulerTemplateData{
		DotfilesPath: dotfilesPath,
		LogFile:      s.Config.LogFile,
		Interval:     interval,
		Label:        kind.LaunchdLabel(),
		Action:       kind.Action(),
		Description:  kind.Description(),
		ServiceName:  kind.SystemdServiceName(),
	}
}
