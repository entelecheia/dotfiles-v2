package gsync

import "fmt"

const (
	// ScheduleIntervalMin is the shortest supported automatic scheduler cadence.
	ScheduleIntervalMin = 60
	// ScheduleIntervalMax is the longest supported automatic scheduler cadence.
	ScheduleIntervalMax = 86400
)

// ScheduleSettings owns the gsync automatic scheduling values that flow
// through CLI flags, LocalConfig, resolved Config, and scheduler templates.
type ScheduleSettings struct {
	Interval     int
	PullInterval int
	PushMode     RunMode
	PullMode     RunMode
}

// ScheduleSettingsFromLocalConfig extracts scheduler settings from cfg.
func ScheduleSettingsFromLocalConfig(cfg *LocalConfig) ScheduleSettings {
	if cfg == nil {
		return ScheduleSettings{}
	}
	return ScheduleSettings{
		Interval:     cfg.Interval,
		PullInterval: cfg.PullInterval,
		PushMode:     cfg.PushMode,
		PullMode:     cfg.PullMode,
	}
}

// ApplyToLocalConfig writes normalized scheduler settings back to cfg.
func (s ScheduleSettings) ApplyToLocalConfig(cfg *LocalConfig) {
	if cfg == nil {
		return
	}
	cfg.Interval = s.Interval
	cfg.PullInterval = s.PullInterval
	cfg.PushMode = s.PushMode
	cfg.PullMode = s.PullMode
}

// ValidateScheduleInterval accepts 0 (off) or a bounded positive cadence.
func ValidateScheduleInterval(seconds int) error {
	if seconds == 0 {
		return nil
	}
	if seconds < ScheduleIntervalMin || seconds > ScheduleIntervalMax {
		return fmt.Errorf("must be 0 or %d..%d seconds (got %d)", ScheduleIntervalMin, ScheduleIntervalMax, seconds)
	}
	return nil
}

// NormalizeScheduleInterval clamps manually edited config values into the
// supported range. Negative values disable scheduling.
func NormalizeScheduleInterval(seconds int) int {
	switch {
	case seconds <= 0:
		return 0
	case seconds < ScheduleIntervalMin:
		return ScheduleIntervalMin
	case seconds > ScheduleIntervalMax:
		return ScheduleIntervalMax
	default:
		return seconds
	}
}

// NormalizeAutomaticMode exposes the automatic-scheduler mode validator.
func NormalizeAutomaticMode(raw RunMode) (RunMode, error) {
	return normalizeAutomaticMode(raw)
}

// Normalize validates all scheduler settings for save/CLI paths.
func (s ScheduleSettings) Normalize() (ScheduleSettings, error) {
	if err := ValidateScheduleInterval(s.Interval); err != nil {
		return s, fmt.Errorf("interval: %w", err)
	}
	if err := ValidateScheduleInterval(s.PullInterval); err != nil {
		return s, fmt.Errorf("pull_interval: %w", err)
	}
	pushMode, err := normalizeAutomaticMode(s.PushMode)
	if err != nil {
		return s, fmt.Errorf("push_mode: %w", err)
	}
	pullMode, err := normalizeAutomaticMode(s.PullMode)
	if err != nil {
		return s, fmt.Errorf("pull_mode: %w", err)
	}
	s.PushMode = pushMode
	s.PullMode = pullMode
	return s, nil
}

// NormalizeLenient heals manually edited config values for load/resolve paths.
// warnf receives the field name, validation error, and fallback value.
func (s ScheduleSettings) NormalizeLenient(warnf func(field string, err error, fallback any)) ScheduleSettings {
	if err := ValidateScheduleInterval(s.Interval); err != nil {
		fallback := NormalizeScheduleInterval(s.Interval)
		if warnf != nil {
			warnf("interval", err, fallback)
		}
		s.Interval = fallback
	}
	if err := ValidateScheduleInterval(s.PullInterval); err != nil {
		fallback := NormalizeScheduleInterval(s.PullInterval)
		if warnf != nil {
			warnf("pull_interval", err, fallback)
		}
		s.PullInterval = fallback
	}
	if mode, err := normalizeAutomaticMode(s.PushMode); err != nil {
		if warnf != nil {
			warnf("push_mode", err, ModeClean)
		}
		s.PushMode = ModeClean
	} else {
		s.PushMode = mode
	}
	if mode, err := normalizeAutomaticMode(s.PullMode); err != nil {
		if warnf != nil {
			warnf("pull_mode", err, ModeClean)
		}
		s.PullMode = ModeClean
	} else {
		s.PullMode = mode
	}
	return s
}
