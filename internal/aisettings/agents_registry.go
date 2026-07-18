package aisettings

// AgentTool describes one AI coding tool target that can receive the shared
// global AGENTS.md content.
type AgentTool struct {
	ID          string
	DisplayName string
	TargetPath  string
	OverlayFile string
	Aliases     []string
	Optional    bool
}

// RegisteredAgentTools returns the built-in AI agents registry.
func RegisteredAgentTools() []AgentTool {
	// Keep this registry to instruction/settings targets only. Skill target
	// symlinks are handled by SkillsManager when modules.ai.skills is enabled.
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
			ID:          "kiro",
			DisplayName: "Kiro CLI",
			TargetPath:  "~/.kiro/steering/AGENTS.md",
			OverlayFile: "kiro.md",
		},
		{
			ID:          "kimi",
			DisplayName: "Kimi Code CLI",
			TargetPath:  "~/.kimi-code/AGENTS.md",
			OverlayFile: "kimi.md",
			Optional:    true,
		},
		{
			ID:          "antigravity",
			DisplayName: "Antigravity CLI",
			TargetPath:  "~/.gemini/GEMINI.md",
			OverlayFile: "antigravity.md",
			Aliases:     []string{"gemini"},
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
