package rsync

import (
	osexec "os/exec"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// SchedulerState represents the auto-sync scheduler state.
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

// TemplateData holds data for rendering scheduler templates.
type TemplateData struct {
	DotfilesPath string
	LogFile      string
	Interval     int
}

// Scheduler manages the platform-specific periodic sync timer.
type Scheduler struct {
	Runner *exec.Runner
	Paths  *Paths
	Config *Config
	Engine *template.Engine
}

// NewScheduler creates a new Scheduler.
func NewScheduler(runner *exec.Runner, paths *Paths, cfg *Config, engine *template.Engine) *Scheduler {
	return &Scheduler{
		Runner: runner,
		Paths:  paths,
		Config: cfg,
		Engine: engine,
	}
}

// templateData returns the data struct for template rendering.
func (s *Scheduler) templateData() TemplateData {
	dotfilesPath, _ := osexec.LookPath("dotfiles")
	if dotfilesPath == "" {
		dotfilesPath, _ = osexec.LookPath("dot")
	}
	return TemplateData{
		DotfilesPath: dotfilesPath,
		LogFile:      s.Config.LogFile,
		Interval:     s.Config.Interval,
	}
}
