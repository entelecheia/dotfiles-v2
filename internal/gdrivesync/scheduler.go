package gdrivesync

import (
	osexec "os/exec"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// Stable identifiers used across launchd / systemd integrations.
// Distinct from the existing rsync sync labels so both schedulers can
// coexist on the same machine without colliding.
const (
	launchdLabel       = "com.dotfiles.gdrive-sync"
	systemdServiceName = "dotfiles-gdrive-sync.service"
	systemdTimerName   = "dotfiles-gdrive-sync.timer"
)

// SchedulerState is the runtime status of the auto-sync timer.
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
}

// Scheduler manages the platform-specific periodic gdrive-sync timer.
//
// Methods Install / Uninstall / Pause / Resume / State are defined per
// platform in scheduler_darwin.go and scheduler_other.go.
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

// templateData resolves the binary path (preferring `dotfiles` over the
// `dot` alias for clarity in plist/unit ProgramArguments) and bundles
// the template inputs.
func (s *Scheduler) templateData() SchedulerTemplateData {
	dotfilesPath, _ := osexec.LookPath("dotfiles")
	if dotfilesPath == "" {
		dotfilesPath, _ = osexec.LookPath("dot")
	}
	return SchedulerTemplateData{
		DotfilesPath: dotfilesPath,
		LogFile:      s.Config.LogFile,
		Interval:     s.Config.Interval,
	}
}
