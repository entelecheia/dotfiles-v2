#!/usr/bin/env bash
# Scenario: dry-run all profiles — no file changes
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

echo "=== Scenario: dry-run all profiles ==="

# Track dotfiles-managed directories only
MANAGED_DIRS="$HOME/.config $HOME/.ssh $HOME/.local $HOME/.oh-my-zsh $HOME/.tmux.conf $HOME/.zshrc $HOME/.bashrc $HOME/.gitconfig $HOME/.gnupg"

snapshot_managed() {
  for d in $MANAGED_DIRS; do
    if [[ -e "$d" ]]; then
      find "$d" -type f 2>/dev/null
    fi
  done | sort | xargs sha256sum 2>/dev/null || true
}

SNAP_BEFORE=$(snapshot_managed)

for profile in minimal full server; do
  echo ""
  echo "--- dry-run: $profile ---"
  dotfiles apply --profile "$profile" --yes --dry-run
  assert_exit_code 0 dotfiles apply --profile "$profile" --yes --dry-run
done

SNAP_AFTER=$(snapshot_managed)
if [[ "$SNAP_BEFORE" == "$SNAP_AFTER" ]]; then
  PASS=$((PASS + 1))
  echo "  ✓ No managed files changed after dry-run"
else
  FAIL=$((FAIL + 1))
  echo "  ✗ No managed files changed after dry-run — files changed"
fi

report
