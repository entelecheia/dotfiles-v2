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

```bash
# On the existing machine — export config
dotfiles config export ~/ai-workspace/secrets/dotfiles-config.yaml

# On the new machine — import and review
dotfiles init --from ~/ai-workspace/secrets/dotfiles-config.yaml
dotfiles apply
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
dotfiles init --from ~/ai-workspace/secrets/config.yaml    # import from another machine
dotfiles init --yes                                        # unattended with defaults
```

Use `--from` to import settings from another machine's exported config. Identity fields (name, email, SSH key) are pre-populated; machine-specific settings (workspace path, terminal) are confirmed interactively.

Prompts for:
- Name, Email, GitHub username
- Timezone (default: `Asia/Seoul`)
- Profile (`minimal` / `full` / `server`)
- GPU/CUDA auto-detection (suggests `server` when NVIDIA GPU detected)
- Module opt-ins: workspace, AI tools, Warp, fonts
- SSH key name (auto-derived from GitHub username)

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
dotfiles config export ~/ai-workspace/secrets/config.yaml    # save to file
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
dotfiles drive-exclude scan              # scan ~/ai-workspace (default)
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
dotfiles clean ~/projects     # custom path (default: ~/ai-workspace/work)
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
dotfiles v0.14.0 (9fc1890)        # release build
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
workspace → ai-tools → fonts → conda → gpg → secrets
```

### Module Details

| Module | Profile | Description |
|--------|---------|-------------|
| **packages** | minimal | Homebrew formula installation |
| **shell** | minimal | zsh, Oh My Zsh, plugins, config files |
| **node** | minimal | .npmrc, pnpm store relocation outside Google Drive |
| **git** | minimal | git config, aliases, global ignore |
| **ssh** | minimal | SSH config, config.d includes |
| **terminal** | minimal | starship prompt, Warp theme (macOS) |
| **tmux** | full | tmux.conf (256color, vim keys, C-a prefix) |
| **workspace** | full | Symlink federation (Google Drive, vault) |
| **ai-tools** | full | Claude Code config, GitHub Models aliases |
| **fonts** | full | Nerd Font download from GitHub Releases |
| **conda** | full | Conda/Mamba shell initialization |
| **gpg** | full | GPG agent + git commit signing |
| **secrets** | full | Age-encrypted SSH keys and shell secrets |

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
| **full** | 13 | 28 | Complete workstation |
| **server** | 8 | 20 | GPU/DGX server |

**server**: Extends `minimal` + tmux, ai-tools, conda. Disables workspace, fonts, gpg, secrets. Auto-suggested when NVIDIA GPU or CUDA is detected.

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
    path: "~/ai-workspace"
    gdrive: "~/My Drive (hello@jeju.ai)"
  ai_tools: true
  warp: false
  fonts:
    family: "FiraCode"
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
| `GITHUB_TOKEN` | GitHub API token for `upgrade` |

---

## Architecture

Same modular Go architecture as [rootfiles-v2](https://github.com/entelecheia/rootfiles-v2).

```
rootfiles-v2 (root, server)     dotfiles-v2 (user, workstation)
━━━━━━━━━━━━━━━━━━━━━━━━━━━     ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Packages (APT), users, SSH       Packages (Homebrew), shell, git
Docker, GPUs, tunnels            Terminal, fonts, AI tools
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
│   ├── clean/                    # Workspace cleanup scanner + deletion
│   ├── driveexclude/             # Google Drive xattr exclusion logic
│   ├── exec/                     # Runner (dry-run), Brew wrapper
│   ├── module/                   # 13 module implementations
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
dotfiles apply --yes    # packages (incl. rsync), shell, git, ssh, terminal, tmux, ai-tools, conda
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
