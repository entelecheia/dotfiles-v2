# Maru / dotfiles-v2 Boundary

`dotfiles-v2` owns environment and AI tool settings. The Maru app (its
`skill_host` module) owns skill sources, the skills registry, runtime symlinks,
and tool federation. `dotfiles-v2` never deploys skills; it provides read-only
diagnostics only (`dot ai skills list|validate|path|status`).

## dotfiles-v2 May Write

- `~/.claude/CLAUDE.md`
- `~/.claude/settings.json` (HUD statusLine block; `dot guard` PreToolUse hook
  entries tagged `# dot-guard`; entries owned by other tools are never touched)
- `~/.claude/settings.local.json` when explicitly included in auth/local flows
- `~/.claude/hooks/**`
- `~/.claude/keybindings.json`
- `~/.config/claude/**`
- `~/.config/shell/30-ai.sh`
- global AGENTS fan-out targets for Claude, Codex, Cursor, Antigravity,
  Copilot, and Aider

## dotfiles-v2 Must Not Write

- anything under any tool skill root (`~/.claude/skills/**`,
  `~/.codex/skills/**`, `~/.agents/skills/**`, `~/.gemini/skills/**`,
  `~/.gemini/antigravity/skills/**`)
- skill source directories under `~/.maru/skills/**` or any configured
  `modules.ai.skills.ssot_path`
- `~/.maru/env/**`

Skill directories may be scanned for diagnostics. Backups/restores do not copy
skills.

## Maru Owns

- `~/.maru/**`
- `~/.maru/skills/registry.json`
- `~/.maru/skills/<name>` runtime symlinks
- tool skill root federation (`~/.claude/skills/**`, `~/.codex/skills/**`, …)
- source reconciliation, registry validation, and duplicate-tier policy

If this boundary changes, update the matching Maru boundary document and the
workspace rule at `~/workspace/work/_meta/rules/skills-ssot.md`.
