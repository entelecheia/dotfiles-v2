# dotfiles-v2

[![Test](https://github.com/entelecheia/dotfiles-v2/actions/workflows/test.yaml/badge.svg)](https://github.com/entelecheia/dotfiles-v2/actions/workflows/test.yaml)
[![Release](https://github.com/entelecheia/dotfiles-v2/actions/workflows/release.yaml/badge.svg)](https://github.com/entelecheia/dotfiles-v2/actions/workflows/release.yaml)

Declarative user environment management + AI-powered tmux workspace manager.
A single Go binary. macOS + Linux + GPU servers. Modular, profile-based, AI-ready.

---

## Quick Start

### Install via Homebrew (recommended on macOS / Linuxbrew)

```bash
brew tap entelecheia/tap
brew trust entelecheia/tap
brew install dotfiles
```

Provides the `dot` binary and the `dotfiles` back-compat symlink.

`brew trust` is what lets Homebrew load a formula from a non-official tap. On
Homebrew 6 with `HOMEBREW_REQUIRE_TAP_TRUST` set, tapping alone is not enough;
the install stops with `Refusing to load formula entelecheia/tap/dotfiles from
untrusted tap`. It is a no-op on older Homebrew and safe to run twice.

Taps that `dot apply` adds for you (`staixbwlb/cask`, `manaflow-ai/cmux`,
`stablyai/orca`) are trusted automatically as part of the run, so this is only
needed for the bootstrap tap that installs `dot` itself.

### Install via curl (fallback)

```bash
curl -fsSL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/install.sh | bash
```

Use this when Homebrew isn't available or you want the bootstrap to install it for you. The installer handles prerequisites automatically:
- **macOS**: Installs Homebrew (which includes Xcode Command Line Tools)
- **Linux**: Installs Linuxbrew for consistent package management
- Downloads the `dot` binary and configures PATH

### Setup

```bash
dot            # welcome screen with next-step guidance
dot init       # interactive TUI — name, email, profile, modules
dot apply      # apply all enabled modules
dot usecase    # detailed workflow examples
```

### Migrate from another machine

**Option A — one-stop wizard (recommended):**

```bash
# On the existing machine — one interactive run backs up everything:
# profile state, macOS app settings, AI/Maru settings, encrypted secrets
dot backup

# On the new machine (Drive already mounted)
dot restore                           # pick the source host, restore in safe order
```

`dot backup` confirms the backup root, lets you pick domains
(profile/apps/ai/secrets), asks about age keys and AI auth tokens, and
stamps profile + AI snapshots with one shared tag. `dot restore` supports
cross-host restore (any machine that backed up into the same root),
optionally runs `dot apply` after the profile restore, and preserves every
overwritten local file in per-step pre-restore backups. Unattended:
`dot backup --yes --scope profile,ai,secrets` / `dot restore --yes --host <src>`.

**Option B — individual commands:**

```bash
# On the existing machine
dot profile backup --tag "pre-migration" --include-secrets
dot apps backup                       # also snapshot per-app settings
dot ai backup                         # portable Claude/Codex/Antigravity/Maru/MCP settings

# On the new machine (Drive already mounted)
dot profile restore --include-secrets # restores ~/.config/dotfiles + ~/.ssh/age_key*
dot apply                             # brew formulas + casks from install list
dot apps restore                      # plists, Application Support, containers
dot ai restore                        # Claude/Codex/Antigravity/MCP settings
```

The shared backup root lives in a single cloud folder
(`<cloud>/secrets/dotfiles-backup` by default) and holds every snapshot the
user has taken across machines. Auto-detection prefers **Dropbox**
(`~/Library/CloudStorage/Dropbox` or `~/Dropbox`) and falls back to Google
Drive — both gated on a `secrets/` marker folder; override anytime with
`dot profile root <path>`. `dot secrets backup` (no argument) and the
workspace sync target default follow the same detected cloud root. `profile list` shows
every version, and
`profile restore --version <id>` rolls back to any specific one.

**Option C — plain YAML export:**

```bash
# On the existing machine — export config
dot config export ~/workspace/secrets/dotfiles-config.yaml

# On the new machine — import and apply
dot init --from ~/workspace/secrets/dotfiles-config.yaml
dot apply
# → gh auth login (if private repos configured)
# → git clone work/vault repos
# → symlink federation, shell config, packages...
```

### Workspace

```bash
dot open myproject   # launch or resume a multi-panel tmux workspace
dot open myproject   # SSH dropped? just run it again — resumes exactly
```

When the workspace module is enabled, the rendered shell environment also
exports the shared AI scratch contract:

```bash
MARU_SCRATCHPAD="$WORK/scratchpad"
MARU_TEMP="$MARU_SCRATCHPAD/temp"
CLAUDE_CODE_TMPDIR="$MARU_TEMP/runtime/claude"
```

Maru and external AI CLIs therefore resolve the same temporary-artifact root.

### Build from source

```bash
git clone https://github.com/entelecheia/dotfiles-v2.git && cd dotfiles-v2
make build          # → bin/dot
make install        # → ~/.local/bin/dot + ~/.local/bin/dotfiles (symlink)
```

---

## Commands

> `dot` is the canonical command; `dotfiles` remains a back-compat alias.
> Run `dot` with no arguments for a welcome screen; `dot usecase` for detailed workflows.

### `dot` (no args) — welcome screen

Prints a friendly getting-started guide. Detects whether you've configured dot and shows next steps. Pass no arguments to any invocation of `dot` to see it.

### `dot usecase`

Walk through 14 detailed workflows: first-time setup, safe apply, daily workspace, sync, backup/restore, updates, GPU server, troubleshooting.

```bash
dot usecase
```

### `dot init`

Interactive TUI setup. Collects user info and saves to `~/.config/dotfiles/config.yaml`.

```bash
dot init                                              # fresh setup
dot init --from ~/workspace/secrets/config.yaml    # import from another machine
dot init --yes                                        # unattended with defaults
```

Use `--from` to import settings from another machine's exported config. Identity fields (name, email, SSH key) are pre-populated; machine-specific settings (workspace path, terminal) are confirmed interactively.

Prompts for:
- Name, Email, GitHub username
- Timezone (default: `Asia/Seoul`)
- Profile (`minimal` / `full` / `server`)
- GPU/CUDA auto-detection (suggests `server` when NVIDIA GPU detected)
- Prompt style (`minimal` / `rich`) — see below
- Terminal app choices (`orca`, `warp`, `wave`, `cmux`, `iterm2`) on macOS;
  Orca selection on Arch Linux
- Module opt-ins: workspace, AI CLI/config helpers, fonts
- SSH key name (auto-derived from GitHub username)
- Workspace git repos: remote URLs for `work` and `vault` directories (optional)
- Vault location: selectable, auto-detected from existing `<workspace>/work/vault` (e.g. a work submodule) or `<workspace>/vault`, with a fresh default of `<workspace>/work/vault`. When the vault is not at `<workspace>/vault`, the separate vault repo entry is skipped (it arrives with work)
- GitHub authentication via `gh auth login` with broad scopes (optional, for private repos)

### `dot apply`

Apply configuration to the system. Runs each enabled module in order.

```bash
dot apply                          # interactive
dot apply --yes                    # unattended (skip prompts)
dot apply --dry-run                # preview only
dot apply --profile minimal        # override profile
dot apply --module shell --module git  # specific modules
```

#### Safe Apply

```bash
dot preflight --check-only   # 1. scan environment (no changes)
dot preflight                # 2. generate config from detected env
dot check                    # 3. show modules with pending changes
dot apply --dry-run          # 4. preview all changes
dot apply --module shell     # 5. apply selectively
```

> Files are backed up to `~/.local/share/dotfiles/backup/` before overwrite. Identical content (SHA256) is never overwritten.

### `dot check`

Compare current system state against desired profile. No changes made.

```bash
dot check
dot check --profile full
```

```
MODULE          STATUS     CHANGES
packages        OK         0 change(s)
shell           PENDING    3 change(s)
  → write ~/.config/shell/00-exports.sh
  → write ~/.config/shell/50-tools-init.sh
  → download/refresh oh-my-zsh
git             OK         0 change(s)
```

### `dot diff`

Preview pending changes with detailed descriptions.

```bash
dot diff
dot diff --module git
```

### `dot update`

Self-updating binary. Downloads the latest release from GitHub, verifies its
sha256 against the release's `checksums.txt` (refusing to install when the
checksum file is missing or the hash mismatches), sanity-checks the new
binary, then replaces the current one atomically. (`upgrade` is an alias.)

```bash
dot update          # download, verify & install
dot update --check  # check only
```

### `dot config`

Show current configuration (profile, system, modules, packages).

```bash
dot config
dot config export                                       # print to stdout
dot config export ~/workspace/secrets/config.yaml    # save to file
```

`config export` produces a portable YAML file that can be used on another machine with `dot init --from <file>`.

### `dot reconfigure`

Re-run init prompts with current values as defaults, then optionally re-apply.

```bash
dot reconfigure
```

### `dot secrets`

Manage age-encrypted secrets (SSH keys, shell secrets).

```bash
dot secrets init              # encrypt SSH key + shell secrets
dot secrets init --scaffold   # also create empty 90-secrets.sh (0600) if missing
dot secrets backup <dir>      # copy .age files to backup dir
dot secrets restore <dir>     # decrypt from backup
dot secrets status            # check decrypted + encrypted files
dot secrets list              # list encrypted files
```

Restore is clobber-safe: each file is decrypted to a 0600 temp file and
renamed into place atomically, so a failed decrypt never corrupts an existing
key. When a target exists with different content, restore prompts (auto-accepts
under `--yes`) and saves the old version to `<dest>.bak-<timestamp>` first;
identical content is reported as unchanged.

Encryption flow:
```
~/.ssh/id_ed25519_user         → age -e → ~/.local/share/dotfiles-secrets/id_ed25519_user.age
~/.config/shell/90-secrets.sh  → age -e → ~/.local/share/dotfiles-secrets/90-secrets.sh.age
```

### `dot backup` / `dot restore`

One-stop cross-machine backup and restore for profile state, macOS app
settings, AI/agent settings, and encrypted secrets.

```bash
dot backup                              # interactive backup wizard
dot backup --yes --scope profile,ai,secrets
dot restore                             # interactive restore wizard
dot restore --yes --host <src>
dot restore --version <snapshot-id>     # pin profile/AI snapshots
```

Backup stores snapshots under the shared backup root. Restore preserves
overwritten local files in per-domain pre-restore backups and can optionally
run `dot apply` after restoring profile state.

### `dot status`

Unified dashboard showing system, user, modules, secrets, sync, and workspace status at a glance.

```bash
dot status
```

### `dot clean`

Remove junk directories that waste disk space and cause cloud sync problems. The `_sys/` subtree is always protected.

```bash
dot clean                # scan and preview (no deletion)
dot clean --yes          # actually delete
dot clean --all --yes    # include risky patterns (dist/, build/, out/, target/)
dot clean ~/projects     # custom path (default: ~/workspace/work)
```

**Safe patterns** (always scanned): `node_modules`, `__pycache__`, `.pytest_cache`, `.mypy_cache`, `.ruff_cache`, `.venv`, `venv`, `env` (with pyvenv.cfg), `.next`, `.cache`, `.DS_Store`

**Risky patterns** (`--all` required): `dist`, `build`, `out`, `target`

> Alias: `dot gc`

### `dot tunnel`

Configure SSH access to this Mac through a locally managed Cloudflare Tunnel.
Server setup is macOS-only and installs a LaunchDaemon; client config is
cross-platform and only writes `~/.ssh/config.d/dot-tunnel`.

```bash
dot tunnel setup          # guide-style server setup for this Mac
dot tunnel setup --dry-run
dot tunnel status         # daemon, config, port 22, connector status
dot tunnel log [N]        # tail /Library/Logs/com.dotfiles.cloudflared.err.log
dot tunnel uninstall      # remove daemon, optionally config/credentials/tunnel

dot tunnel client add mac.example.com
dot tunnel client list
dot tunnel client remove mac.example.com
ssh user@mac.example.com
```

`setup` uses a local Cloudflare Tunnel (`cloudflared tunnel create/list/route
dns`) and renders its own plist at
`/Library/LaunchDaemons/com.dotfiles.cloudflared.plist` with explicit
`--config /etc/cloudflared/config.yml tunnel run` arguments. This avoids the
macOS `cloudflared service install` local-tunnel issue where the service plist
omits `tunnel run`.

Cloudflare Access app creation stays manual: after setup, create a Self-hosted
Access app for the SSH hostname in the Cloudflare dashboard, add an Allow
policy, then run `dot tunnel client add <hostname>` on client machines.

### `dot guard`

Claude Code safety hooks, ported from gstack's careful/freeze skills. Two
protections run as a single PreToolUse hook backed by the dot binary itself:

- **careful**: warns (permission prompt) before destructive shell commands:
  `rm -rf`, SQL `DROP`/`TRUNCATE`, `git push --force`, `git reset --hard`,
  `git checkout/restore .`, `kubectl delete`, `docker rm -f`/`system prune`.
  Recursive deletes of pure build artifacts (`node_modules`, `dist`, `.next`,
  `__pycache__`, `.cache`, `build`, `.turbo`, `coverage`) pass without a prompt.
- **freeze**: denies `Edit`/`Write`/`NotebookEdit` outside a chosen directory.
  Temp dirs and `~/.claude/plans` stay writable so planning and scratch work
  survive a frozen session.

```bash
dot guard enable          # register hooks in ~/.claude/settings.json + careful on
dot guard freeze ./src    # deny file edits outside ./src (immediate)
dot guard unfreeze        # clear the boundary (hooks stay registered)
dot guard status          # registration, careful/freeze state, binary health
dot guard disable         # remove dot-guard entries; other hooks untouched
```

Mechanics and honesty notes:

- Hook entries are tagged with a trailing `# dot-guard` marker, so enable/
  disable only ever touch dot's own entries; hooks owned by other tools are
  preserved semantically (a rewrite sorts JSON keys alphabetically, same as
  `dot ai hud apply`; values are never altered).
- `enable`/`disable` edit `settings.json`, which Claude Code snapshots at
  session start: they affect **new** sessions only. `freeze`/`unfreeze` write
  dot state that the hook reads live, so they apply **immediately**.
- The hook fails open by construction: if the dot binary is missing or moved,
  Claude Code treats the hook error as non-blocking and tool calls proceed
  unprotected. `dot guard status` flags this. Rerun `dot guard enable` after
  moving the binary.
- Guard is a guardrail, not a sandbox: careful only inspects the Bash tool
  (an agent can still write files via `sed`/`tee`), and freeze does not
  constrain shell commands. Fire logging records the matched pattern name
  only, never command content.

### `dot sync`

> The unified successor of the old SSH-only `dot sync` and the mirror-only
> `dot gsync`. Legacy aliases `dot gsync` / `dot gdrive-sync` forward here
> with a deprecation note.

Workspace sync via rsync to a configurable **target** - either a local
cloud-client folder (`local:~/Dropbox/work`; the client handles the cloud
round-trip) or an SSH remote (`ssh:user@host:path`). Replaces app-based
mirror tools (e.g. ChronoSync): git-aware filtering, no `.git` leakage,
previewed pushes, conflict backups.

**Git-aware union filter.** The sync set is:

> (every file tracked by the workspace's **root** Git repo)
> ∪ (untracked files matching the binary-extension allowlist)
> − (submodules, junk, deny-by-default secrets)

- **Submodules never sync here** - they sync through Git itself. Paths come
  from `.gitmodules` per run (`submodules.dyn.conf`), so adding/removing a
  submodule needs no filter edits.
- **Secrets are deny-by-default**: `/.secrets`, `/.maru/secrets`,
  `_sys/mcp.local.json`, `.env`, `.env.*` never sync unless a pattern in
  `allow.txt` explicitly re-includes them (`.env.example/sample/template`
  are always fine). The operator may already keep env files and tokens in
  the cloud mirror and the private Git remote - allow.txt records that
  acceptance explicitly, and `status` warns whenever allows are active.
- **Delete propagation**: the include layer is (tracked ∪ baseline keys), so
  a tracked file deleted locally still matches an include rule and
  `--delete-after` can remove the target copy; the entry ages out at the
  next baseline refresh.

The Git-tracked `<workspace>/.dotfiles/sync/baseline.manifest` is the shared
payload index: `pull` restores files listed there from a local mirror, while
baseline-unknown mirror files are staged by `intake` into
`<workspace>/inbox/gdrive/<intake-ts>/` for manual routing. Deletes remain
non-destructive by default.

```bash
dot sync init                # one-time: create <workspace>/.dotfiles/sync/ (migrates .dotfiles/gdrive-sync/)
dot sync target local:~/Dropbox/work      # local cloud folder target
dot sync target ssh:user@host:~/work      # SSH remote target
dot sync setup               # rsync check + disable managed schedulers by default
dot sync setup --push-interval=10m --push-mode=clean
dot sync setup --pull-interval=15m --pull-mode=force

dot sync push                             # preview workspace → target, then confirm
dot sync push --mode=clean                # auto-push only if no conflicts
dot sync push --mode=force                # auto-push, backing up overwritten target files
dot sync push --propagate=create,update,delete  # include deletes
dot sync pull                             # baseline-tracked payload restore (local targets)
dot sync pull --strict                    # hash every baseline entry
dot sync intake                           # stage new mirror-origin files (local targets)
dot sync fetch <path>...                  # on-demand restore of specific files/folders
                                          # (programmatic entry point for tools that need
                                          #  the binaries behind a path; --update + backups,
                                          #  exclude layers still apply)

dot sync filters show                     # print the ordered filter layer chain
dot sync filters reset                    # regenerate exclude/include from templates (backups kept)

dot sync inbox                            # show staging + manifest counters
dot sync status                           # target, filters, secrets allows, schedulers, last runs
dot sync conflicts / conflicts prune      # inspect / prune .sync-conflicts backups
dot sync pause / resume                   # paused gate for all operations
dot sync shared                           # manual shared-folder exclusions
```

**SSH targets** push and pull with the same union filters but without plan
previews or intake (the remote tree is not walkable): push is direct rsync
with backups; pull is direct `rsync --update`. The baseline is refreshed
from the local payload set after each push so delete propagation still
works. Preflight verifies SSH reachability. For pull-then-push automation,
enable both scheduler units.

**Per-workspace store** at `<workspace>/.dotfiles/sync/` is the authoritative config + state location (a pre-rename `.dotfiles/gdrive-sync/` store is migrated automatically, including the `.gitignore` block):

| File | Purpose |
|------|---------|
| `config.yaml` | machine-local target, filter_mode, propagation policy, opt-in intervals/modes, max_delete, shared_excludes |
| `state.yaml` | machine-local last_pull / last_push / last_intake / last_intake_ts_dir |
| `include.txt` | editable binary-extension allowlist for untracked payloads (case-insensitive) |
| `exclude.txt` | editable static junk excludes (writable copy of embedded baseline; `filters reset` regenerates) |
| `ignore.txt` | user-supplied ignore patterns (additive layer; workspace policy excludes live here) |
| `allow.txt` | **explicit secrets opt-in** (re-included ahead of the deny-by-default secrets layer; never touched by `filters reset`) |
| `submodules.dyn.conf` | auto-generated per-run submodule excludes (from `.gitmodules`) |
| `tracked-includes.dyn.conf` | auto-generated per-run include layer: git-tracked relpaths ∪ baseline keys |
| `shared-excludes.dyn.conf` | auto-generated per-run manual shared-folder excludes |
| `baseline.manifest` | **Git-tracked** payload index (relpath → strict fingerprint), now including tracked files |
| `imports.manifest` | machine-local mirror-origin files already imported (relpath → fingerprint + imported-at) |
| `tombstones.log` | machine-local mirror deletions detected (record-only, never propagated locally) |
| `log/sync.log` | rotated sync log |

The legacy global `~/.config/dotfiles/config.yaml modules.gdrive_sync` block is consulted **once** to migrate values into this store on first invocation, then ignored. `init` appends a workspace `.gitignore` block that keeps machine-local state ignored while allowing `baseline.manifest`, `exclude.txt`, `ignore.txt`, and `allow.txt` to be committed.

**Filter strategy** defaults to the git-aware union (include mode):

| Mode | Behavior |
|------|----------|
| `include` (default) | Sync (root-repo git-tracked files ∪ baseline keys ∪ case-insensitive `include.txt` matches), minus submodules, secrets (unless allowed), `exclude.txt`, `ignore.txt`, shared-folder excludes, symlinks, and always-on state paths. Anything unanticipated does NOT sync. |
| `exclude` | Broad mode: sync everything except submodules, secrets (unless allowed), `exclude.txt`, `ignore.txt`, shared-folder excludes, symlinks, and always-on state paths. |

Default include patterns cover archives, audio, video, images, documents/decks/sheets, and data/ML artifacts (`*.parquet`, `*.pkl`, `*.pt`, `*.pth`, `*.tsv`); see `include.txt`.

**Propagation policy** maps to rsync flags:

| Create | Update | Delete | rsync flags appended |
|--------|--------|--------|----------------------|
| ✓ | ✓ | ✗ (default) | (none — natural copy-new-and-modified) |
| ✓ | ✓ | ✓ | `--delete-after --max-delete=N` |
| ✓ | ✗ | ✗ | `--ignore-existing` |
| ✗ | ✓ | ✗ | `--existing` |
| ✗ | ✗ | ✓ | `--existing --ignore-existing --delete-after --max-delete=N` |
| ✗ | ✗ | ✗ | refused — `must list at least one of create,update,delete` |

**Manual-by-default modes.**

| Mode | Behavior |
|------|----------|
| `manual` (default) | Show affected folders/files and conflicts, then ask before applying. |
| `clean` | Non-interactive; apply only when the plan has no conflicts. |
| `force` | Non-interactive; apply even with conflicts, backing up overwritten destination files. |

**Pull + intake algorithm.**

1. `pull` reads Git-shared `baseline.manifest`, prints a plan, and only applies after `manual` confirmation or an automatic `clean`/`force` mode. If local and Drive both diverged, `clean` aborts; `force` overwrites local with Drive and backs up the local file under `.sync-conflicts/<ts>/from-workspace/`.
2. `intake` scans mirror files not present in `baseline.manifest`. It does not run tracked pull; changed baseline-tracked files are skipped and left for `dot sync pull`. Git-tracked files are also skipped: the mirror is never authoritative for them (the next push overwrites mirror-side edits with a backup).
3. If a baseline-unknown file fingerprint matches `imports.manifest` → skip (already imported; idempotent even after operator moves it out of staging).
4. Else → copy into `<workspace>/inbox/gdrive/<microsecond-timestamp>/<relpath>` preserving subtree, and append to `imports.manifest`.

Files in baseline that are missing from mirror become tombstones — recorded in `tombstones.log`, never propagated as local deletions. Use `dot sync inbox forget <relpath>` to revoke an imports entry and force a re-intake for baseline-unknown files.

**Key features:**
- **Full source mirror, Git untouched**: tracked text and untracked binaries both reach the target, `.git` never does (hardcoded exclude, dir or gitlink). Submodules stay Git-only.
- **Secrets opt-in, recorded**: the deny-by-default layer plus `allow.txt` makes every secret exposure an explicit, versionable decision; `status` and `filters show` surface active allows.
- **Preview-first push/pull**: `push` and `pull` show file lists and conflict status before applying. Direct commands default to `--mode=manual`; automation must opt into `clean` or `force`.
- **Push-first for local artifacts**: `push` propagates workspace artifact state to mirror under the propagation policy. Default policy never deletes — operator opts into delete via `--propagate=...,delete` or by flipping `propagation.delete` in `config.yaml`.
- **Always-on excludes**: `<workspace>/.dotfiles/` and `<workspace>/inbox/gdrive/` are anchored-excluded from rsync passes so the per-workspace store and intake staging area never round-trip to mirror — regardless of operator filters.
- **Post-push baseline refresh**: a successful push rebuilds `baseline.manifest` from files present on both local and mirror, excluding Git-tracked files and using sha256 fingerprints for stable cross-machine diffs.
- **Opt-in schedulers**: `setup` installs no automatic sync by default and removes managed scheduler units (including legacy `com.dotfiles.gdrive-sync*` and `com.dotfiles.workspace-sync`). Pass `--push-interval=DUR --push-mode=clean|force` and/or `--pull-interval=DUR --pull-mode=clean|force` to enable automatic push or pull.
- **Safety filters**: submodule excludes, the secrets layer, `exclude.txt`, `ignore.txt`, manual shared-folder excludes, `--no-links`, `.dotfiles/`, and `inbox/gdrive/` are applied before include matching. `.gitignore` is intentionally not used as a sync filter because gitignored binaries are a primary sync payload.
- **Pause gate**: when `Paused=true`, sync operations refuse to run until `resume` clears it.
- **Shared-drive refusal**: refuses to sync if `mirror_path` resolves under a Drive `Shared drives/` root — workspace-authoritative semantics would propagate deletions into a team drive.
- **No empty mirror dirs**: push runs with rsync `--prune-empty-dirs`, so directories whose contents are entirely filtered out (or that are empty in the workspace) are not created on the mirror. Drop a `.gitkeep` if a placeholder dir must round-trip.
- **Conflict capture**: force/manual-confirmed pull conflicts back up overwritten local files under `.sync-conflicts/<RFC3339-ts>/from-workspace/`; push-side overwrites are backed up under the mirror's `.sync-conflicts/<RFC3339-ts>/from-workspace/`.
- **Safety cap**: `--max-delete=1000` (configurable) aborts runaway push deletions when delete propagation is on.
- **Stale-aware lock**: PID file inside `~/Library/Caches/dotfiles/sync.lock`; signal-0 probes detect crashed-process locks.

> Automatic sync is disabled by default. Scheduler units are `com.dotfiles.sync` (push) and `com.dotfiles.sync-intake` (pull) on macOS, `dotfiles-sync.*` / `dotfiles-sync-intake.*` on Linux.

**Profiles.** `--profile=<name>` selects an independent store under
`<workspace>/.dotfiles/<name>/` - its own target, filter files, baseline, lock
and scheduler unit. The default profile is `sync` (the cloud mirror) and behaves
exactly as before. A non-default profile is machine-local: the managed
`.gitignore` block ignores `/.dotfiles/*` and whitelists only `sync/`, so its
baseline never enters Git. That matters because two machines writing one shared
baseline would produce merge conflicts in the very file that coordinates them.

**Single writer.** A profile may declare an owner; `push` refuses on any other
machine. The cloud mirror is not a merge point - each machine keeps its own
baseline and delete propagation is on, so two pushers take turns undoing each
other.

```bash
dot sync owner                       # who may push this profile
dot sync owner --set-self            # claim it for this machine
dot sync owner --set <machine-name>  # hand it to another
dot sync owner --clear               # remove the restriction
```

### `dot peer`

Machine-to-machine workspace sync over SSH, so either machine can continue the
same work. A sibling of `dot sync`, not a replacement - they answer different
questions:

| | `dot sync` | `dot peer` |
|---|---|---|
| direction | workspace to cloud mirror | both ways with another machine |
| writers | exactly one | both |
| secrets | excluded | included (own `allow.txt`) |
| submodule working trees | excluded (they travel via Git) | included |
| deletes | propagated if configured | never |
| purpose | read the workspace from a phone | keep two machines interchangeable |

Peer sync includes secrets because a peer is a machine you already trust with
the same work, and the workspace does not run there without its env files. It
includes submodule working trees because uncommitted work inside a submodule is
precisely what Git has not seen. It never deletes: deletion is the one
irreversible operation and there is no shared history to justify it, so a file
removed on one machine simply stops being sent.

```bash
dot peer init --host user@host        # write the peer profile
dot peer doctor                       # check before transferring
dot peer sync [--dry-run]             # pull, push, then host paths
dot peer diff                         # paths that changed on BOTH machines
dot peer setup --interval=15m         # schedule it (--off to remove)
```

**Addressing a machine that moves.** Use a hostname that survives a network
change. A Tailscale MagicDNS name (`<machine>.<tailnet>.ts.net`) works from any
network with no static IP and no inbound port.

**Host paths.** A second rsync pass carries the files outside the workspace that
it still needs - `~/.ssh`, `~/.claude.json`, `~/.config/dotfiles` and friends -
listed in `<workspace>/.dotfiles/peer/home-paths.txt`. Deliberately excluded, in
code: `known_hosts` (per-machine trust; overwriting it deletes the host key for
the channel the sync runs over), `~/.maru/env` (venv console scripts bake an
absolute interpreter path - rebuild, never copy), plugin caches, and
`~/.codex/auth.json`. Keychain-backed tokens cannot be moved by any file copy at
all, and cannot even be verified over SSH.

**Conflicts.** Every overwrite is quarantined, so a path edited on both machines
keeps both versions. When timestamps do not order cleanly rsync skips it in both
directions - nothing is lost, but the machines quietly stop agreeing, which is
what `dot peer diff` surfaces.

**Offline peers.** An unreachable peer exits 0. That is what makes the scheduled
job safe on a laptop that comes and goes.

> Scheduler unit is `com.dotfiles.peer`. Peer sync takes its own lock, so a
> scheduled run and a manual one cannot overlap.

### `dot version`

Shows version, git commit, Go version, and OS/arch. For dev builds (no ldflags), falls back to Go's embedded VCS info with `-dirty` suffix if the working tree has uncommitted changes.

```bash
dot version
```

```
dot v0.20.0 (1e5900a)        # release build
dot dev (d1877ee-dirty)      # dev build with uncommitted changes
  go:   go1.23.0
  os:   darwin/arm64
```

### `dot open <project>`

Launch or resume a tmux workspace. Auto-registers unregistered project names.

```bash
dot open myproject                         # launch or resume
dot open myproject --layout claude         # override layout
dot open myproject --theme dracula         # override theme
```

> Explicit `open` is required — running `dot <unknown>` no longer auto-routes to `open`. This prevents typos like `dot aply` from silently creating a bogus project.

### `dot register` / `dot unregister`

```bash
dot register myproject .                          # current dir
dot register myproject ~/dev/app --layout claude  # with options
dot unregister myproject
```

### `dot list`

Show registered projects and active tmux sessions.

```bash
dot list     # or: dot ls
```

```
Projects (2):
  * myproject          ~/dev/app           (layout=dev, theme=default)
    server-mon         ~/ops/monitoring    (layout=monitor, theme=nord)
```

### `dot stop`

```bash
dot stop myproject       # with confirmation
dot stop myproject -f    # force
```

### `dot layouts`

| Layout | Panes | Description |
|--------|-------|-------------|
| **dev** (default) | 5 | Claude + monitor + files + lazygit + shell |
| **claude** | 7 | Claude + monitor + files + remote + lazygit + shell + logs |
| **monitor** | 4 | monitor + lazygit + shell + logs |

### `dot doctor`

Check workspace tool installation status.

```
Workspace tool status:
  ✓ tmux         /opt/homebrew/bin/tmux
  ✓ claude       /usr/local/bin/claude
  ✓ lazygit      /opt/homebrew/bin/lazygit
  ✓ btop         /opt/homebrew/bin/btop
  ○ yazi         (optional — fallback available)
  ✓ eza          /opt/homebrew/bin/eza
```

### `dot ai` — AI CLI/Config Helpers + Settings Backup/Restore

Manage assistant helper files and portable user-level AI configuration. This is
separate from app installation: Claude, Codex, ChatGPT, and similar GUI apps are
installed through `dot apps install` from the macOS cask catalog.
Antigravity CLI itself is installed with Google's installer:

```bash
curl -fsSL https://antigravity.google/cli/install.sh | bash
```

```bash
dot ai list                         # helper files, detected CLIs, AI casks
dot ai status                       # live + backup status for managed paths

dot ai backup                       # versioned snapshot under BackupRoot
dot ai backup --tag "pre-migration"
dot ai backup --include-auth        # include auth/local-secret files explicitly
dot ai restore                      # restore latest snapshot
dot ai restore --version latest     # explicit alias for latest.txt
dot ai restore --version 20260502T010203Z
dot ai restore --include-auth       # restore auth/local-secret files explicitly

dot ai export ~/workspace/secrets/ai-config.tar.gz
dot ai import ~/workspace/secrets/ai-config.tar.gz

dot ai hud status                  # inspect Claude Code + Codex HUD setup
dot ai hud apply --tool claude,codex --persist

dot ai coauthor-guard status       # inspect AGENTS + Git commit-msg guard
dot ai coauthor-guard apply --mode warn --persist

dot ai skills list                 # inventory Codex/Claude/shared/Antigravity skills
dot ai skills validate --strict    # fail on invalid, duplicate, or legacy metadata
dot ai skills path                 # show SSOT + detected target roots (no flags needed)
dot ai skills status               # maru SSOT + detected tools by default (read-only)

dot ai memory install              # share claude-mem across Codex, Kimi, and Kiro
dot ai memory status               # verify hooks, MCP recall, and transcript capture

dot ai audit summary               # summarize append-only dot ai mutation events
dot ai audit tail 20               # print recent events as JSONL

codex-yolo                         # run Codex without approval prompts or sandboxing
claude-yolo                        # run Claude Code without permission prompts
kiro-yolo                          # run Kiro chat with all tools trusted
```

The `ai` module writes shell/config helper files:

| Path | Purpose |
|------|---------|
| `~/.config/shell/30-ai.sh` | Claude Code env, yolo helpers (Codex/Claude/Kiro), GitHub Models aliases, Fabric alias, GPU helper aliases |
| `~/.config/claude/settings.json` | Minimal dot-managed Claude settings |

The yolo helpers are thin wrappers that forward all arguments to the underlying
CLI with its full-access flag:

| Helper | Runs |
|--------|------|
| `codex-yolo [args...]` | `codex --dangerously-bypass-approvals-and-sandbox` |
| `claude-yolo [args...]` | `claude --dangerously-skip-permissions` |
| `kiro-yolo [args...]` | `kiro-cli chat --trust-all-tools` |

They run with the current user's full host permissions and skip every approval
prompt, so use them only in trusted workspaces.

Kiro CLI asks for confirmation per tool call by default, so `kiro-yolo` is the
only opt-in needed for a full-access session. Note that Kiro CLI does not read
`~/.kiro/settings/permissions.yaml` — that path is not one of its config
surfaces, and a file placed there has no effect. Persistent Kiro tool policy
lives in agent configs (`~/.kiro/agents/*.json`, `.kiro/agents/*.json`) under
`permissions.rules` / `allowedTools`; dot does not manage those.

`dot ai hud apply` installs dot-native status lines:

- Codex: updates `~/.codex/config.toml` `[tui].status_line` with model,
  branch, context, token, and usage-limit segments.
- Claude Code: writes `~/.claude/statusline-dot.py` and merges a `statusLine`
  command into `~/.claude/settings.json`.

Pass `--persist` to record `modules.ai.hud: true` so future `dot apply` runs
keep the HUD in sync.

`dot ai coauthor-guard apply --mode warn` adds a marked AGENTS instruction and
a global Git `commit-msg` hook that warns when a commit message contains
`Co-authored by` or `Co-authored-by:` trailers. Use `--mode block` only when you
want the hook to reject the commit. `--persist` records
`modules.git.coauthor_guard: warn|block` for future `dot apply` runs. Add
`--apply-agents` when you also want to render the updated agents SSOT to live
agent targets immediately. The managed AGENTS block also preserves the global
English-only git commit message policy; an explicit user request can still
allow a coauthor trailer.

Portable backup includes Claude/Codex/Antigravity/Maru settings, MCP config
(Claude MCP state comes from `~/.claude.json`), agents,
hooks, prompts, rules, and plugins. It excludes skill directories, auth tokens,
local overrides, caches, logs, sessions, histories, telemetry, sqlite DBs,
plugin caches, generated/system skill bundles, and Antigravity conversation and
brain state by default. Portable settings containing inline credential-like
values fail closed before a snapshot/archive is created; move those values to
an environment variable or keychain. Use `--include-auth` only when you
explicitly want known auth/local-secret files included.
Portable archive imports accept only regular files and directories; symlinks,
hardlinks, devices, and link-based path pivots are rejected. Export materializes
nested symlinks whose resolved target stays inside a managed portable root;
dangling links and links into machine-local paths (plugin caches and the like)
are skipped, since that wiring is rebuilt per machine. Import manifests are restricted to the built-in entry allowlist;
legacy v1 `.claude/mcp.json` snapshots migrate into `~/.claude.json` MCP state,
and pre-rename `.anchor/*` snapshot entries restore into their `.maru/*` targets.

`dot ai memory install` keeps claude-mem's existing Codex plugin hooks and
configures Kimi Code and Kiro CLI to use the same MCP recall server. A macOS
LaunchAgent runs a workspace-aware transcript bridge for Kimi `wire.jsonl` and
Kiro `messages.jsonl` sessions, so activity from all three CLIs reaches the same
`~/.claude-mem` store. Existing transcript offsets are seeded at installation;
new sessions are discovered and captured automatically without replaying old
history. The command also adds an idempotent persistent-memory policy to the
agents SSOT and renders it to Codex, Kimi, and Kiro. Re-run `install` after
moving the `dot` binary; use `dot ai memory status` to verify the LaunchAgent,
MCP entries, native Codex hooks, and discovered session counts.
The bridge requires both Node.js (MCP server) and Bun (transcript watcher), as
does the upstream claude-mem runtime.

`dot ai skills` scans Markdown `SKILL.md` packages without executing them.
Default roots are `~/.codex/skills`, `~/.claude/skills`, and
`~/.agents/skills`, plus `~/.gemini/skills` (tool `gemini`) and the Antigravity
roots `~/.gemini/antigravity/skills`, `~/.gemini/config/plugins`, and
`~/.gemini/antigravity-cli/plugins` (tool `antigravity`). Use
`--tool codex,claude,agents,gemini,antigravity` to narrow them (`gemini` and
`antigravity` are distinct tools) or repeated `--root <dir>` to scan explicit
roots instead. `list` reports `valid`, `legacy`, and `invalid` entries and
always exits 0 unless scanning itself fails. `validate` fails on invalid
metadata or duplicate schema-valid names; add `--strict` when legacy skills
without `schema_version: v1` should also fail.

`dot ai skills` is diagnose-only. Runtime skill symlinks and tool federation
(`~/.claude/skills/**`, `~/.codex/skills/**`, …) are owned by the Maru app;
dot never writes under any tool skill root or skill source directory. Fix
drift by syncing via Maru, not via dot.

The read-only `dot ai skills path` and `dot ai skills status` commands compare
a skills SSOT against Maru's managed Claude Code and Codex roots. Inventory
commands can still scan agents, Gemini, and Antigravity roots. `provider: maru`
(default; `anchor` is
accepted as a legacy alias) defaults the SSOT to `~/.maru/skills`;
`provider: path` requires `ssot_path`. Target tools are auto-detected (any
managed tool whose skills root such as `~/.claude/skills` exists, falling back
to Claude Code and Codex), so both commands run with no flags. Defaults may also come
from `modules.ai.skills` config:

Legacy `tools` entries for `agents`, `gemini`, and `antigravity` remain
loadable; diagnostics warn and normalize them out of Maru-managed targets.

```yaml
modules:
  ai:
    enabled: true
    agents_ssot: true
    skills:
      provider: maru
      tools: [claude, codex]
```

`modules.ai.skills.enabled` is deprecated and ignored; legacy configs that
still set it load fine, but `dot check`/`dot apply` no longer manage skill
symlinks.

Mutating `dot ai` subcommands append redacted events to
`~/.local/share/dotfiles/ai/events.jsonl`. Events record command type, target
paths, hashes, counts, and backup paths, never prompt text, file content, auth
tokens, or local-secret values. `--dry-run` does not write audit events.

**AI agents SSOT**

`dot ai agents` manages one shared global instruction file at
`~/.config/dotfiles/agents/AGENTS.md` and copy-deploys rendered content to each
tool's expected global path:

| Tool | Target |
|------|--------|
| Claude Code | `~/.claude/CLAUDE.md` |
| Codex CLI | `~/.codex/AGENTS.md` |
| Cursor | `~/.cursor/AGENTS.md` |
| Kiro CLI | `~/.kiro/steering/AGENTS.md` |
| Kimi Code CLI | `~/.kimi-code/AGENTS.md` |
| Antigravity CLI (`gemini` alias) | `~/.gemini/GEMINI.md` |
| GitHub Copilot | `~/.config/github-copilot/AGENTS.md` |
| Aider | `~/.aider.conf.md` |

The deploy model is copy-based, not symlink-based. The SSOT stays authoritative,
but local tool files can be inspected or edited without immediately mutating the
source file. Optional per-tool addenda live under
`~/.config/dotfiles/agents/overlays/<tool>.md` and are appended only when that
tool is rendered.

```bash
# bootstrap from an existing tool file
dot ai agents init --from-current claude

# or run the section-based assistant from scratch
dot ai agents author

# review
dot ai agents show
dot ai agents show --rendered codex --with-line-numbers
dot ai agents show --rendered antigravity
dot ai agents diff --tool codex

# deploy to Claude, Codex, Cursor, plus optional tools that already exist
dot ai agents apply
dot ai agents apply --tool claude,codex
dot ai agents apply --tool antigravity  # gemini is accepted as an alias
dot ai agents apply --tool codex --force  # overwrite externally edited target after backup
```

Agents deployment uses protected writes. If a rendered target changed outside
the last dot-managed apply, `dot ai agents apply` stops instead of overwriting
it. Review `dot ai agents diff --tool <id>` first, then rerun with `--force`
only when the backup-and-overwrite behavior is intended.

`dot ai backup`, `restore`, `export`, and `import` include the SSOT directory
and the rendered tool targets. `dot ai restore --reapply-agents` restores the
snapshot and then reapplies the restored SSOT to selected tool targets.
Automatic deployment during `dot apply` is off by default; enable
`modules.ai.agents_ssot: true` only when you want `dot apply` to re-render
agent targets. Fresh interactive and unattended `dot init` configurations
enable this explicitly; reconfiguration preserves the existing choice.

### `dot ws` — Dual-Workspace Folder Ops

Operate on both `~/workspace/work/` (git-tracked text) and the resolved sync mirror (cloud-backed binaries) simultaneously to keep their folder structures in sync.

```bash
dot ws init                          # clone configured repos (work, vault) recursively — vault targets the configured/detected vault location
dot ws init --force                  # re-clone over populated targets (destructive; prompts)
dot ws mkdir projects/rise-y2        # create on both sides
dot ws mv projects/rise projects/rise-y1  # rename on both sides
dot ws rm scratch --recursive        # remove from both sides
dot ws audit                         # report structural mismatches
dot ws audit projects                # limit scope
dot ws reconcile                     # interactive resolve (copy/delete/skip)
dot ws reconcile --yes               # bulk copy (never deletes)
```

### `dot apps` — macOS Cask Install + Settings Backup/Restore

Manage macOS cask applications and their per-app settings (plists, `Application Support/`,
sandbox `Containers`, `Group Containers`). macOS-only; no-ops on Linux.

```bash
dot apps list                         # show catalog: groups + ★ defaults + ✓ installed
dot apps install                      # interactive MultiSelect + optional "save to state"
dot apps install raycast obsidian     # explicit tokens (skip picker)
dot apps install --defaults           # catalog's default set
dot apps install --all                # every cask in the catalog
dot apps install --select             # force picker even when state has a list
dot apps install --no-save            # one-off install without persisting selection
dot apps status                       # install ✓/· + backup path counts per app
dot apps backup                       # snapshot settings for BackupApps ∩ manifest
dot apps backup raycast hazel         # explicit tokens
dot apps backup --all --to <root>     # override backup root, back up every entry
dot apps restore                      # restore from the configured root (confirms first)
dot apps restore --from <root> moom
```

**Two independent lists** on the user state:

- **Install list** (`modules.macapps.casks` + `casks_extra`) — drives `dot apps install`.
  Terminal app choices from `modules.terminal_apps.apps` are merged into this
  list during `dot apply` and saved-state `dot apps install` runs.
- **Backup list** (`modules.macapps.backup_apps`) — scopes `apps backup/restore`. Empty
  means "manifest ∩ installed casks".

**Archive layout** under the shared backup root:

```
<BackupRoot>/app-settings/<hostname>/<cask>/<Library-relative-path>...
```

Excludes `Caches/`, `GPUCache/`, `Code Cache/`, `IndexedDB/`, `Local Storage/`,
`Service Worker/`, `Logs/`, `*.log`, `*.lock`, `Singleton*`, and `.DS_Store`.
Before overwriting live files on restore, originals are snapshotted to
`~/.local/share/dotfiles/backup/`. `killall cfprefsd` runs after restore so
re-launched apps read the new plists.

The embedded cask catalog (`internal/config/catalog/macos-apps.yaml`) groups 60+
apps across Security, Knowledge, Browsers, Terminal & Editor, AI, Communication,
Productivity utilities, Capture & Dictation, Files, Media, Dev, System, Writing.
The `defaults:` section defines the preselected bootstrap set. Catalog entries
can declare required Homebrew taps; `cmux` runs `brew tap manaflow-ai/cmux`
before `brew install --cask cmux`, and `maru-workspace` runs
`brew tap staixbwlb/cask` before `brew install --cask maru-workspace`. Orca
uses the `stablyai/orca` tap.

### `dot profile` — Versioned Profile Snapshots

Snapshot the user-level profile (`~/.config/dotfiles/config.yaml`, install / backup
cask lists, and optionally `~/.ssh/age_key*`) into host-scoped, timestamped
version directories under the shared backup root.

```bash
dot profile backup                            # new snapshot
dot profile backup --tag "pre-migration"      # with label
dot profile backup --include-secrets          # also copy ~/.ssh/age_key*
dot profile list                              # newest-first; ★ marks latest
dot profile restore                           # restore the latest version
dot profile restore --version 20260416T000856Z
dot profile restore --include-secrets         # include ~/.ssh/age_key*
dot profile restore --no-state                # skip config.yaml copy
dot profile prune --keep 5                    # delete older snapshots
dot profile backup --to <root>                # override backup root
```

**Layout** (one cloud backup folder holds app settings, profile snapshots, and AI settings):

```
<BackupRoot>/
├── app-settings/<hostname>/<cask>/Library/<rel-path>...
├── ai-config/<hostname>/
│   ├── latest.txt
│   └── 20260502T010203Z/
│       ├── meta.yaml
│       ├── manifest.yaml
│       └── home/.codex/config.toml
└── profiles/<hostname>/
    ├── latest.txt                       # points at current version
    ├── 20260416T000829Z/
    │   ├── meta.yaml                    # version, tag, hostname, timestamp, user
    │   ├── config.yaml                  # ~/.config/dotfiles/config.yaml
    │   ├── apps/install.yaml            # casks + casks_extra
    │   ├── apps/backup.yaml             # backup_apps + backup_root
    │   └── secrets/age_key*             # only with --include-secrets
    └── 20260416T000856Z-2/              # back-to-back runs get a -N suffix
```

Versions are UTC `YYYYMMDDTHHMMSSZ`. The `latest.txt` pointer is what `restore`
reads by default; pass `--version` to pick any earlier snapshot. On a fresh
machine the first `profile restore` boots state before you've run `dot init`.

### Backup-root resolution (shared by `apps`, `profile`, and `ai`)

Precedence, highest wins:

1. `--to <path>` / `--from <path>` flag.
2. `modules.macapps.backup_root` in user state.
3. Auto-detected cloud secrets folder (`<cloud>/secrets/dotfiles-backup`, Dropbox preferred).
4. Local fallback: `~/.local/share/dotfiles/backup`.

Set it once in `dot init` — all backup subcommands read the same value so
your cloud backup folder holds everything.

**`ws init`** clones each configured repo (from user state `workspace.repos`) into `<workspace.path>/<name>` using `git clone --recurse-submodules`. Targets that are missing, empty, or contain only a `.gdrive` symlink are cloned without `--force` (the symlink is preserved). Populated targets are skipped unless `--force` is given.

**Safety:**
- Rejects absolute paths, `..`, and workspace root refs
- Never overwrites existing directories
- Symlinks (e.g. `inbox/downloads`) auto-excluded from audit
- `rm` of non-empty dir requires `--recursive`
- `reconcile --yes` only copies — deletion always needs interactive confirmation
- Ignore patterns: `.git`, `node_modules`, `.venv`, `__pycache__`, `.next`, `.cache`, `_sys`

### Global Flags

| Flag | Description |
|------|-------------|
| `--profile` | Profile name (`minimal`, `full`, `server`) |
| `--module` | Run specific modules only (repeatable) |
| `--dry-run` | Preview without changes |
| `--yes` | Unattended mode (skip prompts) |
| `--config` | Custom config YAML path |
| `--home` | Override home directory (admin setup) |

---

## Modules

### Execution Order

```
packages → shell → node → git → ssh → terminal → tmux →
workspace → ai → fonts → macapps → conda → gpg → secrets
```

### Module Details

| Module | Profile | Description |
|--------|---------|-------------|
| **packages** | minimal | Homebrew formula installation |
| **shell** | minimal | zsh, Oh My Zsh, plugins, config files |
| **node** | full | pnpm store relocation outside cloud-synced workspace trees (~/.config/pnpm/npmrc) |
| **git** | minimal | git config, aliases, global ignore |
| **ssh** | minimal | SSH config, config.d includes |
| **terminal** | minimal | starship prompt, Orca auto-install (macOS/Arch), Warp theme |
| **tmux** | full | tmux.conf (256color, vim keys, C-a prefix) |
| **workspace** | full | Dual-workspace: git repo clone, gh auth, symlink federation (cloud mirror, vault, inbox). Vault location is selectable at init and auto-detected from existing `<workspace>/work/vault` or `<workspace>/vault`; the separate vault repo entry is skipped when the vault lives inside work (e.g. as a submodule). Cloud mirror is selected at init from detected mounts (Dropbox preferred, Google Drive accounts are listed); shell exports `CLOUD_WORKSPACE`/`CLOUD_WORK`, alias `cwork`, and the `ws()` jumper (formerly `GDRIVE_*`/`gwork`) |
| **ai** | full | AI CLI/config helpers, Claude/Codex/Antigravity settings backup, optional HUD |
| **fonts** | full | Nerd Font download from GitHub Releases |
| **macapps** | full (darwin) | Install selected Homebrew casks from the embedded catalog |
| **conda** | full | Conda/Mamba `.condarc` defaults; shell hooks live in managed shell init |
| **gpg** | full | GPG agent + git commit signing |
| **secrets** | full | Age-encrypted SSH keys and shell secrets |

### Prompt Styles

The terminal module deploys a Starship prompt config. Two styles are selectable
during `dot init` or `dot reconfigure`:

| Style | Default for | Character | Info shown |
|-------|-------------|-----------|------------|
| **minimal** | minimal, server | `>` | truncated path, branch, dirty marker |
| **rich** | full | `→` | time, user, path, host, branch+status, language versions, duration |

```bash
dot apply --module terminal     # deploys the selected style
dot reconfigure                 # switch between minimal ↔ rich
```

Config key: `modules.terminal.prompt_style` (state: `modules.prompt_style`).

### Terminal Apps

`dot init` and `dot reconfigure` include a non-server terminal app selection.
The fresh `full` profile defaults to Orca. macOS offers `orca`, `warp`, `wave`,
`cmux`, and `iterm2`; Arch Linux offers Orca. Existing explicit selections are
preserved.

Selections are stored in `modules.terminal_apps.apps`. The legacy `casks` key
is accepted on load and rewritten as `apps` on the next save. Selecting `warp`
also enables the managed Warp theme file.

On macOS, regular `dot apply` installs a missing Orca through Homebrew:

```bash
brew install --cask stablyai/orca/orca
```

On Arch Linux, the terminal module accepts either `stably-orca-bin` or
`stably-orca-git` as installed and otherwise runs:

```bash
yay -S --needed stably-orca-bin
```

`yay` is a prerequisite; dot reports the required command and stops if the AUR
helper is unavailable. Other Linux distributions do not attempt an automatic
GUI app install. This preference controls dot's selected terminal workspace
app and does not register an operating-system terminal command handler.

### Packages

**minimal** (17):
`git`, `git-lfs`, `gh`, `age`, `rsync`, `fzf`, `ripgrep`, `fd`, `bat`, `jq`, `yq`, `direnv`, `zoxide`, `eza`, `starship`, `curl`, `fnm`

**full** adds (+11 unique):
`maru-cli`, `btop`, `lazygit`, `yazi`, `glow`, `csvlens`, `chafa`, `uv`, `pipx`, `tmux`, `gnupg`

**server** adds (+4):
`btop`, `tmux`, `uv`, `pipx`

---

## Tmux

### Key Bindings

| Key | Action |
|-----|--------|
| `C-a` | Prefix |
| `C-a d` | Detach session |
| `C-a s` | List sessions |
| `C-a c` | New window (current path) |
| `C-a n/p` | Next / previous window |
| `C-a \|` | Split horizontal |
| `C-a -` | Split vertical |
| `C-a h/j/k/l` | Navigate panes |
| `C-a H/J/K/L` | Resize panes |
| `C-a Enter` | Enter copy mode |
| `v` / `y` (copy mode) | Begin selection / Copy and exit |
| `C-a r` | Reload config |
| `C-a /` | Show cheatsheet popup |

### Shell Aliases

| Alias | Command |
|-------|---------|
| `t [name]` | Attach or create session (default: `main`) |
| `ta <name>` | `tmux attach -t` |
| `ts <name>` | `tmux new-session -s` |
| `tl` | `tmux list-sessions` |
| `tk <name>` | `tmux kill-session -t` |
| `td` | `tmux detach` |

### Workspace Layouts

**dev** (default — 5 panes):
```
┌──────────────┬──────────┐
│              │  MONITOR │
│   CLAUDE     ├──────────┤
│              │  FILES   │
├──────────────┼──────────┤
│  LAZYGIT     │   SHELL  │
└──────────────┴──────────┘
```

**claude** (7 panes):
```
┌──────────────┬──────────┐
│              │  MONITOR │
│   CLAUDE     ├──────────┤
│              │  FILES   │
│              ├──────────┤
│              │  REMOTE  │
├──────────────┼─────┬────┤
│   LAZYGIT    │SHELL│LOG │
└──────────────┴─────┴────┘
```

**monitor** (4 panes):
```
┌──────────────┬──────────┐
│   MONITOR    │  SHELL   │
├──────────────┼──────────┤
│   LAZYGIT    │  LOGS    │
└──────────────┴──────────┘
```

### Themes

5 built-in themes: `default`, `dracula`, `nord`, `catppuccin`, `tokyo-night`.
Session-scoped — multiple workspaces can use different themes simultaneously.

### Tool Fallback Chains

| Pane | Primary | Fallback |
|------|---------|----------|
| MONITOR | btop | htop → top |
| GIT | lazygit | git status |
| FILES | yazi | eza → tree → ls |
| CLAUDE | claude | install message |

---

## Profiles

Profiles use YAML inheritance. `full` extends `minimal`.

| Profile | Modules | Packages | Use Case |
|---------|---------|----------|----------|
| **minimal** | 5 | 17 | Lightweight dev setup |
| **full** | 14 | 28 | Complete workstation (macapps enabled on darwin) |
| **server** | 8 | 21 | GPU/DGX server |

**server**: Extends `minimal` + tmux, ai, conda. Disables workspace, fonts, macapps, gpg, secrets. Auto-suggested when NVIDIA GPU or CUDA is detected.

---

## Configuration

User settings are stored in `~/.config/dotfiles/config.yaml`:

```yaml
name: "Young Joon Lee"
email: "hello@jeju.ai"
github_user: "entelecheia"
timezone: "Asia/Seoul"
profile: "full"
modules:
  workspace:
    path: "~/workspace"
    # vault: "~/workspace/work/vault"  # optional; auto-detected when omitted (default <path>/work/vault)
    repos:
      - name: work
        remote: "git@github.com:user/work.git"
      # vault repo entry only when the vault is a STANDALONE repo at <path>/vault;
      # skipped automatically when the vault lives inside work (e.g. a submodule)
      - name: vault
        remote: "git@github.com:user/vault.git"
  ai:
    enabled: true
  prompt_style: rich    # "minimal" or "rich"
  terminal_apps:
    enabled: true
    apps:
      - orca
  fonts:
    family: "FiraCode"
  macapps:
    enabled: true
    casks:          # install list (catalog tokens)
      - 1password
      - raycast
      - obsidian
    casks_extra:    # install list (free-form additions)
      - maccy
    backup_apps:    # backup/restore scope; empty = manifest ∩ installed
      - raycast
      - obsidian
    backup_root: "~/Library/CloudStorage/GoogleDrive-*/My Drive/secrets/dotfiles-backup"
  rsync:
    remote_host: "user@ubuntu-server"
    remote_path: "~/workspace/work/"
    interval: 300
ssh:
  key_name: "id_ed25519_entelecheia"
secrets:
  age_identity: "~/.ssh/age_key_entelecheia"
  age_recipients:
    - "age1..."
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `DOTFILES_YES` | Set to `true` for unattended mode |
| `DOTFILES_PROFILE` | Override profile name |
| `DOTFILES_NAME` | Override user name |
| `DOTFILES_EMAIL` | Override email |
| `DOTFILES_WORKSPACE_PATH` | Override workspace path |
| `DOTFILES_REPO_DIR` | Dotfiles repo directory |
| `DOTFILES_HOME` | Override home directory |
| `GITHUB_TOKEN` | GitHub API token for `update` |

---

## Architecture

Same modular Go architecture as [rootfiles-v2](https://github.com/entelecheia/rootfiles-v2).

```
rootfiles-v2 (root, server)     dotfiles-v2 (user, workstation)
━━━━━━━━━━━━━━━━━━━━━━━━━━━     ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Packages (APT), users, SSH       Packages (Homebrew), shell, git
Docker, GPUs, tunnels            Terminal, fonts, AI
Locale, firewall, storage        Workspace, secrets, sync, tmux
```

### Project Structure

```
dotfiles-v2/
├── cmd/dot/main.go               # Entry point (ldflags: version, commit)
├── internal/
│   ├── cli/                      # Cobra commands
│   │   ├── open.go               # dot open — workspace launcher
│   │   ├── sync_cmd.go           # dot sync — workspace sync (local mirror or SSH)
│   │   ├── clean_cmd.go          # dot clean — workspace junk cleanup
│   │   ├── status_cmd.go         # dot status — unified dashboard
│   │   └── workspace_cmds.go     # stop, list, register, unregister, layouts, doctor
│   ├── config/                   # Config struct, loader, detector, state
│   │   └── profiles/             # Embedded YAML profiles (go:embed)
│   ├── aisettings/               # AI assistant settings backup/restore/export/import
│   ├── clean/                    # Workspace cleanup scanner + deletion
│   ├── exec/                     # Runner (dry-run), Brew wrapper
│   ├── module/                   # 14 module implementations (macapps darwin-only)
│   ├── syncer/                   # Workspace sync engine (used by dot sync)
│   │   ├── scheduler.go          # Scheduler types
│   │   ├── scheduler_darwin.go   # macOS launchd
│   │   └── scheduler_other.go    # Linux systemd
│   ├── workspace/                # Workspace management
│   │   ├── config.go             # Project config, YAML load/save
│   │   ├── deploy.go             # Shell script deployer (go:embed)
│   │   └── scripts/              # Embedded shell scripts
│   ├── template/                 # Go text/template engine
│   │   └── templates/            # Embedded templates (go:embed)
│   ├── fileutil/                 # File ops, download, hash compare
│   └── ui/                       # Charm huh TUI wrapper
├── tests/                        # Integration + scenario tests
├── scripts/install.sh            # curl-pipe installer
├── .goreleaser.yaml              # Cross-platform release config
└── .github/workflows/            # CI: test → release pipeline
```

### Key Design

- **Module interface**: `Check()` → `Apply()` — idempotent, dry-run aware
- **Profile inheritance**: YAML `extends` chain with field-level merging
- **go:embed**: Profiles, templates, and scripts compiled into the binary
- **SHA256 hash**: Skip writes when content unchanged, backup before overwrite
- **Non-fatal errors**: Module failures logged, remaining modules continue
- **Platform build tags**: Platform-specific code (xattr, launchd, systemd) via `//go:build`

---

## CI/CD

### Test Pipeline

| Job | Matrix | Description |
|-----|--------|-------------|
| **lint** | ubuntu-latest | golangci-lint |
| **unit** | ubuntu-latest, macos-latest | Go unit tests + coverage |
| **integration** | ubuntu-24.04 × {minimal,full,server} + server image | Docker-based profile tests |
| **linux** | modules + 10 scenarios on ubuntu-22.04 image | Module and E2E scenario suite |
| **apps-install-macos** | macos-latest | macOS cask install plus macapps scenario |

**Release**: Triggered by `workflow_run` — only after Test succeeds on a `v*` tag. Uses GoReleaser for cross-platform builds (darwin/linux × amd64/arm64).

### Creating a Release

```bash
git tag v0.9.0
git push origin v0.9.0
# Test workflow runs → on success → Release workflow creates GitHub Release
```

---

## GPU Server Provisioning

On a fresh DGX or GPU server — auto-detects NVIDIA GPU + CUDA:

```bash
curl -fsSL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/install.sh | bash
dot init --yes     # auto-selects 'server' profile
dot apply --yes    # packages (incl. rsync), shell, git, ssh, terminal, tmux, ai, conda
```

Or import config from your workstation:

```bash
dot init --from ~/workspace/secrets/dotfiles-config.yaml
dot apply --yes
```

Detection: `nvidia-smi` (GPU model), `/usr/local/cuda` (CUDA home), `/etc/dgx-release` (DGX).

---

## Development

```bash
make build      # build binary
make test       # run tests
make lint       # lint
make clean      # clean artifacts
make install    # install to ~/.local/bin/
```

## License

MIT
