# dotfiles-v2

Cross-platform dotfiles managed by [chezmoi](https://chezmoi.io).
macOS + Ubuntu. Lightweight, modular, AI-ready.

## Quick Start

### One-line install

```bash
curl -sL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/bootstrap.sh | sh
```

### Fork workflow (recommended)

1. Fork this repo
2. Run: `DOTFILES_REPO="youruser/dotfiles-v2" bash <(curl -sL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/bootstrap.sh)`
3. Customize: `chezmoi edit-config`

### Unattended (CI/Docker)

```bash
CHEZMOI_ARGS="--no-tty --promptDefaults" bash <(curl -sL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/bootstrap.sh)
```

## What's included

| Category | Tools |
|----------|-------|
| Shell | zsh, oh-my-zsh, starship prompt |
| Search | fzf, ripgrep, fd |
| Files | eza, bat, yazi |
| Git | lazygit, gh |
| Dev | fnm (Node), uv (Python), pipx |
| AI | Claude Code config, gh-models aliases |
| Monitor | btop |

## Modules (opt-in at `chezmoi init`)

- **Workspace** — workspace symlinks, navigation aliases
- **AI Tools** — Claude Code, GitHub Models, Ollama config
- **Warp** — Warp Terminal themes (macOS)

## Profiles

- `minimal` — Core tools only (15 packages)
- `full` — Everything (35+ packages)

## Secrets Management

Secrets (SSH keys, API tokens) are **not** stored in the git repo — neither plaintext nor encrypted. Instead, they are managed locally with [age](https://github.com/FiloSottile/age) encryption.

### Setup

Age key auto-detection searches these paths in order:

1. `~/.ssh/age_key_<github-username>`
2. `~/.config/chezmoi/key.txt`

If no key exists yet:

```bash
age-keygen -o ~/.config/chezmoi/key.txt
```

Then re-run `chezmoi init` to configure recipients.

### Encrypt & manage secrets

```bash
secrets init                    # encrypt SSH key + shell secrets
secrets backup ~/Dropbox/keys   # copy .age files to backup
secrets restore ~/Dropbox/keys  # restore on a new machine
secrets status                  # check what's in place
```

Or via Make:

```bash
make secrets-init
make secrets-status
make secrets-backup DEST=~/backup
make secrets-restore SRC=~/backup
```

### Where secrets live

| Item | Location | Git tracked |
|------|----------|-------------|
| age identity (private) | `~/.ssh/age_key_<user>` or `~/.config/chezmoi/key.txt` | No |
| Encrypted backups (.age) | `~/.local/share/chezmoi-secrets/` | No |
| SSH private key | `~/.ssh/id_ed25519_<user>` | No |
| Shell secrets | `~/.config/shell/90-secrets.sh` | No |

## Daily use

```bash
chezmoi update    # pull + apply
chezmoi diff      # preview changes
chezmoi add FILE  # track a new file
```

## License

MIT
