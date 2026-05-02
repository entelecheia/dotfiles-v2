package config

import "strings"

// TerminalAppOption describes a GUI terminal app backed by a Homebrew cask.
type TerminalAppOption struct {
	Token string
	Name  string
}

// TerminalToolOption describes a CLI tool backed by a Homebrew formula.
type TerminalToolOption struct {
	Formula string
	Name    string
}

var terminalAppOptions = []TerminalAppOption{
	{Token: "warp", Name: "Warp"},
	{Token: "cmux", Name: "cmux"},
	{Token: "iterm2", Name: "iTerm2"},
}

var terminalToolOptions = []TerminalToolOption{
	{Formula: "fzf", Name: "fuzzy finder"},
	{Formula: "ripgrep", Name: "rg search"},
	{Formula: "fd", Name: "find alternative"},
	{Formula: "bat", Name: "cat alternative"},
	{Formula: "jq", Name: "JSON processor"},
	{Formula: "yq", Name: "YAML processor"},
	{Formula: "direnv", Name: "directory env loader"},
	{Formula: "zoxide", Name: "z/zi directory jumper"},
	{Formula: "eza", Name: "ls alternative"},
	{Formula: "btop", Name: "system monitor"},
	{Formula: "lazygit", Name: "git TUI"},
	{Formula: "yazi", Name: "file manager"},
	{Formula: "glow", Name: "markdown viewer"},
	{Formula: "csvlens", Name: "CSV viewer"},
	{Formula: "chafa", Name: "terminal image viewer"},
}

var defaultTerminalToolsByProfile = map[string][]string{
	"minimal": []string{"fzf", "ripgrep", "fd", "bat", "jq", "yq", "direnv", "zoxide", "eza"},
	"server":  []string{"fzf", "ripgrep", "fd", "bat", "jq", "yq", "direnv", "zoxide", "eza", "btop"},
	"full": []string{
		"fzf", "ripgrep", "fd", "bat", "jq", "yq", "direnv", "zoxide", "eza",
		"btop", "lazygit", "yazi", "glow", "csvlens", "chafa",
	},
}

// TerminalAppOptions returns the curated GUI terminal app catalog.
func TerminalAppOptions() []TerminalAppOption {
	return append([]TerminalAppOption(nil), terminalAppOptions...)
}

// TerminalToolOptions returns the curated CLI terminal tool catalog.
func TerminalToolOptions() []TerminalToolOption {
	return append([]TerminalToolOption(nil), terminalToolOptions...)
}

// DefaultTerminalApps returns the profile's default GUI terminal app selection.
func DefaultTerminalApps(profile string) []string {
	if profile == "full" {
		return []string{"warp"}
	}
	return nil
}

// DefaultTerminalTools returns the profile's default CLI terminal tool selection.
func DefaultTerminalTools(profile string) []string {
	tools := defaultTerminalToolsByProfile[profile]
	if len(tools) == 0 {
		tools = defaultTerminalToolsByProfile["full"]
	}
	return append([]string(nil), tools...)
}

// IsTerminalAppToken returns true when token is in the curated terminal app list.
func IsTerminalAppToken(token string) bool {
	for _, opt := range terminalAppOptions {
		if opt.Token == token {
			return true
		}
	}
	return false
}

// IsTerminalToolFormula returns true when formula is in the curated tool list.
func IsTerminalToolFormula(formula string) bool {
	for _, opt := range terminalToolOptions {
		if opt.Formula == formula {
			return true
		}
	}
	return false
}

// IsBrewToken accepts conservative Homebrew token syntax for free-form formulas.
func IsBrewToken(token string) bool {
	if strings.TrimSpace(token) != token || token == "" {
		return false
	}
	for _, r := range token {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '-', '_', '.', '+', '@', '/':
			continue
		default:
			return false
		}
	}
	return true
}
