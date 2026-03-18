#!/usr/bin/env bash
# Scenario: upgrade --check runs without error
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

echo "=== Scenario: upgrade --check ==="

# upgrade --check should exit 0 (connects to GitHub API)
# In CI, GITHUB_TOKEN may be set for authentication
echo ""
echo "--- upgrade --check ---"
dotfiles upgrade --check
assert_exit_code 0 dotfiles upgrade --check

report
