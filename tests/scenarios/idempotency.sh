#!/usr/bin/env bash
# Scenario: apply twice — second run should make no changes
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

echo "=== Scenario: idempotency ==="

echo ""
echo "--- first apply ---"
dotfiles init --profile minimal --yes
dotfiles apply --profile minimal --yes

echo ""
echo "--- snapshot after first apply ---"
SNAP_AFTER_FIRST=$(snapshot_dir "$HOME")

echo ""
echo "--- second apply ---"
dotfiles apply --profile minimal --yes

assert_no_changes "$HOME" "$SNAP_AFTER_FIRST" "Second apply made no changes"

report
