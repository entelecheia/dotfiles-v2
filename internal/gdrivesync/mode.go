package gdrivesync

import (
	"fmt"
	"strings"
)

// RunMode controls whether a pull/push applies interactively or automatically.
type RunMode string

const (
	ModeManual RunMode = "manual"
	ModeClean  RunMode = "clean"
	ModeForce  RunMode = "force"
)

func ParseRunMode(raw string) (RunMode, error) {
	switch RunMode(strings.TrimSpace(strings.ToLower(raw))) {
	case "", ModeManual:
		return ModeManual, nil
	case ModeClean:
		return ModeClean, nil
	case ModeForce:
		return ModeForce, nil
	default:
		return "", fmt.Errorf("unknown mode %q (want manual|clean|force)", raw)
	}
}

func normalizeAutomaticMode(raw RunMode) (RunMode, error) {
	switch raw {
	case "", ModeClean:
		return ModeClean, nil
	case ModeForce:
		return ModeForce, nil
	case ModeManual:
		return "", fmt.Errorf("manual mode cannot be used by automatic schedulers")
	default:
		return "", fmt.Errorf("unknown mode %q (want clean|force)", raw)
	}
}

func (m RunMode) String() string {
	if m == "" {
		return string(ModeManual)
	}
	return string(m)
}
