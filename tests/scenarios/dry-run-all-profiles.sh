#!/usr/bin/env bash
# Scenario: dry-run all profiles — no file changes
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

echo "=== Scenario: dry-run all profiles ==="

SNAP_BEFORE=$(snapshot_dir "$HOME")

for profile in minimal full server; do
  echo ""
  echo "--- dry-run: $profile ---"
  dotfiles apply --profile "$profile" --yes --dry-run
  assert_exit_code 0 dotfiles apply --profile "$profile" --yes --dry-run
done

assert_no_changes "$HOME" "$SNAP_BEFORE" "No files changed after dry-run"

report
