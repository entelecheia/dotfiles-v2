# Anchor / dotfiles-v2 Boundary

`dotfiles-v2` owns environment and AI tool settings. Anchor owns skill packages,
skill runtime state, and Claude/Codex skill symlink federation.

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

## dotfiles-v2 Must Not Write

- `~/.claude/skills/**`
- `~/.codex/skills/**`
- `~/.anchor/skills/**`
- `~/.anchor/env/**`

Skill directories may be scanned for diagnostics only. They must not be backed
up, restored, copied, deleted, or normalized by `dotfiles-v2`.

## Anchor Owns

- `~/.anchor/**`
- `~/.anchor/skills/registry.json`
- `~/.anchor/skills/<name>` runtime symlinks
- `~/.claude/skills/<name>` and `~/.codex/skills/<name>` skill symlinks created
  through Anchor install actions

If this boundary changes, update the matching Anchor boundary document and the
workspace rule at `~/workspace/work/_sys/rules/skills-ssot.md`.
