// Package watchdog implements the dot watchdog: a runaway-process reaper
// (phase P1 of the watchdog feature) plus the shared notifier, JSON-lines
// log, and LaunchAgent plumbing the later connectivity/power phases reuse.
package watchdog

import (
	"fmt"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

// Reaper modes.
const (
	ModeDryRun  = "dry-run"
	ModeEnforce = "enforce"
)

// Defaults applied when the profile leaves a reaper knob unset. They encode
// the incident shape the reaper exists for: a build tool spinning near a
// full core for tens of minutes, checked every five.
const (
	DefaultInterval     = 300 * time.Second
	DefaultCPUThreshold = 50.0 // percent, per process
	DefaultSustain      = 30 * time.Minute
	DefaultKillGrace    = 10 * time.Second
)

// ReaperSettings is the resolved, defaults-applied form of
// config.WatchdogReaperConfig that the engine runs against.
type ReaperSettings struct {
	Mode         string
	Interval     time.Duration
	CPUThreshold float64
	Sustain      time.Duration
	OrphanOnly   bool
	Paths        []string
	Args         []string
	Allow        []string
	KillGrace    time.Duration
}

// NotifySettings is the resolved notifier configuration.
type NotifySettings struct {
	MacOS   bool
	NtfyURL string
}

// ResolveReaper applies defaults and validates the mode.
func ResolveReaper(c config.WatchdogReaperConfig) (ReaperSettings, error) {
	s := ReaperSettings{
		Mode:         c.Mode,
		Interval:     c.Interval.Std(),
		CPUThreshold: c.CPUThreshold,
		Sustain:      c.Sustain.Std(),
		OrphanOnly:   c.Match.OrphanOnly,
		Paths:        c.Match.Paths,
		Args:         c.Match.Args,
		Allow:        c.Allow,
		KillGrace:    c.KillGrace.Std(),
	}
	if s.Mode == "" {
		s.Mode = ModeDryRun
	}
	if s.Mode != ModeDryRun && s.Mode != ModeEnforce {
		return ReaperSettings{}, fmt.Errorf("watchdog reaper mode must be %q or %q, got %q", ModeDryRun, ModeEnforce, s.Mode)
	}
	if s.Interval <= 0 {
		s.Interval = DefaultInterval
	}
	if s.CPUThreshold <= 0 {
		s.CPUThreshold = DefaultCPUThreshold
	}
	if s.Sustain <= 0 {
		s.Sustain = DefaultSustain
	}
	if s.KillGrace <= 0 {
		s.KillGrace = DefaultKillGrace
	}
	return s, nil
}

// ResolveNotify maps the notify block through unchanged; it exists so every
// consumer reads notifier config through one seam.
func ResolveNotify(c config.WatchdogNotifyConfig) NotifySettings {
	return NotifySettings{MacOS: c.MacOS, NtfyURL: c.NtfyURL}
}
