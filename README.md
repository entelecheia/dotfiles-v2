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

### Setup

```bash
dotfiles init       # interactive TUI — name, email, profile, modules
dotfiles apply      # apply all enabled modules
```

### Workspace

```bash
dot myproject       # launch or resume a multi-panel tmux workspace
dot myproject       # SSH dropped? just run it again — resumes exactly
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

### `dotfiles init`

Interactive TUI setup. Collects user info and saves to `~/.config/dotfiles/config.yaml`.

```bash
dotfiles init
```

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

### `dotfiles upgrade`

Self-upgrading binary. Downloads the latest release from GitHub.

```bash
dotfiles upgrade          # download & install
dotfiles upgrade --check  # check only
```

### `dotfiles reconfigure`

Re-run init prompts with current values as defaults, then optionally re-apply.

```bash
dotfiles reconfigure
```

### `dotfiles secrets`

Manage age-encrypted secrets (SSH keys, shell secrets).

```bash
dotfiles secrets init              # encrypt SSH key + shell secrets
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

Supported patterns: `node_modules`, `.pnpm`, `.next`, `.nuxt`, `.astro`, `.svelte-kit`, `.parcel-cache`, `.turbo`, `.angular`, `.webpack`, `.venv`, `__pycache__`, `.mypy_cache`, `.pytest_cache`

> macOS only for real xattr. `--dry-run` works on all platforms.

### `dotfiles sync`

Bidirectional workspace sync with Google Drive via rclone bisync. Selective sync with `--filter-from` excludes node_modules, build caches, and other heavy directories.

```bash
dotfiles sync setup       # install rclone, configure remote, deploy filter & scheduler
dotfiles sync             # trigger immediate bisync
dotfiles sync status      # show sync status and scheduler state
dotfiles sync log         # last 50 lines of sync log
dotfiles sync log 100     # last 100 lines
dotfiles sync pause       # pause auto-sync scheduler
dotfiles sync resume      # resume auto-sync scheduler
```

`setup` handles the full flow: rclone installation (via Homebrew), Google Drive remote configuration, filter file deployment, launchd/systemd scheduler registration, and optional initial `--resync`.

> Auto-sync runs every 5 minutes via launchd (macOS) or systemd timer (Linux). Conflicts resolved with `--conflict-resolve newer`.

### `dotfiles version`

```bash
dotfiles version
```

```
dotfiles v0.9.0 (47d7aa7)
  go:   go1.23.0
  os:   darwin/arm64
```

### `dotfiles open` / `dot <project>`

Launch or resume a tmux workspace. Auto-registers unregistered project names.

```bash
dot myproject                         # implicit: dot open myproject
dot open myproject --layout claude    # override layout
dot open myproject --theme dracula    # override theme
```

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

**minimal** (15):
`git`, `git-lfs`, `gh`, `age`, `fzf`, `ripgrep`, `fd`, `bat`, `jq`, `yq`, `direnv`, `zoxide`, `eza`, `starship`, `curl`

**full** adds (+11):
`btop`, `lazygit`, `yazi`, `glow`, `csvlens`, `chafa`, `fnm`, `uv`, `pipx`, `tmux`, `gnupg`

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
| **minimal** | 5 | 15 | Lightweight dev setup |
| **full** | 13 | 26+ | Complete workstation |
| **server** | 8 | 19 | GPU/DGX server |

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
│   ├── cli/                      # Cobra commands (15 files)
│   │   ├── open.go               # dot open — workspace launcher
│   │   ├── sync_cmd.go           # dot sync — rclone bisync management
│   │   ├── drive_exclude.go      # dot drive-exclude — xattr management
│   │   └── workspace_cmds.go     # stop, list, register, unregister, layouts, doctor
│   ├── config/                   # Config struct, loader, detector, state
│   │   └── profiles/             # Embedded YAML profiles (go:embed)
│   ├── driveexclude/             # Google Drive xattr exclusion logic
│   ├── exec/                     # Runner (dry-run), Brew wrapper
│   ├── module/                   # 13 module implementations
│   ├── sync/                     # rclone bisync: runner, scheduler, status
│   │   ├── sync.go               # Config resolution, Bisync runner
│   │   ├── rclone.go             # Install, remote config, access check
│   │   ├── scheduler.go          # Scheduler types
│   │   ├── scheduler_darwin.go   # macOS launchd
│   │   └── scheduler_other.go   # Linux systemd
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
dotfiles apply --yes    # packages, shell, git, ssh, terminal, tmux, ai-tools, conda
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
