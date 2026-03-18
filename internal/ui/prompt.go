package ui

import (
	"github.com/charmbracelet/huh"
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
func Select(message string, options []string, defaultVal string, unattended bool) (string, error) {
	if unattended {
		return defaultVal, nil
	}
	var selected string
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

// Input asks for text input. Returns defaultVal if unattended.
func Input(message, defaultVal string, unattended bool) (string, error) {
	if unattended {
		return defaultVal, nil
	}
	var value string
	err := huh.NewInput().
		Title(message).
		Value(&value).
		Placeholder(defaultVal).
		Run()
	if err != nil {
		return defaultVal, err
	}
	if value == "" {
		return defaultVal, nil
	}
	return value, nil
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
