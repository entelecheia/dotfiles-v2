#!/usr/bin/env bash
# Scenario: apply twice — second run should make no changes
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

echo "=== Scenario: idempotency ==="

# Track dotfiles-managed directories only
MANAGED_DIRS="$HOME/.config $HOME/.ssh $HOME/.local $HOME/.oh-my-zsh $HOME/.tmux.conf $HOME/.zshrc $HOME/.bashrc $HOME/.gitconfig $HOME/.gnupg"

snapshot_managed() {
  for d in $MANAGED_DIRS; do
    if [[ -e "$d" ]]; then
      find "$d" -type f 2>/dev/null
    fi
  done | sort | xargs sha256sum 2>/dev/null || true
}

echo ""
echo "--- first apply ---"
dotfiles init --profile minimal --yes
dotfiles apply --profile minimal --yes

echo ""
echo "--- snapshot after first apply ---"
SNAP_AFTER_FIRST=$(snapshot_managed)

echo ""
echo "--- second apply ---"
dotfiles apply --profile minimal --yes

SNAP_AFTER_SECOND=$(snapshot_managed)
if [[ "$SNAP_AFTER_FIRST" == "$SNAP_AFTER_SECOND" ]]; then
  PASS=$((PASS + 1))
  echo "  ✓ Second apply made no changes"
else
  FAIL=$((FAIL + 1))
  echo "  ✗ Second apply made no changes — files changed"
fi

report
