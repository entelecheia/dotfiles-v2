package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/entelecheia/dotfiles-v2/internal/sliceutil"
)

// printSection prints a styled section header.
func printSection(title string) {
	fmt.Println()
	fmt.Println(StyleSection.Render("▸ " + title))
}

func printKV(key, value string) {
	if value == "" {
		value = lipgloss.NewStyle().Foreground(lipgloss.Color("#565F89")).Render("(unset)")
	} else {
		value = StyleValue.Render(value)
	}
	fmt.Printf("  %s  %s\n", StyleKey.Render(key+":"), value)
}

func formatBool(v bool) string {
	if v {
		return StyleSuccess.Render("✓") + " enabled"
	}
	return StyleHint.Render("✗ disabled")
}

// splitCaskList parses a whitespace-separated cask list into a clean,
// de-duplicated slice.
func splitCaskList(s string) []string {
	return sliceutil.Dedupe(strings.Fields(s))
}

func pickBackupChoice(path string, detectedDrive bool) string {
	if detectedDrive {
		return "drive (auto-detected)"
	}
	if strings.Contains(path, "/.local/share/dotfiles/") {
		return "local"
	}
	if path != "" {
		return "custom"
	}
	return "local"
}
