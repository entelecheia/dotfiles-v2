#!/usr/bin/env bash
set -euo pipefail

REPO="${DOTFILES_REPO:-entelecheia/dotfiles-v2}"
BRANCH="${DOTFILES_BRANCH:-main}"

GREEN='\033[0;32m'
NC='\033[0m'
info() { echo -e "${GREEN}[+]${NC} $*"; }

OS=$(uname -s | tr '[:upper:]' '[:lower:]')

# ── 1. Homebrew 설치 ──
if ! command -v brew &>/dev/null; then
  info "Installing Homebrew..."
  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

  # PATH 설정
  if [ "$OS" = "darwin" ]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
  else
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
  fi
fi

# ── 2. Linux 전제조건 ──
if [ "$OS" = "linux" ]; then
  sudo apt-get update -qq
  sudo apt-get install -y -qq build-essential procps curl file git zsh
fi

# ── 3. Chezmoi 설치 + init ──
if ! command -v chezmoi &>/dev/null; then
  info "Installing chezmoi..."
  brew install chezmoi
fi

# ── 4. 초기화 ──
CHEZMOI_ARGS="${CHEZMOI_ARGS:-}"
info "Initializing dotfiles from $REPO..."
chezmoi init "$REPO" --branch "$BRANCH" --apply $CHEZMOI_ARGS

info "Done! Restart shell: exec zsh"
