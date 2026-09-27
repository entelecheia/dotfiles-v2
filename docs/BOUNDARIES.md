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
- `~/.config/git/gitignore.global` — the global git excludes file
  (`core.excludesFile`), deployed whole by the `git` module from
  `internal/template/templates/git/gitignore.global`. The legacy
  `~/.config/git/ignore` it supersedes is removed by `dot apply`, and only
  when the file still matches the last managed content byte for byte; a
  locally edited copy is left alone
- `~/.config/shell/30-ai.sh`
- `~/.maru/settings.json` and `~/.maru/sites.json` only during explicit AI
  backup/restore operations
- the global AGENTS fan-out targets, one per registered tool:
  `~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`,
  `~/.kiro/steering/AGENTS.md`, `~/.kimi-code/AGENTS.md`,
  `~/.pi/agent/AGENTS.md`, `~/.qwen/AGENTS.md`, `~/.gemini/GEMINI.md`,
  `~/.copilot/copilot-instructions.md`, and `~/.aider.conf.md`. Each is
  the whole file, rendered from the agents SSOT; registering a tool adds
  a target here or the boundary test fails. Session transcripts
  (`~/.pi/agent/sessions/**`, `~/.qwen/projects/**/chats/**`) are
  read-only scan surfaces for the claude-mem transcript bridge and are
  never written

Dot-owned state trees:

- `~/.config/dotfiles/agents`: the shared agents instruction SSOT
  (`AGENTS.md`), written by `dot ai agents init|pull|author|edit` (edit
  scaffolds a missing SSOT), by `dot apply` (scaffolds it on a fresh
  machine), by `dot ai restore|import`, and by the instruction blocks of `dot ai
  coauthor-guard` and `dot ai memory install`. This dir is synced between
  machines, so it holds no machine state: every agents apply removes the
  legacy apply-state file (`.state.json`) from it, best-effort, after
  migrating the entries that match this machine's targets
- `~/.local/share/dotfiles`: dot's machine-local data tree, holding the
  append-only AI audit log (`ai/events.jsonl`, one record per `dot ai`
  mutation), the agents apply state (`agents/state.json`, the
  last-applied hash of each target on this machine), and the timestamped
  backup trees (`backup/agents*/<timestamp>/...`) taken before agents
  SSOT and target edits. The apply state is written by every agents
  apply: `dot ai agents apply`, `dot apply`, `dot ai coauthor-guard
  apply --apply-agents`, `dot ai memory install`, and `dot ai restore
  --reapply-agents`

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
  written by `dot ai memory install`; also the claude-mem database itself
  (`claude-mem.db`: `dot ai memory sync pull` and bidirectional `sync`
  import peer rows into it locally, and `dot ai memory sync --serve
  import` does the same when a peer pushes), the peer-sync bookkeeping
  (`sync-state.json`), and the scheduled sync log
  (`logs/claude-mem-sync.log`)
- `~/Library/LaunchAgents/com.dotfiles.claude-mem-bridge.plist` — the
  user LaunchAgent that keeps the claude-mem bridge alive, written and
  bootstrapped by `dot ai memory install` (macOS only)
- `~/Library/LaunchAgents/com.dotfiles.claude-mem-sync.plist` — the user
  LaunchAgent that runs `dot ai memory sync --peer <target>` hourly,
  written and bootstrapped by `dot ai memory install --peer` (macOS only)
- `~/.claude/plugins/marketplaces/thedotmack` — the claude-mem
  marketplace checkout, refreshed by `dot ai memory update` (via
  `claude plugin marketplace update`, or `git pull --ff-only` when the
  claude CLI is unavailable) and by `dot ai update`; the installed plugin
  cache under `~/.claude/plugins/cache/` and the codex plugin cache under
  `~/.codex/plugins/cache/` are updated through their owning CLIs
  (`claude plugin update`, `codex plugin remove/add`), never edited
  directly
- `~/Library/LaunchAgents/com.dotfiles.watchdog.reap.plist` — the user
  LaunchAgent that runs the watchdog reaper on its configured interval,
  written and loaded by `dot watchdog setup` and removed by
  `dot watchdog uninstall` (macOS only)
