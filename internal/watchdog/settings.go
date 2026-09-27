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

// Defaults applied when the profile leaves a warp knob unset: probe every
// two minutes, act after two consecutive failures.
const (
	DefaultWarpInterval      = 120 * time.Second
	DefaultWarpFailThreshold = 2
)

// WarpSettings is the resolved, defaults-applied form of
// config.WatchdogWarpConfig.
type WarpSettings struct {
	Interval      time.Duration
	FailThreshold int
}

// ResolveWarp applies defaults.
func ResolveWarp(c config.WatchdogWarpConfig) WarpSettings {
	s := WarpSettings{
		Interval:      c.Interval.Std(),
		FailThreshold: c.FailThreshold,
	}
	if s.Interval <= 0 {
		s.Interval = DefaultWarpInterval
	}
	if s.FailThreshold <= 0 {
		s.FailThreshold = DefaultWarpFailThreshold
	}
	return s
}

// Defaults applied when the profile leaves a monit knob unset. The load
// threshold has no constant default: it resolves to the host's logical CPU
// count, injected by the caller. CyclesMax mirrors monit's own parser limit.
const (
	DefaultMonitCPUUserThreshold   = 90.0 // percent
	DefaultMonitCPUSystemThreshold = 50.0 // percent
	DefaultMonitCycles             = 3
	MonitCyclesMax                 = 64
)

// MonitSettings is the resolved, defaults-applied form of
// config.WatchdogMonitConfig that RenderMonitrc renders against.
type MonitSettings struct {
	Load1Threshold     float64
	CPUUserThreshold   float64
	CPUSystemThreshold float64
	Cycles             int
	ScreenSharing      bool
}

// ResolveMonit applies defaults and validates the thresholds. logicalCPU is
// the host's logical CPU count (runtime.NumCPU at the call site), injected so
// the default load threshold stays deterministic under test.
func ResolveMonit(c config.WatchdogMonitConfig, logicalCPU int) (MonitSettings, error) {
	s := MonitSettings{
		Load1Threshold:     c.Load1Threshold,
		CPUUserThreshold:   c.CPUUserThreshold,
		CPUSystemThreshold: c.CPUSystemThreshold,
		Cycles:             c.Cycles,
		ScreenSharing:      true,
	}
	if c.ScreenSharing != nil {
		s.ScreenSharing = *c.ScreenSharing
	}
	if s.Load1Threshold <= 0 {
		if logicalCPU < 1 {
			return MonitSettings{}, fmt.Errorf("monit load1_threshold is unset and the logical CPU count is %d", logicalCPU)
		}
		s.Load1Threshold = float64(logicalCPU)
	}
	if s.CPUUserThreshold <= 0 {
		s.CPUUserThreshold = DefaultMonitCPUUserThreshold
	}
	if s.CPUSystemThreshold <= 0 {
		s.CPUSystemThreshold = DefaultMonitCPUSystemThreshold
	}
	if s.Cycles <= 0 {
		s.Cycles = DefaultMonitCycles
	}
	if s.CPUUserThreshold > 100 || s.CPUSystemThreshold > 100 {
		return MonitSettings{}, fmt.Errorf("monit cpu thresholds are percents of one system, got user %.4g%% / system %.4g%%", s.CPUUserThreshold, s.CPUSystemThreshold)
	}
	if s.Cycles > MonitCyclesMax {
		return MonitSettings{}, fmt.Errorf("monit cycles must be at most %d (monit's parser limit), got %d", MonitCyclesMax, s.Cycles)
	}
	return s, nil
}
