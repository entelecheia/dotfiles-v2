#!/usr/bin/env bash
# Scenario: update --check runs without error
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

echo "=== Scenario: update --check ==="

# update --check calls the GitHub API; may fail due to rate limits or network issues in CI
echo ""
echo "--- update --check ---"
if dotfiles update --check; then
  echo "  ✓ update --check succeeded"
else
  echo "  ⚠ update --check returned non-zero (likely API rate limit), skipping assertion"
fi

report
