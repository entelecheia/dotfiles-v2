#!/usr/bin/env bash
set -euo pipefail

REPO="entelecheia/dotfiles-v2"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# --- Helpers ---

if [[ -z "${NO_COLOR:-}" ]] && [[ -t 1 ]]; then
  _green='\033[0;32m' _yellow='\033[0;33m' _red='\033[0;31m' _reset='\033[0m'
else
  _green='' _yellow='' _red='' _reset=''
fi

info() { printf "${_green}[+]${_reset} %s\n" "$*"; }
warn() { printf "${_yellow}[!]${_reset} %s\n" "$*"; }
err()  { printf "${_red}[x]${_reset} %s\n" "$*" >&2; }

# --- Step 1: Detect OS and arch ---

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) err "Unsupported architecture: $ARCH"; exit 1 ;;
esac

info "Detected ${OS}/${ARCH}"

# --- Step 2: Ensure Homebrew ---
# macOS: Homebrew's installer also handles Xcode Command Line Tools automatically.
# Linux: Linuxbrew provides consistent package management for dot apply.

if [[ "$OS" == "darwin" ]]; then
  if [[ "$ARCH" == "arm64" ]]; then
    BREW_PREFIX="/opt/homebrew"
  else
    BREW_PREFIX="/usr/local"
  fi
elif [[ "$OS" == "linux" ]]; then
  BREW_PREFIX="/home/linuxbrew/.linuxbrew"
fi

if command -v brew &>/dev/null; then
  info "Homebrew: $(brew --version 2>/dev/null | head -1)"
elif [[ -n "${BREW_PREFIX:-}" ]] && [[ -x "$BREW_PREFIX/bin/brew" ]]; then
  eval "$("$BREW_PREFIX/bin/brew" shellenv)"
  info "Homebrew: $(brew --version 2>/dev/null | head -1)"
elif [[ -n "${BREW_PREFIX:-}" ]]; then
  if [[ "$OS" == "darwin" ]]; then
    info "Installing Homebrew (includes Xcode Command Line Tools)..."
  else
    info "Installing Homebrew (Linuxbrew)..."
    if command -v apt-get &>/dev/null; then
      info "Installing Linuxbrew prerequisites..."
      sudo apt-get update -qq
      sudo apt-get install -y build-essential procps curl file git
    fi
  fi
  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  if [[ -x "$BREW_PREFIX/bin/brew" ]]; then
    eval "$("$BREW_PREFIX/bin/brew" shellenv)"
  fi
  info "Homebrew installed"
fi

# --- Step 3: Download dot binary ---

# Support GITHUB_TOKEN for API rate limits
AUTH_HEADER=()
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
  AUTH_HEADER=(-H "Authorization: token $GITHUB_TOKEN")
fi

LATEST=$(curl -fsSL "${AUTH_HEADER[@]}" "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')

if [[ -z "$LATEST" ]]; then
  err "Failed to fetch latest release from GitHub"
  exit 1
fi

VERSION="${LATEST#v}"

# Skip download if already at latest version
if [[ -x "$INSTALL_DIR/dotfiles" ]]; then
  CURRENT=$("$INSTALL_DIR/dotfiles" --version 2>/dev/null || echo "")
  if [[ "$CURRENT" == *"$VERSION"* ]]; then
    info "dotfiles v${VERSION} already installed, skipping download"
  else
    info "Upgrading dotfiles to v${VERSION}..."
    URL="https://github.com/${REPO}/releases/download/${LATEST}/dotfiles_${VERSION}_${OS}_${ARCH}.tar.gz"
    curl -fsSL "$URL" | tar xz -C "$INSTALL_DIR" dotfiles
    chmod +x "$INSTALL_DIR/dotfiles"
  fi
else
  info "Installing dotfiles v${VERSION}..."
  URL="https://github.com/${REPO}/releases/download/${LATEST}/dotfiles_${VERSION}_${OS}_${ARCH}.tar.gz"
  mkdir -p "$INSTALL_DIR"
  curl -fsSL "$URL" | tar xz -C "$INSTALL_DIR" dotfiles
  chmod +x "$INSTALL_DIR/dotfiles"
fi

# Create 'dot' convenience symlink
ln -sf "$INSTALL_DIR/dotfiles" "$INSTALL_DIR/dot"

# --- Step 4: Ensure PATH ---

ensure_path() {
  local target_dir="$1"

  # Already in PATH
  case ":$PATH:" in
    *":$target_dir:"*) return 0 ;;
  esac

  # Detect shell RC file
  local shell_name rc_file
  shell_name="$(basename "${SHELL:-/bin/zsh}")"
  case "$shell_name" in
    zsh)  rc_file="$HOME/.zshrc" ;;
    bash) rc_file="$HOME/.bashrc" ;;
    *)    rc_file="$HOME/.profile" ;;
  esac

  local path_line="export PATH=\"$target_dir:\$PATH\""

  if [[ -f "$rc_file" ]] && grep -qF "$target_dir" "$rc_file" 2>/dev/null; then
    export PATH="$target_dir:$PATH"
    return 0
  fi

  info "Adding $target_dir to PATH in $rc_file"
  {
    echo ""
    echo "# Added by dotfiles installer"
    echo "$path_line"
  } >> "$rc_file"

  export PATH="$target_dir:$PATH"
}

ensure_path "$INSTALL_DIR"

# --- Step 5: Verify and show next steps ---

if command -v dot &>/dev/null; then
  info "dot $(dot --version 2>/dev/null) is ready"
elif [[ -x "$INSTALL_DIR/dot" ]]; then
  info "dot $("$INSTALL_DIR/dot" --version 2>/dev/null) installed at $INSTALL_DIR/dot"
  warn "Open a new terminal or run: export PATH=\"$INSTALL_DIR:\$PATH\""
else
  err "Installation failed -- dot binary not found"
  exit 1
fi

echo ""
echo "=== Next steps ==="
echo ""
echo "  dot init              # Setup wizard"
echo "  dot apply             # Install packages & configure environment"
echo "  dot secrets restore   # Decrypt SSH keys & secrets (optional)"
echo "  dot sync setup        # Configure server sync (optional)"
echo ""
