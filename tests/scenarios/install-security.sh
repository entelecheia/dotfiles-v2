#!/usr/bin/env bash
# Scenario: checksum-first installer replacement safety.
set -euo pipefail

source "$(dirname "$0")/../assert.sh"

SCRIPT="$(cd "$(dirname "$0")/../.." && pwd)/scripts/install.sh"

echo "=== Scenario: install-security ==="

echo ""
echo "--- checksum-first installer contract ---"
assert_exit_code 0 grep -q 'select_checksum_tool' "$SCRIPT"
assert_exit_code 0 grep -q 'verify_checksum' "$SCRIPT"
assert_exit_code 0 grep -q 'checksums.txt' "$SCRIPT"

report
