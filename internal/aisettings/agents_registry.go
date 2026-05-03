package aisettings

// AgentTool describes one AI coding tool target that can receive the shared
// global AGENTS.md content.
type AgentTool struct {
	ID          string
	DisplayName string
	TargetPath  string
	OverlayFile string
	Optional    bool
}

// RegisteredAgentTools returns the built-in AI agents registry.
func RegisteredAgentTools() []AgentTool {
	return []AgentTool{
		{
			ID:          "claude",
			DisplayName: "Claude Code",
			TargetPath:  "~/.claude/CLAUDE.md",
			OverlayFile: "claude.md",
		},
		{
			ID:          "codex",
			DisplayName: "Codex CLI",
			TargetPath:  "~/.codex/AGENTS.md",
			OverlayFile: "codex.md",
		},
		{
			ID:          "cursor",
			DisplayName: "Cursor",
			TargetPath:  "~/.cursor/AGENTS.md",
			OverlayFile: "cursor.md",
		},
		{
			ID:          "gemini",
			DisplayName: "Gemini CLI",
			TargetPath:  "~/.gemini/GEMINI.md",
			OverlayFile: "gemini.md",
			Optional:    true,
		},
		{
			ID:          "copilot",
			DisplayName: "GitHub Copilot",
			TargetPath:  "~/.config/github-copilot/AGENTS.md",
			OverlayFile: "copilot.md",
			Optional:    true,
		},
		{
			ID:          "aider",
			DisplayName: "Aider",
			TargetPath:  "~/.aider.conf.md",
			OverlayFile: "aider.md",
			Optional:    true,
		},
	}
}
