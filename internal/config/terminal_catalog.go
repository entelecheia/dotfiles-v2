package config

import "strings"

// TerminalAppOption describes a supported GUI terminal app.
type TerminalAppOption struct {
	Token  string
	Name   string
	Darwin bool
	Arch   bool
}

// TerminalToolOption describes a CLI tool backed by a Homebrew formula.
type TerminalToolOption struct {
	Formula string
	Name    string
}

var terminalAppOptions = []TerminalAppOption{
	{Token: "orca", Name: "Orca", Darwin: true, Arch: true},
	{Token: "warp", Name: "Warp", Darwin: true},
	{Token: "wave", Name: "Wave", Darwin: true},
	{Token: "cmux", Name: "cmux", Darwin: true},
	{Token: "iterm2", Name: "iTerm2", Darwin: true},
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
	{Formula: "oven-sh/bun/bun", Name: "Bun JavaScript runtime/toolkit"},
}

var defaultTerminalToolsByProfile = map[string][]string{
	"minimal": {"fzf", "ripgrep", "fd", "bat", "jq", "yq", "direnv", "zoxide", "eza"},
	"server":  {"fzf", "ripgrep", "fd", "bat", "jq", "yq", "direnv", "zoxide", "eza", "btop"},
	"full": {
		"fzf", "ripgrep", "fd", "bat", "jq", "yq", "direnv", "zoxide", "eza",
		"btop", "lazygit", "yazi", "glow", "csvlens", "chafa",
	},
}

// TerminalAppOptions returns the curated GUI terminal app catalog.
func TerminalAppOptions() []TerminalAppOption {
	return append([]TerminalAppOption(nil), terminalAppOptions...)
}

// TerminalAppOptionsForSystem returns terminal apps supported by the detected
// desktop platform. Other Linux distributions keep existing selections but do
// not offer an unsupported automatic installer.
func TerminalAppOptionsForSystem(system *SystemInfo) []TerminalAppOption {
	var options []TerminalAppOption
	for _, app := range terminalAppOptions {
		switch {
		case system != nil && system.OS == "darwin" && app.Darwin:
			options = append(options, app)
		case system != nil && system.IsArchLinux() && app.Arch:
			options = append(options, app)
		}
	}
	return options
}

// TerminalToolOptions returns the curated CLI terminal tool catalog.
func TerminalToolOptions() []TerminalToolOption {
	return append([]TerminalToolOption(nil), terminalToolOptions...)
}

// DefaultTerminalApps returns the profile's platform-supported default GUI
// terminal app selection.
func DefaultTerminalApps(profile string, system *SystemInfo) []string {
	if profile == "full" && len(TerminalAppOptionsForSystem(system)) > 0 {
		return []string{"orca"}
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
