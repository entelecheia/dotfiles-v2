package ui

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/entelecheia/dotfiles-v2/internal/sliceutil"
)

// Styles for consistent output across the CLI.
var (
	StyleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7AA2F7")).
			BorderForeground(lipgloss.Color("#7AA2F7")).
			Padding(0, 1)

	StyleSection = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#BB9AF7")).
			MarginTop(1)

	StyleKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9ECE6A")).
			Width(14)

	StyleValue = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C0CAF5"))

	StyleMuted = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#565F89")).
			Italic(true)

	StyleSuccess = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9ECE6A")).
			Bold(true)

	StyleWarning = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E0AF68")).
			Bold(true)

	StyleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F7768E")).
			Bold(true)

	StyleHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#565F89"))
)

// Confirm asks for yes/no. Returns true immediately if unattended.
func Confirm(message string, unattended bool) (bool, error) {
	if unattended {
		return true, nil
	}
	var confirmed bool
	err := huh.NewConfirm().
		Title(message).
		Value(&confirmed).
		Run()
	return confirmed, err
}

// Select presents a choice. Returns defaultVal if unattended.
// Default is pre-selected in the list.
func Select(message string, options []string, defaultVal string, unattended bool) (string, error) {
	if unattended {
		return defaultVal, nil
	}
	selected := defaultVal
	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o, o)
	}
	err := huh.NewSelect[string]().
		Title(message).
		Options(opts...).
		Value(&selected).
		Run()
	if err != nil {
		return defaultVal, err
	}
	return selected, nil
}

// Input asks for text input. Pre-filled with defaultVal so user can edit
// existing value directly instead of retyping. Returns defaultVal if unattended.
func Input(message, defaultVal string, unattended bool) (string, error) {
	if unattended {
		return defaultVal, nil
	}
	// Seed value with default so user sees and can edit it
	value := defaultVal
	input := huh.NewInput().
		Title(message).
		Value(&value)
	if defaultVal == "" {
		input = input.Placeholder("(empty)")
	}
	if err := input.Run(); err != nil {
		return defaultVal, err
	}
	if value == "" {
		return defaultVal, nil
	}
	return value, nil
}

// InputWithDetected is like Input but marks a system-detected default.
// Displays "(auto-detected)" hint when defaultVal comes from system detection.
func InputWithDetected(message, defaultVal string, detected bool, unattended bool) (string, error) {
	if detected && defaultVal != "" {
		message = message + " " + StyleHint.Render("(auto-detected)")
	}
	return Input(message, defaultVal, unattended)
}

// MultiSelect presents a checkbox-style multi-select. Returns defaultVals
// unchanged if unattended. Options pre-selected where they appear in defaultVals.
func MultiSelect(message string, options, defaultVals []string, unattended bool) ([]string, error) {
	if unattended {
		return defaultVals, nil
	}
	selected := append([]string(nil), defaultVals...)
	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o, o).Selected(sliceutil.Contains(defaultVals, o))
	}
	err := huh.NewMultiSelect[string]().
		Title(message).
		Options(opts...).
		Value(&selected).
		Run()
	if err != nil {
		return defaultVals, err
	}
	return selected, nil
}

// ConfirmBool asks for a bool. Returns defaultVal if unattended.
func ConfirmBool(message string, defaultVal, unattended bool) (bool, error) {
	if unattended {
		return defaultVal, nil
	}
	value := defaultVal
	err := huh.NewConfirm().
		Title(message).
		Value(&value).
		Run()
	return value, err
}
