# Maru / dotfiles-v2 Boundary

`dotfiles-v2` owns environment and AI tool settings. The Maru app (its
`skill_host` module) owns skill sources, the skills registry, runtime symlinks,
and tool federation. `dotfiles-v2` never deploys skills; it provides read-only
diagnostics only (`dot ai skills list|validate|path|status`).
Maru-managed status/path diagnostics target Claude Code and Codex. Broader
agents, Gemini, and Antigravity roots remain inventory-only scan surfaces.

## dotfiles-v2 May Write

- `~/.claude/CLAUDE.md`
- `~/.claude/settings.json` (HUD statusLine block tagged `# dot-hud`, and only
  when dot owns the existing entry or `--force` is passed; `dot guard`
  PreToolUse hook entries tagged `# dot-guard`; entries owned by other tools
  are never touched). Notably NOT dotfiles-managed: `skillOverrides`,
  `permissions`, `enabledPlugins`, `hooks` outside the `# dot-guard` entries.
  Those belong to Claude Code's own UI (`/skills`, `/config`) or to whichever
  tool installed them.
- `~/.claude/settings.local.json` when explicitly included in auth/local flows
- `~/.claude/.dot-lock` — a PID lock directory serializing writers of
  `~/.claude/settings.json`, held only for the duration of a
  `claudecfg.Mutate` call and removed on every exit path; never taken on a
  read
- `~/.claude/statusline-dot.py`
- `~/.claude/keybindings.json`
- `~/.config/claude/**`
- `~/.config/shell/30-ai.sh`
- `~/.maru/settings.json` and `~/.maru/sites.json` only during explicit AI
  backup/restore operations
- global AGENTS fan-out targets for Claude, Codex, Cursor, Antigravity,
  Copilot, and Aider

Dot-owned state trees:

- `~/.config/dotfiles/agents` — the shared agents instruction SSOT
  (`AGENTS.md`) and its apply-state file (`.state.json`), written by
  `dot ai agents init|apply` and by the coauthor-guard instruction block
  (`dot ai coauthor-guard`)
- `~/.local/share/dotfiles` — dot's data tree: the append-only AI audit
  log (`ai/events.jsonl`, one record per `dot ai` mutation) and the
  timestamped backup trees (`backup/agents*/<timestamp>/…`) taken before
  agents SSOT and target edits

Third-party files dot edits (each entry states what dot writes there and
under what condition; everything else in the file belongs to its owning
tool):

- `~/.config/git/config` — the `hooksPath = ~/.config/git/hooks` line
  inside the `[core]` table, and only when `dot ai coauthor-guard`
  applies a warn or block mode; an existing `core.hooksPath` dot does not
  manage is a conflict that refuses the write unless `--force-hooks-path`
  is passed. No other table or key is touched.
- `~/.config/git/hooks/commit-msg` — the coauthor-guard hook script,
  written by `dot ai coauthor-guard` when the guard mode is warn or
  block
- `~/.claude-mem` — the cross-CLI transcript watch config and state files
  (`cross-cli-transcript-watch.json`,
  `cross-cli-transcript-watch-state.json`) and the bridge log directory,
  written by `dot ai memory install`
- `~/Library/LaunchAgents/com.dotfiles.claude-mem-bridge.plist` — the
  user LaunchAgent that keeps the claude-mem bridge alive, written and
  bootstrapped by `dot ai memory install` (macOS only)
- `~/.kimi-code/mcp.json` — the `claude-mem` entry under `mcpServers`
  (command and args only), written by `dot ai memory install`; every
  other server entry is preserved
- `~/.kiro/settings/mcp.json` — the `claude-mem` entry under
  `mcpServers` plus `"disabled": false`, written by `dot ai memory
  install`; every other server entry is preserved
- `~/.copilot/mcp-config.json` — the `claude-mem` entry under
  `mcpServers` plus `"type": "local"` and `"tools": ["*"]`, written by
  `dot ai memory install`; every other server entry is preserved
- `~/.codex/config.toml` — the `tui.status_line` setting, patched by
  `dot ai hud apply`; the write is an atomic rename because Codex
  rewrites this file continuously. No other key is touched.

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

- `~/.maru/**` except the two portable settings files above
- `~/.maru/skills/registry.json`
- `~/.maru/skills/<name>` runtime symlinks
- tool skill root federation (`~/.claude/skills/**`, `~/.codex/skills/**`, …)
- source reconciliation, registry validation, and duplicate-tier policy

If this boundary changes, update the matching Maru boundary document and the
workspace rule at `~/workspace/work/_meta/rules/skills-ssot.md`.
