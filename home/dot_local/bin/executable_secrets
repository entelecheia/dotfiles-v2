#!/usr/bin/env bash
set -euo pipefail

SECRETS_DIR="${SECRETS_DIR:-$HOME/.local/share/chezmoi-secrets}"

usage() {
  cat <<EOF
Usage: $(basename "$0") <command>

Commands:
  init       Create secrets directory and encrypt files with age
  backup     Copy encrypted secrets to a backup location
  restore    Restore encrypted secrets from a backup location
  list       List current secrets
  status     Check if secrets are in place

Environment:
  SECRETS_DIR   Secrets storage (default: ~/.local/share/chezmoi-secrets)
EOF
}

get_recipients() {
  if command -v chezmoi &>/dev/null; then
    chezmoi data --format json 2>/dev/null | jq -r '.age_recipients // empty' | tr ',' '\n' | while read -r r; do
      r="$(echo "$r" | xargs)"
      [ -n "$r" ] && echo "-r" && echo "$r"
    done
  fi
}

cmd_init() {
  mkdir -p "$SECRETS_DIR"
  chmod 700 "$SECRETS_DIR"

  echo "[+] Secrets directory: $SECRETS_DIR"
  echo ""

  local recipients
  recipients="$(get_recipients)"
  if [ -z "$recipients" ]; then
    echo "[!] No age recipients found. Set age_recipients in chezmoi config."
    exit 1
  fi

  # SSH private key
  local ssh_key
  ssh_key="$(chezmoi data --format json 2>/dev/null | jq -r '.ssh_key_name // "id_ed25519"')"
  local ssh_src="$HOME/.ssh/$ssh_key"
  if [ -f "$ssh_src" ] && [ ! -f "$SECRETS_DIR/$ssh_key.age" ]; then
    echo "[+] Encrypting SSH key: $ssh_key"
    # shellcheck disable=SC2086
    age -e $recipients -o "$SECRETS_DIR/$ssh_key.age" "$ssh_src"
  fi

  # Shell secrets
  local secrets_src="$HOME/.config/shell/90-secrets.sh"
  if [ -f "$secrets_src" ] && [ ! -f "$SECRETS_DIR/90-secrets.sh.age" ]; then
    echo "[+] Encrypting shell secrets"
    # shellcheck disable=SC2086
    age -e $recipients -o "$SECRETS_DIR/90-secrets.sh.age" "$secrets_src"
  fi

  echo "[+] Done. Run '$(basename "$0") backup <dest>' to back up."
}

cmd_backup() {
  local dest="${1:-}"
  [ -z "$dest" ] && { echo "Usage: $(basename "$0") backup <destination-dir>"; exit 1; }
  mkdir -p "$dest"
  cp -v "$SECRETS_DIR"/*.age "$dest/"
  echo "[+] Backed up to $dest"
}

cmd_restore() {
  local src="${1:-}"
  [ -z "$src" ] && { echo "Usage: $(basename "$0") restore <source-dir>"; exit 1; }
  [ -d "$src" ] || { echo "Not found: $src"; exit 1; }
  mkdir -p "$SECRETS_DIR"
  chmod 700 "$SECRETS_DIR"
  cp -v "$src"/*.age "$SECRETS_DIR/"

  local age_identity
  age_identity="$(chezmoi data --format json 2>/dev/null | jq -r '.age_identity // empty')"
  [ -z "$age_identity" ] && { echo "[!] No age identity found. Run chezmoi init first."; exit 1; }

  local ssh_key
  ssh_key="$(chezmoi data --format json 2>/dev/null | jq -r '.ssh_key_name // "id_ed25519"')"

  if [ -f "$SECRETS_DIR/$ssh_key.age" ]; then
    echo "[+] Decrypting SSH key -> ~/.ssh/$ssh_key"
    age -d -i "$age_identity" -o "$HOME/.ssh/$ssh_key" "$SECRETS_DIR/$ssh_key.age"
    chmod 600 "$HOME/.ssh/$ssh_key"
  fi

  if [ -f "$SECRETS_DIR/90-secrets.sh.age" ]; then
    echo "[+] Decrypting shell secrets -> ~/.config/shell/90-secrets.sh"
    mkdir -p "$HOME/.config/shell"
    age -d -i "$age_identity" -o "$HOME/.config/shell/90-secrets.sh" "$SECRETS_DIR/90-secrets.sh.age"
    chmod 600 "$HOME/.config/shell/90-secrets.sh"
  fi

  echo "[+] Restored"
}

cmd_list() {
  echo "Secrets in $SECRETS_DIR:"
  ls -lh "$SECRETS_DIR"/*.age 2>/dev/null || echo "(none)"
}

cmd_status() {
  local ok=true
  local ssh_key
  ssh_key="$(chezmoi data --format json 2>/dev/null | jq -r '.ssh_key_name // "id_ed25519"')"

  echo "=== Secrets Status ==="
  for f in "$HOME/.ssh/$ssh_key" "$HOME/.config/shell/90-secrets.sh"; do
    if [ -f "$f" ]; then
      echo "  [OK] $f"
    else
      echo "  [!!] $f (missing)"
      ok=false
    fi
  done
  for f in "$SECRETS_DIR/$ssh_key.age" "$SECRETS_DIR/90-secrets.sh.age"; do
    if [ -f "$f" ]; then
      echo "  [OK] $f (backup)"
    else
      echo "  [!!] $f (no backup)"
    fi
  done
  $ok && echo "All secrets in place." || echo "Some secrets missing. Run 'secrets init' or 'secrets restore <src>'."
}

case "${1:-}" in
  init)    cmd_init ;;
  backup)  shift; cmd_backup "$@" ;;
  restore) shift; cmd_restore "$@" ;;
  list)    cmd_list ;;
  status)  cmd_status ;;
  *)       usage ;;
esac
