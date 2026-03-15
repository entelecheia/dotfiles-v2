#!/usr/bin/env bash
set -euo pipefail

REPO="entelecheia/dotfiles-v2"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# Detect OS and arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "[+] Detecting: ${OS}/${ARCH}"

# Get latest version
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
VERSION="${LATEST#v}"

echo "[+] Installing dotfiles v${VERSION}..."

# Download and extract
URL="https://github.com/${REPO}/releases/download/${LATEST}/dotfiles_${VERSION}_${OS}_${ARCH}.tar.gz"
mkdir -p "$INSTALL_DIR"
curl -fsSL "$URL" | tar xz -C "$INSTALL_DIR" dotfiles

chmod +x "$INSTALL_DIR/dotfiles"
echo "[+] Installed to $INSTALL_DIR/dotfiles"
echo "[+] Version: $($INSTALL_DIR/dotfiles --version)"

# Check PATH
if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
  echo "[!] Add $INSTALL_DIR to your PATH"
fi
