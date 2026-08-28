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
dot ai backup                         # portable Claude/Codex/Copilot/Antigravity/Maru/MCP settings

# On the new machine (Drive already mounted)
dot profile restore --include-secrets # restores ~/.config/dotfiles + ~/.ssh/age_key*
dot apply                             # brew formulas + casks from install list
dot apps restore                      # plists, Application Support, containers
dot ai restore                        # Claude/Codex/Copilot/Antigravity/MCP settings
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

### Sync secret policy

`dot sync` excludes credential-bearing paths by default, including `.ssh`,
`.gnupg`, common cloud and package-manager credentials, and private-key and
keystore extensions. An operator can explicitly re-include a path in
`allow.txt`; status and dry-run previews show every such sensitive override
without changing the requested transfer decision.

### Build from source

```bash
git clone https://github.com/entelecheia/dotfiles-v2.git && cd dotfiles-v2
make build          # → bin/dot
make install        # → ~/.local/bin/dot + ~/.local/bin/dotfiles (symlink)
```

---

For the full command reference, see [docs/commands](docs/commands/). Cross-command operating guidance, backup-root resolution, and persistent flags are in the [command guide](docs/COMMANDS-GUIDE.md). Configuration ownership is documented in [boundaries](docs/BOUNDARIES.md).

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
| **ai** | full | AI CLI/config helpers, Claude/Codex/Copilot/Cursor/Kiro/Kimi/Antigravity/Aider/Maru settings backup, optional HUD |
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
| `DOT_SCHEMA_FORCE` | Set to `1` to overwrite a state file written by a newer `dot`, dropping any keys this binary does not know |

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

Source builds require Go 1.25.8 or newer.

```bash
make build      # build binary
make test       # run tests
make lint       # lint
make clean      # clean artifacts
make install    # install to ~/.local/bin/
```

## License

MIT
