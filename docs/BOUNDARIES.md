# Anchor / dotfiles-v2 Boundary

`dotfiles-v2` owns environment and AI tool settings. Anchor owns skill packages,
skill runtime state, and the default skills SSOT. When explicitly configured,
`dotfiles-v2` may deploy source-owned skills into tool-specific skill roots as
symlinks.

## dotfiles-v2 May Write

- `~/.claude/CLAUDE.md`
- `~/.claude/settings.json`
- `~/.claude/settings.local.json` when explicitly included in auth/local flows
- `~/.claude/hooks/**`
- `~/.claude/keybindings.json`
- `~/.config/claude/**`
- `~/.config/shell/30-ai.sh`
- global AGENTS fan-out targets for Claude, Codex, Cursor, Antigravity,
  Copilot, and Aider
- configured skill target symlinks under `~/.claude/skills/**`,
  `~/.codex/skills/**`, `~/.agents/skills/**`, `~/.gemini/skills/**`, and
  `~/.gemini/antigravity/skills/**` when `modules.ai.skills.enabled: true`

## dotfiles-v2 Must Not Write

- skill source directories under `~/.anchor/skills/**` or any configured
  `modules.ai.skills.ssot_path`
- non-symlink skill targets unless the user passes `dot ai skills apply --force`
  to back up and replace a conflict
- `~/.anchor/env/**`

Skill directories may be scanned for diagnostics. Backups/restores do not copy
skills; configured skills management only creates or repairs tool-facing
symlinks from the configured SSOT.

## Anchor Owns

- `~/.anchor/**`
- `~/.anchor/skills/registry.json`
- `~/.anchor/skills/<name>` runtime symlinks
- source reconciliation, registry validation, and duplicate-tier policy

If this boundary changes, update the matching Anchor boundary document and the
workspace rule at `~/workspace/work/_sys/rules/skills-ssot.md`.