- `/Library/LaunchDaemons/com.dotfiles.watchdog.warp.plist` — the root
  LaunchDaemon that runs the WARP heal pass, installed with sudo by
  `dot watchdog setup` when `watchdog.warp` is enabled and removed by
  `dot watchdog uninstall` (macOS only). Trust note: the daemon executes
  the same user-installed `dot` binary (with `--home` pinned to the
  installing user). On the single-user Macs dot targets that user already
  holds sudo, so the user-writable binary is not a privilege boundary;
  hardening it would need a root-owned copy refreshed on every dot
  update, which is a documented non-goal for now. The kickstart target is
  re-validated against Cloudflare's WARP daemon label shape at run time,
  so a local edit of `warp.json` cannot redirect the root daemon at an
  arbitrary service
- `~/.config/monit/monitrc` — the monit control file rendered by
  `dot watchdog setup` when `watchdog.monit` is enabled (mode 0600, which
  monit requires of its control file), and removed by
  `dot watchdog uninstall` (macOS only)
- `~/Library/LaunchAgents/com.dotfiles.monit.plist` — the user LaunchAgent
  that runs monit in the foreground against that control file, written and
  loaded by `dot watchdog setup` and removed by `dot watchdog uninstall`
  (macOS only)
- `/Library/Application Support/dot/screensharing-heal` — the root-owned
  helper (mode 0755) that runs `launchctl kickstart -k
  system/com.apple.screensharing`; installed with sudo by
  `dot watchdog setup` when `watchdog.monit.screensharing` heals, exec'd by
  monit through passwordless sudo when the VNC handshake on 127.0.0.1:5900
  fails, and removed only by the interactive-only uninstall prompt. Root
  ownership is the security property: the monit agent runs as the user, so
  the file sudo executes must not be user-writable
- `/etc/sudoers.d/dot-watchdog-screensharing` — the sudoers drop-in (mode
  0440) granting the installing user passwordless exec of exactly that
  helper path and nothing else; validated with `visudo -c -f` before it
  goes live, and removed only by the interactive-only uninstall prompt
- `~/Library/Logs/dot/monit.log` — monit's own log, written by the monit
  agent (plus the unit's launchd stdout/stderr logs beside it)
- `~/.local/state/dot/watchdog/` — the watchdog machine state: the resolved
  config snapshot (`watchdog.yaml`) the scheduled reaper reads, the
  cross-run CPU history (`samples.json`), the WARP heal bookkeeping
  (`warp.json`: consecutive failures, reconnect attempts, last daemon
  restart, resolved WARP daemon label), and the pre-hardening power values
  (`power.json`) that uninstall restores from; written by
  `dot watchdog setup`, every `dot watchdog reap`/`dot watchdog warp`
  pass, and removed only by the interactive uninstall prompt
- `~/Library/Logs/dot/watchdog.log` — the watchdog JSON-lines event log
  (`dot watchdog log` tails it), plus the reaper unit's launchd
  stdout/stderr logs beside it
- the power-management keys `sleep` (charger profile), `autorestart`, and
  `womp` via `pmset`, and `restartfreeze` via `systemsetup` — written only
  by `dot watchdog setup --headless`, after the prior values are saved to
  `power.json`, and restored by `dot watchdog uninstall` under the
  interactive-only restore prompt. No other pmset/systemsetup key is
  touched.
- `~/.kimi-code/mcp.json` — the `claude-mem` entry under `mcpServers`
  (command and args only), written by `dot ai memory install`; every
  other server entry is preserved
- `~/.qwen/settings.json` — the `claude-mem` entry under `mcpServers`
  (command and args only), written by `dot ai memory install`; every
  other key — Qwen's model, provider, auth, and UI settings — is
  preserved. Notably NOT dotfiles-managed: `~/.qwen/.env` (secrets),
  `~/.qwen/projects/**/chats/**`, `~/.qwen/memories/**`, and
  `~/.qwen/usage_record.jsonl` are machine state dot neither backs up
  nor restores
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
