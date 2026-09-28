# Maru / dotfiles-v2 Boundary

`dotfiles-v2` owns selected agent/tool installation orchestration, environment
and documented tool settings. Maru owns shared skill sources, registry,
canonical links and target deployment. Dot delegates selected skill sharing to
Maru's capability-checked CLI; it never copies or rewrites skill trees itself.
Native/plugin/system bundles remain owned by their native installers.

The selectable agent environments are Claude, Codex, Kimi, Qwen, Grok,
OpenCode, Genspark Gencode, pi and Antigravity. The automatic add-on catalog
is limited to ripwire, Open Code Review, GSD, claude-mem and ponytail.
Selection limits managed writes; it does not hide existing globally
discoverable skills from unselected agents. Gencode and pi install through
the managed npm prefix; Antigravity installs `agy` under `~/.local/bin` and
self-updates, so update operations defer rather than reinstall.

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
  `~/.copilot/copilot-instructions.md`, `~/.aider.conf.md`,
  `~/.grok/AGENTS.md`, and `~/.config/opencode/AGENTS.md`. Each is
  the whole file, rendered from the agents SSOT; registering a tool adds
  a target here or the boundary test fails. Session transcripts
  (`~/.pi/agent/sessions/**`, `~/.qwen/projects/**/chats/**`) are
  read-only scan surfaces for the claude-mem transcript bridge and are
  never written. Selected Codex and Kimi targets honor `CODEX_HOME` and
  `KIMI_CODE_HOME`; OpenCode honors `OPENCODE_CONFIG_DIR`, then
  `XDG_CONFIG_HOME/opencode`. Explicit `--home` isolates these destinations
  from ambient profile overrides.
- `~/.qwen/settings.json` — only the additive `context.fileName` entry required
  to discover AGENTS.md; preserve existing filenames and unrelated settings

Dot-owned state trees:

- `~/.local/share/dotfiles/ai/policy/`: private owned Claude launch overlays,
  preference ownership receipts and mutation locks. Only the overlay's
  `permissions.defaultMode`, exact knowledge-tool `permissions.allow`, model
  and effort fields are generated; native
  `~/.claude/settings.json` retains its original ownership limits. Apply and
  rollback refuse foreign edits and symlink paths. These are machine-local
  derivatives of portable `modules.ai.policy`, not shared policy sources.
- `$CODEX_HOME/dot-policy-*.config.toml` (or `~/.codex/` without an override):
  dedicated generated native profiles containing model, effort, approval,
  sandbox settings and per-tool approval leaves for registered Obsidian and
  claude-mem bindings. They do not register servers or copy command/URL/auth
  definitions. Only files recorded in the local policy ownership receipt
  may be replaced or removed. Existing `config.toml`, legacy inline profiles,
  MCP server definitions and authentication stay untouched. Explicit `--home`
  ignores ambient profile locations.
- `~/.local/share/dotfiles/ai/sessions/`: private session receipts and curated
  checkpoints. Receipts contain no raw prompts or model output. Native session
  history remains agent-owned. A managed process may write its normal native
  session history; dot never copies it between providers or machines.

- `~/.config/dotfiles/agents`: the shared agents instruction SSOT
  (`AGENTS.md`), written by `dot ai agents init|pull|author|edit` (edit
  scaffolds a missing SSOT), by `dot apply` (scaffolds it on a fresh
  machine), by `dot ai restore|import`, and by the instruction blocks of `dot ai
  coauthor-guard` and `dot ai memory install`, and the selected setup continuity
  policy block (backed up before upgrading an existing SSOT). This dir is synced between
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

- `~/.local/share/dotfiles/ai/handoffs/<canonical-git-common-dir-hash>.jsonl`
  — append-only local curated development handoffs shared across project
  worktrees. Records preserve producer claims, UTC time, commit/worktree and
  artifact digests. Show marks stale evidence for revalidation; neither command
  expands raw transcript collection or publishes records externally.
- `~/.local/share/dotfiles/ai/tooling-state.json` — private machine-local
  installation receipts: adopted provider/path/version/status/check time.
  Native provider files remain native-installer owned.
- `~/.local/share/dotfiles/ai/update-schedule.json`,
  `update-schedule.lock`, `update-schedule.out.log` and
  `update-schedule.err.log` in that directory — selected maintenance schedule
  state, nonblocking state-update lock and launchd run diagnostics
- `~/Library/LaunchAgents/com.dotfiles.ai.update.plist` — the macOS user
  stable-update job, owned by `dot ai update schedule enable|disable`
