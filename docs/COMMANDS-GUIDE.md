# Command guide

The generated [command reference](commands/) is canonical for command syntax and flags. This companion preserves cross-command operating guidance that cannot be generated from a single Cobra help string.

## Setup and safe application

`dot init --from <file>` pre-populates identity from another machine, while workspace path and terminal are confirmed locally. Setup covers identity, timezone, profile, GPU detection, module opt-ins, SSH key, workspace repositories, vault location, and GitHub authentication.

For a cautious apply, run `dot preflight --check-only`, `dot preflight`, `dot check`, and `dot apply --dry-run` before a selected module. Files are backed up under `~/.local/share/dotfiles/backup/` before overwrite; an identical SHA256 is not overwritten. `dot config export <file>` creates the portable YAML consumed by `dot init --from <file>`. `dot update` verifies the release checksum and binary; use `dot ai update` for AI CLIs and plugins.

## Secrets and migration

`dot secrets restore` decrypts to a 0600 temporary file and atomically renames it into place. A differing target is retained as `<dest>.bak-<timestamp>`; identical files remain unchanged. One-stop backup and restore coordinate profile, apps, AI, and secrets, can run `dot apply` after profile state, and retain pre-restore copies.

## Workspace commands

`dot open` requires an explicit subcommand, so `dot aply` cannot accidentally create a project. `dot layouts` offers `dev`, `claude`, and `monitor`; `dot doctor` reports tool paths and optional-tool availability.

For dev builds (no ldflags), `dot version` falls back to Go's embedded VCS info with a `-dirty` suffix if the working tree has uncommitted changes.

## Cleanup and indexing

`dot clean` always protects `_sys/`. Its safe set includes node modules, Python caches/environments, `.next`, `.cache`, and `.DS_Store`; `--all` adds `dist`, `build`, `out`, and `target` because those can hold deliverables.

`dot noindex` walks project roots but marks cache roots at the top. `~/.claude` is excluded so plans and skills stay searchable. `build` and `out` are not marked because finished deliverables need to be findable. A marker affects only future Spotlight indexing; use `sudo mdutil -E /` to remove existing entries.

## Tunnel operations

`dot tunnel setup` renders explicit `--config /etc/cloudflared/config.yml tunnel run` arguments, avoiding the macOS service-install form that omits the run command. Cloudflare Access policy creation stays in the dashboard; then run `dot tunnel client add <hostname>` on clients.

## Guard operation and limits

`dot guard` edits only entries marked `# dot-guard`; a semantic rewrite may sort JSON keys but does not alter other values. `enable` and `disable` apply to new Claude Code sessions, while `freeze` and `unfreeze` update live state.

The hook fails open if the dot binary is missing or moved. `dot guard status` reports that condition, and `dot guard enable` must be re-run after moving the binary. It is a guardrail, not a sandbox: it only inspects the Bash tool and shell writes are outside the freeze boundary.

## Sync and peer operating model

`dot sync` sends a Git-aware union: root-repository tracked files plus approved untracked binary extensions, minus submodules, junk, and deny-by-default secrets. `allow.txt` records an explicit secret exception. A baseline-unknown mirror file stages under `inbox/gdrive/` for manual routing.

Run `dot sync names normalize --profile=peer --dry-run` and then `--yes` for selected NFC-to-NFD names. It refuses invalid UTF-8, symlinks, and canonical collisions, moves deepest paths first, and rolls back completed moves on error. Profiles use stores below `<workspace>/.dotfiles/<profile>/`; the peer profile shares the cloud workspace lock while other custom profiles do not.

`dot peer` is two-way peer sync, not the cloud mirror. Its deletion propagation quarantines baseline-recorded removals, but host paths remain additive. Keep the same dot version on both machines before scheduling because older peers do not understand tombstones. The coordinator records simultaneous conflicts in `peer-conflicts.log`; an offline peer exits zero for safe scheduled laptop runs.

Peer host paths exclude `known_hosts`, `~/.maru/env`, plugin caches, `~/.codex/auth.json`, and `~/.codex/config.toml`: they are trust, absolute-runtime, or keychain-coupled state rather than portable workspace settings.

## AI helpers and portability

`dot ai` manages helper files and portable AI configuration, not GUI app installation; use `dot apps install` for casks. The `*-yolo` helpers forward full-access flags and belong only in trusted workspaces. HUD apply owns only the `# dot-hud` Claude statusLine. A foreign owner is a conflict unless `--force` is supplied; `--persist` records the desired HUD.

Portable AI snapshots exclude skill roots, auth by default, caches, histories, databases, and machine-local plugin state; archives reject symlinks, hardlinks, devices, and link-pivot paths. `~/.codex/config.toml` is normally kept live on restore because Codex rewrites it and hashes MCP credentials into the Keychain. `dot ai skills` is diagnose-only: Maru owns runtime skill symlinks and tool federation.

## Apps and profile snapshots

`dot apps` manages cask installation and per-host settings. A profile snapshot contains config, install and backup lists, metadata, and optional age keys at `<backup-root>/profiles/<hostname>/<version>/`.

## Backup-root resolution

`dot apps`, `dot profile`, and `dot ai` share this precedence: an explicit command flag, saved `BackupRoot`, a cloud root detected from a `secrets/` marker, then the local default. It spans commands and has no generated page.

## Global flags

All commands inherit `--config`, `--dry-run`, `--home`, `--module`, `--profile`, and `--yes`. Generated pages carry the inherited option block.
