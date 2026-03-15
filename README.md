# dotfiles-v2

Declarative user environment management — a single Go binary that replaces chezmoi.
macOS + Linux. Modular, profile-based, AI-ready.

## Quick Start

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/install.sh | bash

# Interactive setup
dotfiles init

# Apply configuration
dotfiles apply
```

## Modules

| Module | Description |
|--------|-------------|
| packages | Homebrew formula installation |
| shell | zsh, Oh My Zsh, plugins, shell config files |
| git | git config, aliases, global ignore |
| ssh | SSH config with config.d includes |
| terminal | starship prompt, Warp theme |
| tmux | tmux.conf with vim bindings |
| workspace | Symlink federation (Google Drive, vault) |
| ai-tools | Claude Code, GitHub Models aliases |
| fonts | Nerd Font auto-install from GitHub Releases |
| conda | Conda/Mamba shell initialization |
| gpg | GPG agent + git commit signing |
| secrets | Age-encrypted SSH keys and shell secrets |

## Profiles

- **minimal** — Core tools (15 packages): packages, shell, git, ssh, terminal
- **full** (extends minimal) — Everything (26+ packages): + workspace, ai-tools, tmux, fonts, conda, gpg, secrets

## Commands

```bash
dotfiles init                    # Interactive TUI setup
dotfiles apply [--dry-run]       # Apply configuration
dotfiles check                   # Show current vs desired state
dotfiles diff                    # Preview pending changes
dotfiles update                  # Git pull + re-apply
dotfiles reconfigure             # Re-run setup prompts
dotfiles secrets init            # Encrypt SSH key + shell secrets
dotfiles secrets backup <dir>    # Back up encrypted files
dotfiles secrets restore <dir>   # Decrypt and restore
dotfiles secrets status          # Check secrets state
dotfiles version                 # Version info
```

### Flags

```
--profile    Profile name (minimal, full)
--module     Run specific modules only
--dry-run    Preview without changes
--yes        Unattended mode
--config     Custom config YAML path
```

## Configuration

User settings stored in `~/.config/dotfiles/config.yaml` (created by `dotfiles init`):

```yaml
name: "Your Name"
email: "you@example.com"
github_user: "username"
timezone: "Asia/Seoul"
profile: "full"
```

## Architecture

Same modular Go architecture as [rootfiles-v2](https://github.com/entelecheia/rootfiles-v2):

```
rootfiles-v2 (root, server)     dotfiles-v2 (user, workstation)
━━━━━━━━━━━━━━━━━━━━━━━━━━━     ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Packages (APT), users, SSH       Packages (Homebrew), shell, git
Docker, GPUs, tunnels            Terminal, fonts, AI tools
Locale, firewall, storage        Workspace, secrets, tmux
```

## Development

```bash
make build      # Build binary
make test       # Run tests
make install    # Install to ~/.local/bin/
```

## License

MIT
