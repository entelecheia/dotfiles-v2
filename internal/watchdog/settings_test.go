package watchdog

import (
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

func TestResolveReaper_Defaults(t *testing.T) {
	s, err := ResolveReaper(config.WatchdogReaperConfig{})
	if err != nil {
		t.Fatalf("ResolveReaper: %v", err)
	}
	if s.Mode != ModeDryRun {
		t.Errorf("default mode = %q, want dry-run", s.Mode)
	}
	if s.Interval != DefaultInterval || s.Sustain != DefaultSustain || s.KillGrace != DefaultKillGrace {
		t.Errorf("defaults not applied: %#v", s)
	}
	if s.CPUThreshold != DefaultCPUThreshold {
		t.Errorf("default threshold = %v", s.CPUThreshold)
	}
}

func TestResolveReaper_RejectsUnknownMode(t *testing.T) {
	_, err := ResolveReaper(config.WatchdogReaperConfig{Mode: "napalm"})
	if err == nil {
		t.Fatal("unknown mode must be rejected")
	}
}

func TestResolveReaper_KeepsExplicitValues(t *testing.T) {
	s, err := ResolveReaper(config.WatchdogReaperConfig{
		Mode:         "enforce",
		Interval:     config.Duration(120 * time.Second),
		CPUThreshold: 80,
		Sustain:      config.Duration(10 * time.Minute),
		KillGrace:    config.Duration(3 * time.Second),
		Match:        config.WatchdogMatchConfig{OrphanOnly: true, Args: []string{"bun"}},
		Allow:        []string{"cloudflared"},
	})
	if err != nil {
		t.Fatalf("ResolveReaper: %v", err)
	}
	if s.Mode != ModeEnforce || s.Interval != 120*time.Second || s.CPUThreshold != 80 ||
		s.Sustain != 10*time.Minute || s.KillGrace != 3*time.Second {
		t.Errorf("explicit values lost: %#v", s)
	}
	if !s.OrphanOnly || len(s.Args) != 1 || len(s.Allow) != 1 {
		t.Errorf("match/allow not carried: %#v", s)
	}
}

func TestResolveWarp_Defaults(t *testing.T) {
	s := ResolveWarp(config.WatchdogWarpConfig{})
	if s.Interval != DefaultWarpInterval || s.FailThreshold != DefaultWarpFailThreshold {
		t.Errorf("defaults = %#v, want 120s/2", s)
	}
}

func TestResolveWarp_KeepsExplicitValues(t *testing.T) {
	s := ResolveWarp(config.WatchdogWarpConfig{
		Interval:      config.Duration(60 * time.Second),
		FailThreshold: 4,
	})
	if s.Interval != 60*time.Second || s.FailThreshold != 4 {
		t.Errorf("explicit values lost: %#v", s)
	}
}
