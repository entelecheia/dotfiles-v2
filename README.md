# dotfiles-v2

[![Test](https://github.com/entelecheia/dotfiles-v2/actions/workflows/test.yaml/badge.svg)](https://github.com/entelecheia/dotfiles-v2/actions/workflows/test.yaml)
[![Release](https://github.com/entelecheia/dotfiles-v2/actions/workflows/release.yaml/badge.svg)](https://github.com/entelecheia/dotfiles-v2/actions/workflows/release.yaml)

Declarative user environment management + AI-powered tmux workspace manager.
A single Go binary. macOS + Linux + GPU servers. Modular, profile-based, AI-ready.

---

## Quick Start

### Install

```bash
curl -fsSL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/install.sh | bash
```

The installer handles prerequisites automatically:
- **macOS**: Installs Homebrew (which includes Xcode Command Line Tools)
- **Linux**: Installs Linuxbrew for consistent package management
- Downloads the `dot` binary and configures PATH

### Setup

```bash
dotfiles            # welcome screen with next-step guidance
dotfiles init       # interactive TUI — name, email, profile, modules
dotfiles apply      # apply all enabled modules
dotfiles usecase    # detailed workflow examples
```

### Migrate from another machine

**Option A — profile snapshot (recommended on macOS):**

```bash
# On the existing machine
dotfiles profile backup --tag "pre-migration" --include-secrets
dotfiles apps backup                       # also snapshot per-app settings
dotfiles ai backup                         # portable Claude/Codex/MCP/skills settings

# On the new machine (Drive already mounted)
dotfiles profile restore --include-secrets # restores ~/.config/dotfiles + ~/.ssh/age_key*
dotfiles apply                             # brew formulas + casks from install list
dotfiles apps restore                      # plists, Application Support, containers
dotfiles ai restore                        # Claude/Codex/MCP/skills settings
```

The shared backup root lives in a single Drive folder
(`<drive>/secrets/dotfiles-backup` by default) and holds every snapshot the
user has taken across machines. `profile list` shows every version, and
`profile restore --version <id>` rolls back to any specific one.

**Option B — plain YAML export:**

```bash
# On the existing machine — export config
dotfiles config export ~/workspace/secrets/dotfiles-config.yaml

# On the new machine — import and apply
dotfiles init --from ~/workspace/secrets/dotfiles-config.yaml
dotfiles apply
# → gh auth login (if private repos configured)
# → git clone work/vault repos
# → symlink federation, shell config, packages...
```

### Workspace

```bash
dotfiles open myproject   # launch or resume a multi-panel tmux workspace
dotfiles open myproject   # SSH dropped? just run it again — resumes exactly
```

### Build from source

```bash
git clone https://github.com/entelecheia/dotfiles-v2.git && cd dotfiles-v2
make build          # → bin/dotfiles
make install        # → ~/.local/bin/dotfiles + ~/.local/bin/dot (symlink)
```

---

## Commands

> `dotfiles` and `dot` are interchangeable — `dot` is a convenience symlink.
> Run `dotfiles` with no arguments for a welcome screen; `dotfiles usecase` for detailed workflows.

### `dotfiles` (no args) — welcome screen

Prints a friendly getting-started guide. Detects whether you've configured dotfiles and shows next steps. Pass no arguments to any invocation of `dotfiles` to see it.

### `dotfiles usecase`

Walk through 9 detailed workflows: first-time setup, safe apply, daily workspace, Google Drive sync, drive-exclude, secrets, updates, GPU server, troubleshooting.

```bash
dotfiles usecase
```

### `dotfiles init`

Interactive TUI setup. Collects user info and saves to `~/.config/dotfiles/config.yaml`.

```bash
dotfiles init                                              # fresh setup
dotfiles init --from ~/workspace/secrets/config.yaml    # import from another machine
dotfiles init --yes                                        # unattended with defaults
```

Use `--from` to import settings from another machine's exported config. Identity fields (name, email, SSH key) are pre-populated; machine-specific settings (workspace path, terminal) are confirmed interactively.

Prompts for:
- Name, Email, GitHub username
- Timezone (default: `Asia/Seoul`)
- Profile (`minimal` / `full` / `server`)
- GPU/CUDA auto-detection (suggests `server` when NVIDIA GPU detected)
- Prompt style (`minimal` / `rich`) — see below
- Module opt-ins: workspace, AI CLI/config helpers, Warp, fonts
- SSH key name (auto-derived from GitHub username)
- Workspace git repos: remote URLs for `work` and `vault` directories (optional)
- GitHub authentication via `gh auth login` with broad scopes (optional, for private repos)

### `dotfiles apply`

Apply configuration to the system. Runs each enabled module in order.

```bash
dotfiles apply                          # interactive
dotfiles apply --yes                    # unattended (skip prompts)
dotfiles apply --dry-run                # preview only
dotfiles apply --profile minimal        # override profile
dotfiles apply --module shell --module git  # specific modules
```

#### Safe Apply

```bash
dotfiles preflight --check-only   # 1. scan environment (no changes)
dotfiles preflight                # 2. generate config from detected env
dotfiles check                    # 3. show modules with pending changes
dotfiles apply --dry-run          # 4. preview all changes
dotfiles apply --module shell     # 5. apply selectively
```

> Files are backed up to `~/.local/share/dotfiles/backup/` before overwrite. Identical content (SHA256) is never overwritten.

### `dotfiles check`

Compare current system state against desired profile. No changes made.

```bash
dotfiles check
dotfiles check --profile full
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

### `dotfiles diff`

Preview pending changes with detailed descriptions.

```bash
dotfiles diff
dotfiles diff --module git
```

### `dotfiles update`

Self-updating binary. Downloads the latest release from GitHub. (`upgrade` is an alias.)

```bash
dotfiles update          # download & install
dotfiles update --check  # check only
```

### `dotfiles config`

Show current configuration (profile, system, modules, packages).

```bash
dotfiles config
dotfiles config export                                       # print to stdout
dotfiles config export ~/workspace/secrets/config.yaml    # save to file
```

`config export` produces a portable YAML file that can be used on another machine with `dotfiles init --from <file>`.

### `dotfiles reconfigure`

Re-run init prompts with current values as defaults, then optionally re-apply.

```bash
dotfiles reconfigure
```

### `dotfiles secrets`

Manage age-encrypted secrets (SSH keys, shell secrets).

```bash
dotfiles secrets init              # encrypt SSH key + shell secrets
dotfiles secrets init --scaffold   # also create empty 90-secrets.sh (0600) if missing
dotfiles secrets backup <dir>      # copy .age files to backup dir
dotfiles secrets restore <dir>     # decrypt from backup
dotfiles secrets status            # check decrypted + encrypted files
dotfiles secrets list              # list encrypted files
```

Encryption flow:
```
~/.ssh/id_ed25519_user         → age -e → ~/.local/share/dotfiles-secrets/id_ed25519_user.age
~/.config/shell/90-secrets.sh  → age -e → ~/.local/share/dotfiles-secrets/90-secrets.sh.age
```

### `dotfiles drive-exclude`

Exclude heavy directories from Google Drive sync using macOS xattr (`com.google.drivefs.ignorecontent`).

```bash
dotfiles drive-exclude scan              # scan ~/workspace (default)
dotfiles drive-exclude scan ~/projects   # custom path
dotfiles drive-exclude apply             # interactive confirmation
dotfiles drive-exclude apply --yes       # skip confirmation
dotfiles drive-exclude apply --dry-run   # preview only
dotfiles drive-exclude add <path>...     # manually exclude specific dirs
dotfiles drive-exclude status            # show current exclusion status
```

Supported patterns: `node_modules`, `.pnpm`, `.npm`, `.next`, `.nuxt`, `.astro`, `.svelte-kit`, `.parcel-cache`, `.turbo`, `.angular`, `.webpack`, `dist`, `build`, `out`, `target`, `.venv`, `venv`, `__pycache__`, `.mypy_cache`, `.pytest_cache`, `.ruff_cache`, `.pixi`, `.cache`

> macOS only for real xattr. `--dry-run` works on all platforms.

### `dotfiles status`

Unified dashboard showing system, user, modules, secrets, sync, and workspace status at a glance.

```bash
dotfiles status
```

### `dotfiles clean`

Remove junk directories that waste disk space and cause Google Drive sync problems. The `_sys/` subtree is always protected.

```bash
dotfiles clean                # scan and preview (no deletion)
dotfiles clean --yes          # actually delete
dotfiles clean --all --yes    # include risky patterns (dist/, build/, out/, target/)
dotfiles clean ~/projects     # custom path (default: ~/workspace/work)
```

**Safe patterns** (always scanned): `node_modules`, `__pycache__`, `.pytest_cache`, `.mypy_cache`, `.ruff_cache`, `.venv`, `venv`, `env` (with pyvenv.cfg), `.next`, `.cache`, `.DS_Store`

**Risky patterns** (`--all` required): `dist`, `build`, `out`, `target`

> Alias: `dotfiles gc`

### `dotfiles sync`

Binary-only workspace sync with a remote server over SSH using `rsync`. Text files use git exclusively. Default is **pull-then-push**: pull newer binaries from remote, then push local binaries (local is authoritative).

```bash
dotfiles sync setup           # install rsync, configure SSH, deploy extensions & scheduler
dotfiles sync                 # pull then push (default)
dotfiles sync pull            # pull only: remote → local (--update, safe)
dotfiles sync push            # push only: local → remote (--delete-after)
dotfiles sync status          # show sync state, scheduler, last result
dotfiles sync log [N]         # tail last N sync log lines (default 50)
dotfiles sync pause           # pause auto-sync scheduler
dotfiles sync resume          # resume auto-sync scheduler
```

**Key features:**
- **Binary-only**: syncs via `--include-from` binary extensions file (pdf, hwp, docx, images, video, archives, ML data)
- **Pull-then-push**: pull phase uses `--update` (safe), push phase uses `--delete-after` (local authority). Remote-created files are pulled first, so push never deletes them.
- **POSIX lock**: `mkdir`-based atomic lock prevents concurrent syncs (macOS compatible, no `flock` needed)
- **Log rotation**: auto-trims log at 2000 lines
- **`-V` / `--verbose`**: streams rsync progress to terminal

> Auto-sync runs every 5 minutes via launchd (macOS) or systemd timer (Linux).

### `dotfiles gdrive-sync`

Local rsync mirror between `~/workspace/work` (single primary) and the cloud-sync client's mirror tree (default `~/gdrive-workspace/work`). No SSH; the cloud client (Google Drive, Dropbox, etc.) handles the round-trip to the cloud.

**Git + Drive payload model.** Git remains the source of truth for text/source files. `gdrive-sync` fills the LFS-shaped gap for binaries and large artifacts while preserving Google Drive sharing benefits. The Git-tracked `<workspace>/.dotfiles/gdrive-sync/baseline.manifest` is the shared Drive payload index: `pull` restores or updates files listed there from the mirror, while baseline-unknown Drive files are staged by `intake` into `<workspace>/inbox/gdrive/<intake-ts>/` for manual routing. Deletes remain non-destructive by default.

```bash
dotfiles gdrive-sync init               # one-time: create <workspace>/.dotfiles/gdrive-sync/ + migrate global state
dotfiles gdrive-sync setup              # rsync (if missing) + push scheduler
dotfiles gdrive-sync setup --pull-interval=15m   # also deploy pull+intake scheduler

dotfiles gdrive-sync push                            # workspace → mirror (default policy)
dotfiles gdrive-sync push --propagate=create,update,delete  # full sync
dotfiles gdrive-sync push --propagate=create         # additive only
dotfiles gdrive-sync push --propagate=update         # in-place updates only

dotfiles gdrive-sync pull                            # restore/update baseline-tracked Drive payloads
dotfiles gdrive-sync intake                          # pull tracked payloads, then stage new Drive files
dotfiles gdrive-sync intake --strict                 # use sha256 fingerprints

dotfiles gdrive-sync inbox                           # show staging + manifest counters
dotfiles gdrive-sync inbox forget <relpath>          # force re-intake of one path
dotfiles gdrive-sync inbox clear                     # empty imports + tombstones manifests

# Aliases (back-compat; emit a deprecation hint):
dotfiles gdrive-sync                # alias for `push`
dotfiles gdrive-sync sync           # alias for `push`

# Maintenance:
dotfiles gdrive-sync status         # paths, propagation, schedulers, last-pull/push/intake
dotfiles gdrive-sync conflicts      # list .sync-conflicts/<ts>/ entries with ages
dotfiles gdrive-sync pause          # stop push + pull+intake schedulers (paused gate)
dotfiles gdrive-sync resume         # clear paused gate, re-arm schedulers
dotfiles gdrive-sync shared         # manage shared-folder exclusions (auto + manual)
dotfiles gdrive-sync migrate        # one-shot: convert legacy symlinks + bring mirror in (idempotent)
```

**Per-workspace store** at `<workspace>/.dotfiles/gdrive-sync/` is the authoritative config + state location:

| File | Purpose |
|------|---------|
| `config.yaml` | machine-local propagation policy, intervals, mirror_path, max_delete, shared_excludes |
| `state.yaml` | machine-local last_pull / last_push / last_intake / last_intake_ts_dir |
| `exclude.txt` | editable static excludes (writable copy of embedded baseline) |
| `ignore.txt` | user-supplied ignore patterns (additive layer) |
| `shared-excludes.dyn.conf` | auto-generated per-run shared-folder list |
| `baseline.manifest` | **Git-tracked** Drive payload index (relpath → strict fingerprint) |
| `imports.manifest` | machine-local GDrive-origin files already intaked (relpath → fingerprint + imported-at) |
| `tombstones.log` | machine-local mirror deletions detected (record-only, never propagated locally) |
| `log/gdrive-sync.log` | rotated sync log |

The legacy global `~/.config/dotfiles/config.yaml modules.gdrive_sync` block is consulted **once** to migrate values into this store on first invocation, then ignored. `init` appends a workspace `.gitignore` block that keeps machine-local state ignored while allowing `.dotfiles/gdrive-sync/baseline.manifest` to be committed.

**Propagation policy** maps to rsync flags:

| Create | Update | Delete | rsync flags appended |
|--------|--------|--------|----------------------|
| ✓ | ✓ | ✗ (default) | (none — natural copy-new-and-modified) |
| ✓ | ✓ | ✓ | `--delete-after --max-delete=N` |
| ✓ | ✗ | ✗ | `--ignore-existing` |
| ✗ | ✓ | ✗ | `--existing` |
| ✗ | ✗ | ✓ | `--existing --ignore-existing --delete-after --max-delete=N` |
| ✗ | ✗ | ✗ | refused — `must list at least one of create,update,delete` |

**Pull + intake algorithm.**

1. `pull` reads Git-shared `baseline.manifest`. For each baseline path, if the mirror has a newer/different payload and local still matches baseline, it updates the original local path. If the local file is missing, it restores it from the mirror. If local and mirror both diverged, local is preserved and the Drive copy is saved under `.sync-conflicts/<ts>/from-gdrive/`.
2. `intake` runs that tracked pull first, then scans mirror files not present in `baseline.manifest`.
3. If a baseline-unknown file fingerprint matches `imports.manifest` → skip (already intaked; idempotent even after operator moves it out of staging).
4. Else → copy into `<workspace>/inbox/gdrive/<microsecond-timestamp>/<relpath>` preserving subtree, and append to `imports.manifest`.

Files in baseline that are missing from mirror become tombstones — recorded in `tombstones.log`, never propagated as local deletions. Use `dot gdrive-sync inbox forget <relpath>` to revoke an imports entry and force a re-intake for baseline-unknown files.

**Key features:**
- **Git-tracked baseline, Drive-backed payloads**: Git syncs the manifest across machines; Google Drive carries the large/binary payloads. Git-tracked files are excluded from baseline and handled by Git, not gdrive-sync.
- **Push-first for local artifacts**: `push` propagates workspace artifact state to mirror under the propagation policy. Default policy never deletes — operator opts into delete via `--propagate=...,delete` or by flipping `propagation.delete` in `config.yaml`.
- **Tracked pull guard**: `push` first runs tracked pull. Safe Drive-side edits are applied locally; unresolved two-sided edits block push so local old content cannot overwrite Drive edits.
- **Always-on excludes**: `<workspace>/.dotfiles/` and `<workspace>/inbox/gdrive/` are anchored-excluded from push so the per-workspace store and intake staging area never round-trip to mirror — regardless of operator excludes.
- **Post-push baseline refresh**: a successful push rebuilds `baseline.manifest` from files present on both local and mirror, excluding Git-tracked files and using sha256 fingerprints for stable cross-machine diffs.
- **Optional pull+intake scheduler**: pass `--pull-interval=DUR` to `setup` (e.g. `15m`, `1h`) to deploy a parallel pull+intake unit that pulls tracked payloads first, then stages new Drive-origin files. Pass `0` to remove. The push scheduler is always-on; identifiers `com.dotfiles.gdrive-sync` (push) and `com.dotfiles.gdrive-sync-intake` (pull+intake) so both coexist on the same machine.
- **Four-layer excludes**: (1) `.dotfiles/gdrive-sync/exclude.txt` (writable static baseline) + (2) `.dotfiles/gdrive-sync/ignore.txt` (user-supplied additive layer) + (3) per-run dynamic shared-folder list — Drive shortcuts auto-detected via `.shortcut-targets-by-id/` and `Shared drives/` symlink targets, plus an operator-curated manual list managed via `dot gdrive-sync shared add/remove` + (4) `--no-links` (skip all symlinks). `.gitignore` is intentionally not used as a sync filter because gitignored binaries are a primary gdrive-sync use case.
- **Migration gate**: refuses to run while legacy symlinks (`.gdrive`, `inbox/downloads`, `inbox/incoming`) are still in place — point user to `migrate`.
- **Pause gate**: `migrate` leaves `Paused=true` so the operator verifies first; `resume` clears it.
- **Shared-drive refusal**: refuses to sync if `mirror_path` resolves under a Drive `Shared drives/` root — workspace-authoritative semantics would propagate deletions into a team drive.
- **Conflict capture**: pull conflicts save Drive copies under `.sync-conflicts/<RFC3339-ts>/from-gdrive/`; push-side rsync overwrites are backed up under `.sync-conflicts/<RFC3339-ts>/from-workspace/`.
- **Safety cap**: `--max-delete=1000` (configurable) aborts runaway push deletions when delete propagation is on.
- **Stale-aware lock**: PID file inside `~/Library/Caches/dotfiles/gdrive-sync.lock`; signal-0 probes detect crashed-process locks.

> Auto-push runs every Interval seconds via launchd (macOS) or systemd timer (Linux); default 5m. Auto pull+intake is opt-in via `--pull-interval`. Distinct identifiers from rsync's scheduler (`com.dotfiles.gdrive-sync` vs `com.dotfiles.workspace-sync`) so both can coexist.

### `dotfiles clone`

Safe workspace sync with Google Drive via `rclone copy --update`. Default is **pull only** (safe for consumer machines). Explicit `push` or `all` for uploads.

```bash
dotfiles clone setup           # install rclone, configure remote, deploy filter & scheduler
dotfiles clone                 # pull only: remote → local (default, safe)
dotfiles clone pull            # pull only (explicit)
dotfiles clone push            # push only: local → remote
dotfiles clone all             # pull then push (bidirectional)
dotfiles clone status          # show sync state, mount status, last stats
dotfiles clone log [N]         # tail last N sync log lines (default 50)
dotfiles clone skip            # list files auto-skipped due to permission errors
dotfiles clone skip clear      # reset skip list to retry all files
dotfiles clone connect         # configure a new Google Drive remote
dotfiles clone reconnect       # refresh expired authentication
dotfiles clone mount           # mount remote as FUSE filesystem (live, no local storage)
dotfiles clone mount --unmount # unmount the FUSE filesystem
dotfiles clone pause           # pause auto-sync scheduler
dotfiles clone resume          # resume auto-sync scheduler
```

**Key features:**
- **Skip list**: files that fail with permission errors are auto-added to skip list
- **`--drive-skip-shared-with-me`**: avoids read-only shared folders entirely
- **`--drive-skip-gdocs`**: skips Google Docs native files
- **`-V` / `--verbose`**: streams rclone progress to terminal

> Auto-sync runs every 5 minutes via launchd (macOS) or systemd timer (Linux). Conflicts resolved with `--update` (newer wins).

### `dotfiles version`

Shows version, git commit, Go version, and OS/arch. For dev builds (no ldflags), falls back to Go's embedded VCS info with `-dirty` suffix if the working tree has uncommitted changes.

```bash
dotfiles version
```

```
dotfiles v0.20.0 (1e5900a)        # release build
dotfiles dev (d1877ee-dirty)      # dev build with uncommitted changes
  go:   go1.23.0
  os:   darwin/arm64
```

### `dotfiles open <project>`

Launch or resume a tmux workspace. Auto-registers unregistered project names.

```bash
dotfiles open myproject                         # launch or resume
dotfiles open myproject --layout claude         # override layout
dotfiles open myproject --theme dracula         # override theme
```

> Explicit `open` is required — running `dotfiles <unknown>` no longer auto-routes to `open`. This prevents typos like `dotfiles aply` from silently creating a bogus project.

### `dotfiles register` / `dotfiles unregister`

```bash
dotfiles register myproject .                          # current dir
dotfiles register myproject ~/dev/app --layout claude  # with options
dotfiles unregister myproject
```

### `dotfiles list`

Show registered projects and active tmux sessions.

```bash
dotfiles list     # or: dotfiles ls
```

```
Projects (2):
  * myproject          ~/dev/app           (layout=dev, theme=default)
    server-mon         ~/ops/monitoring    (layout=monitor, theme=nord)
```

### `dotfiles stop`

```bash
dotfiles stop myproject       # with confirmation
dotfiles stop myproject -f    # force
```

### `dotfiles layouts`

| Layout | Panes | Description |
|--------|-------|-------------|
| **dev** (default) | 5 | Claude + monitor + files + lazygit + shell |
| **claude** | 7 | Claude + monitor + files + remote + lazygit + shell + logs |
| **monitor** | 4 | monitor + lazygit + shell + logs |

### `dotfiles doctor`

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

### `dotfiles ai` — AI CLI/Config Helpers + Settings Backup/Restore

Manage assistant helper files and portable user-level AI configuration. This is
separate from app installation: Claude, Codex, ChatGPT, and similar GUI apps are
installed through `dotfiles apps install` from the macOS cask catalog.

```bash
dotfiles ai list                         # helper files, detected CLIs, AI casks
dotfiles ai status                       # live + backup status for managed paths

dotfiles ai backup                       # versioned snapshot under BackupRoot
dotfiles ai backup --tag "pre-migration"
dotfiles ai backup --include-auth        # include auth/local-secret files explicitly
dotfiles ai restore                      # restore latest snapshot
dotfiles ai restore --version latest     # explicit alias for latest.txt
dotfiles ai restore --version 20260502T010203Z
dotfiles ai restore --include-auth       # restore auth/local-secret files explicitly

dotfiles ai export ~/workspace/secrets/ai-config.tar.gz
dotfiles ai import ~/workspace/secrets/ai-config.tar.gz
```

The `ai` module writes shell/config helper files:

| Path | Purpose |
|------|---------|
| `~/.config/shell/30-ai.sh` | Claude Code env, GitHub Models aliases, Fabric alias, GPU helper aliases |
| `~/.config/claude/settings.json` | Minimal dotfiles-managed Claude settings |

Portable backup includes Claude/Codex settings, MCP config, agents, hooks,
prompts, rules, and user skills. It excludes auth tokens, local overrides,
caches, logs, sessions, histories, telemetry, sqlite DBs, plugin caches, and
generated/system skill bundles by default. Use `--include-auth` only when you
explicitly want known auth/local-secret files included.

### `dotfiles ws` — Dual-Workspace Folder Ops

Operate on both `~/workspace/work/` (git-tracked text) and `~/gdrive-workspace/work/` (Drive binaries) simultaneously to keep their folder structures in sync.

```bash
dotfiles ws init                          # clone configured repos (work, vault) recursively
dotfiles ws init --force                  # re-clone over populated targets (destructive; prompts)
dotfiles ws mkdir projects/rise-y2        # create on both sides
dotfiles ws mv projects/rise projects/rise-y1  # rename on both sides
dotfiles ws rm scratch --recursive        # remove from both sides
dotfiles ws audit                         # report structural mismatches
dotfiles ws audit projects                # limit scope
dotfiles ws reconcile                     # interactive resolve (copy/delete/skip)
dotfiles ws reconcile --yes               # bulk copy (never deletes)
```

Top-level aliases: `dot ws-mkdir`, `dot ws-mv`, `dot ws-rm`, `dot ws-audit`, `dot ws-reconcile`.

### `dotfiles apps` — macOS Cask Install + Settings Backup/Restore

Manage macOS cask applications and their per-app settings (plists, `Application Support/`,
sandbox `Containers`, `Group Containers`). macOS-only; no-ops on Linux.

```bash
dotfiles apps list                         # show catalog: groups + ★ defaults + ✓ installed
dotfiles apps install                      # interactive MultiSelect + optional "save to state"
dotfiles apps install raycast obsidian     # explicit tokens (skip picker)
dotfiles apps install --defaults           # catalog's default set
dotfiles apps install --all                # every cask in the catalog
dotfiles apps install --select             # force picker even when state has a list
dotfiles apps install --no-save            # one-off install without persisting selection
dotfiles apps status                       # install ✓/· + backup path counts per app
dotfiles apps backup                       # snapshot settings for BackupApps ∩ manifest
dotfiles apps backup raycast hazel         # explicit tokens
dotfiles apps backup --all --to <root>     # override backup root, back up every entry
dotfiles apps restore                      # restore from the configured root (confirms first)
dotfiles apps restore --from <root> moom
```

**Two independent lists** on the user state:

- **Install list** (`modules.macapps.casks` + `casks_extra`) — drives `dotfiles apps install`.
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

The embedded cask catalog (`internal/config/catalog/macos-apps.yaml`) groups ~60
apps across Security, Knowledge, Browsers, Terminal & Editor, AI, Communication,
Productivity utilities, Capture & Dictation, Files, Media, Dev, System, Writing.
The `defaults:` section defines the preselected 20-app bootstrap set.

### `dotfiles profile` — Versioned Profile Snapshots

Snapshot the user-level profile (`~/.config/dotfiles/config.yaml`, install / backup
cask lists, and optionally `~/.ssh/age_key*`) into host-scoped, timestamped
version directories under the shared backup root.

```bash
dotfiles profile backup                            # new snapshot
dotfiles profile backup --tag "pre-migration"      # with label
dotfiles profile backup --include-secrets          # also copy ~/.ssh/age_key*
dotfiles profile list                              # newest-first; ★ marks latest
dotfiles profile restore                           # restore the latest version
dotfiles profile restore --version 20260416T000856Z
dotfiles profile restore --include-secrets         # include ~/.ssh/age_key*
dotfiles profile restore --no-state                # skip config.yaml copy
dotfiles profile prune --keep 5                    # delete older snapshots
dotfiles profile backup --to <root>                # override backup root
```

**Layout** (one Drive folder holds app settings, profile snapshots, and AI settings):

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
machine the first `profile restore` boots state before you've run `dotfiles init`.

### Backup-root resolution (shared by `apps`, `profile`, and `ai`)

Precedence, highest wins:

1. `--to <path>` / `--from <path>` flag.
2. `modules.macapps.backup_root` in user state.
3. Auto-detected Drive secrets folder (`<drive>/secrets/dotfiles-backup`).
4. Local fallback: `~/.local/share/dotfiles/backup`.

Set it once in `dotfiles init` — all backup subcommands read the same value so
your Drive folder holds everything.

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
| **node** | full | pnpm store relocation outside Google Drive (~/.config/pnpm/npmrc) |
| **git** | minimal | git config, aliases, global ignore |
| **ssh** | minimal | SSH config, config.d includes |
| **terminal** | minimal | starship prompt (minimal / rich selectable), Warp theme (macOS) |
| **tmux** | full | tmux.conf (256color, vim keys, C-a prefix) |
| **workspace** | full | Dual-workspace: git repo clone, gh auth, symlink federation (Drive, vault, inbox) |
| **ai** | full | AI CLI/config helpers, Claude/Codex settings backup |
| **fonts** | full | Nerd Font download from GitHub Releases |
| **macapps** | full (darwin) | Install selected Homebrew casks from the embedded catalog |
| **conda** | full | Conda/Mamba shell initialization |
| **gpg** | full | GPG agent + git commit signing |
| **secrets** | full | Age-encrypted SSH keys and shell secrets |

### Prompt Styles

The terminal module deploys a Starship prompt config. Two styles are selectable
during `dotfiles init` or `dotfiles reconfigure`:

| Style | Default for | Character | Info shown |
|-------|-------------|-----------|------------|
| **minimal** | minimal, server | `>` | truncated path, branch, dirty marker |
| **rich** | full | `→` | time, user, path, host, branch+status, language versions, duration |

```bash
dotfiles apply --module terminal     # deploys the selected style
dotfiles reconfigure                 # switch between minimal ↔ rich
```

Config key: `modules.terminal.prompt_style` (state: `modules.prompt_style`).

### Packages

**minimal** (16):
`git`, `git-lfs`, `gh`, `age`, `rsync`, `fzf`, `ripgrep`, `fd`, `bat`, `jq`, `yq`, `direnv`, `zoxide`, `eza`, `starship`, `curl`

**full** adds (+12):
`btop`, `lazygit`, `rclone`, `yazi`, `glow`, `csvlens`, `chafa`, `fnm`, `uv`, `pipx`, `tmux`, `gnupg`

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
| **minimal** | 5 | 16 | Lightweight dev setup |
| **full** | 14 | 28 | Complete workstation (macapps enabled on darwin) |
| **server** | 8 | 20 | GPU/DGX server |

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
    gdrive: "~/My Drive (hello@jeju.ai)"
    gdrive_symlink: "~/gdrive-workspace"
    repos:
      - name: work
        remote: "git@github.com:user/work.git"
      - name: vault
        remote: "git@github.com:user/vault.git"
  ai:
    enabled: true
  warp: false
  prompt_style: rich    # "minimal" or "rich"
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
  sync:
    remote: "gdrive"
    path: "work"
    interval: 300
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
├── cmd/dotfiles/main.go          # Entry point (ldflags: version, commit)
├── internal/
│   ├── cli/                      # Cobra commands
│   │   ├── open.go               # dot open — workspace launcher
│   │   ├── sync_cmd.go           # dot sync — rsync binary sync
│   │   ├── clone_cmd.go          # dot clone — rclone Google Drive sync
│   │   ├── clean_cmd.go          # dot clean — workspace junk cleanup
│   │   ├── status_cmd.go         # dot status — unified dashboard
│   │   ├── drive_exclude.go      # dot drive-exclude — xattr management
│   │   └── workspace_cmds.go     # stop, list, register, unregister, layouts, doctor
│   ├── config/                   # Config struct, loader, detector, state
│   │   └── profiles/             # Embedded YAML profiles (go:embed)
│   ├── aisettings/               # AI assistant settings backup/restore/export/import
│   ├── clean/                    # Workspace cleanup scanner + deletion
│   ├── driveexclude/             # Google Drive xattr exclusion logic
│   ├── exec/                     # Runner (dry-run), Brew wrapper
│   ├── module/                   # 14 module implementations (macapps darwin-only)
│   ├── rclone/                   # rclone Google Drive sync (used by clone)
│   │   ├── sync.go               # Config, pull/push/mount
│   │   ├── rclone.go             # Install, remote config, access check
│   │   ├── scheduler.go          # Scheduler types
│   │   ├── scheduler_darwin.go   # macOS launchd
│   │   └── scheduler_other.go    # Linux systemd
│   ├── rsync/                    # rsync binary sync (used by sync)
│   │   ├── rsync.go              # Config, pull/push, lock
│   │   ├── helpers.go            # Install, SSH check
│   │   ├── status.go             # Status, log parsing
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
| **unit** | ubuntu-latest, macos-latest | Go unit tests + coverage |
| **integration** | ubuntu-{22.04,24.04} × {minimal,full,server} + GPU sim | Docker-based profile tests |
| **module** | 8 modules × ubuntu-22.04 | Individual module tests |
| **scenario** | 9 E2E scenarios | dry-run, idempotency, server, upgrade, home-override, workspace, drive-exclude, sync |

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
dotfiles init --yes     # auto-selects 'server' profile
dotfiles apply --yes    # packages (incl. rsync), shell, git, ssh, terminal, tmux, ai, conda
```

Or import config from your workstation:

```bash
dotfiles init --from ~/workspace/secrets/dotfiles-config.yaml
dotfiles apply --yes
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
