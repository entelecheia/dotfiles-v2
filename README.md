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

- **Workspace** — ai-workspace symlinks, navigation aliases
- **AI Tools** — Claude Code, GitHub Models, Ollama config
- **Warp** — Warp Terminal themes (macOS)

## Profiles

- `minimal` — Core tools only (15 packages)
- `full` — Everything (35+ packages)

## Encryption (optional)

```bash
age-keygen -o ~/.config/chezmoi/key.txt
chezmoi add --encrypt ~/.ssh/id_ed25519
```

## Daily use

```bash
chezmoi update    # pull + apply
chezmoi diff      # preview changes
chezmoi add FILE  # track a new file
```

## License

MIT
