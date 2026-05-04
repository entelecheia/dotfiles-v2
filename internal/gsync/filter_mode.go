package gsync

import (
	"fmt"
	"strings"
)

// FilterMode controls the top-level sync selection strategy.
type FilterMode string

const (
	FilterModeInclude FilterMode = "include"
	FilterModeExclude FilterMode = "exclude"
)

// DefaultFilterMode is intentionally include-first: Drive mirrors binary
// payloads, while Git remains authoritative for source/text files.
func DefaultFilterMode() FilterMode {
	return FilterModeInclude
}

func ParseFilterMode(raw string) (FilterMode, error) {
	switch FilterMode(strings.ToLower(strings.TrimSpace(raw))) {
	case FilterModeInclude:
		return FilterModeInclude, nil
	case FilterModeExclude:
		return FilterModeExclude, nil
	default:
		return "", fmt.Errorf("unknown filter mode %q (want include|exclude)", raw)
	}
}

func (m FilterMode) Validate() error {
	_, err := ParseFilterMode(string(m))
	return err
}

func (m FilterMode) String() string {
	if m == "" {
		return string(DefaultFilterMode())
	}
	return string(m)
}

func normalizeFilterMode(m FilterMode) FilterMode {
	if parsed, err := ParseFilterMode(string(m)); err == nil {
		return parsed
	}
	return DefaultFilterMode()
}