- `dot ai run`, the selected-tooling updates and `dot ai update schedule
  enable` hold their slots in the admission state root below (the same slots
  and pressure history as `dot admit`, #162). The former
  `/tmp/dotfiles-resource-<uid>/` store is no longer written.

Third-party files dot edits (each entry states what dot writes there and
under what condition; everything else in the file belongs to its owning
tool):

- `~/.config/git/config` — the `[hook "coauthor-guard"]` table
  (`command = ~/.config/git/hooks/commit-msg`, `event = commit-msg`),
  applies a warn or block mode and the installed git supports config-based
  hooks (2.54+). Configured hooks run in addition to `.git/hooks/*` and any
  repo-local `core.hooksPath`, so neither is touched; a `core.hooksPath`
  dot does not manage is left alone and is preserved across the git
  module's wholesale template rewrite of this file, while a leftover
  dot-managed one (`~/.config/git/hooks` from the pre-2.54 wiring) is
  removed as migration. dot never sets a global `core.hooksPath`. The same
  table is also rendered by the git module's `git/config.tmpl` when
  `modules.git.coauthor_guard` is set. No other table or key is touched.
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
- `~/Library/LaunchAgents/com.dotfiles.beszel-agent.plist` — the user
  LaunchAgent that runs the Beszel external-monitoring agent, sourcing the
  secrets-managed env file before exec'ing the brew-installed agent binary;
  written and loaded by `dot watchdog setup` when `watchdog.beszel` is
  enabled, and removed (the plist only, never the env file) by
  `dot watchdog uninstall` (macOS only)
- `~/.config/beszel/agent.env` — the Beszel agent env file (hub
  HUB_URL/KEY/TOKEN/LISTEN), secrets-managed: an age-encrypted archive ↔
  plaintext pair registered in the `dot secrets` entry table, so
  init/backup/restore cover it like the SSH key and shell secrets. Watchdog
  setup only probes its presence — it never reads, renders, or removes the
  file — and the beszel plist sources it at agent start
- `~/.local/state/dot/admission/` — the resource-admission controller state:
  the slot directories (`slots/`, one per repo plus the shared maintenance
  slot, each holding a `lease.json` with the owner pid/start, heartbeat and
  deadline), the cross-invocation pressure history (`history.json`), and the
  per-scope notify dedup marks (`notify/`); written by every `dot admit`
  gate evaluation, slot acquire/heartbeat/release, and defer notification,
  and by `dot ai run` and the tooling updates through the same controller.
  The root is the real user's (`$HOME`), never a `--home` or CODEX_HOME
  override, so one repository has one slot per user. Slots are removed by
  their owner's release or by stale-owner recovery; the history and notify
  marks are small JSON files with no scheduled cleanup
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
- `<workspace repo>/.git/index` and the repo's `HEAD` ref — moved
  together by `dot peer git realign --apply` through git's lockfile
  protocol (compare-and-swap `update-ref`, fast-forward only onto a
  strict descendant, the old value recorded in the reflog for undo).
  The worktree is never written by git; preview (`dot peer git status`,
  default `realign`, `--dry-run`) writes nothing; linked worktrees and
  locked, staged, conflicting or in-progress repositories are never
  touched.
- host files the operator lists under `host_merge` in the peer config
  (for example `~/.claude.json`), on both machines: before the additive
  host-path pass of a two-way `dot peer sync`, the entries of the listed
  top-level JSON keys from both copies are merged into the newer copy,
  which is written here (atomic rename, mode and owner kept, a symlink
  refused) and pushed to the peer with its mtime. Other keys are the newer copy's; an unlisted file is only
  ever copied whole, as before.

## dotfiles-v2 Must Not Write Directly

- anything under any tool skill root (`~/.claude/skills/**`,
  `~/.codex/skills/**`, `~/.agents/skills/**`, `~/.gemini/skills/**`,
  `~/.gemini/antigravity/skills/**`, `~/.kimi-code/skills/**`,
  `~/.qwen/skills/**`, `~/.grok/skills/**`, `~/.config/opencode/skills/**`,
  and resolved alternate profile skill roots)
- skill source directories under `~/.maru/skills/**` or any configured
  `modules.ai.skills.ssot_path`
- `~/.maru/env/**`

Skill directories may be scanned for diagnostics. Backups/restores do not copy
skills. Dot may invoke Maru for selected installable registry entries or a
selected add-on's native installer for its own bundle; those tools retain write
ownership. A missing Maru capability defers sharing, never triggers direct
copies. Do not overwrite foreign skills, implicitly retarget links or enroll a
generic skill source in automatic updates.

## Maru Owns

- `~/.maru/**` except the two portable settings files above
- `~/.maru/skills/registry.json`
- `~/.maru/skills/<name>` runtime symlinks
- owned skill symlinks for selected Claude, Codex, Kimi, Qwen, Grok and
  OpenCode profiles; native/plugin/system entries remain excluded
- source reconciliation, registry validation, and duplicate-tier policy

If this boundary changes, update the matching Maru boundary document and the
workspace rule at `~/workspace/work/_meta/rules/skills-ssot.md`.
