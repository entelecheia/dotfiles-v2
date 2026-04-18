package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
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

func sameSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
	}
	for _, c := range seen {
		if c != 0 {
			return false
		}
	}
	return true
}

func splitCaskList(s string) []string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
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

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
