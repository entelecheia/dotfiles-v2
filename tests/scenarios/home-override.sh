#!/usr/bin/env bash
# Scenario: --home override puts state/config in custom directory
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

echo "=== Scenario: home-override ==="

CUSTOM_HOME=$(mktemp -d /tmp/dotfiles-home-XXXX)

echo ""
echo "--- init with --home $CUSTOM_HOME ---"
dotfiles init --home "$CUSTOM_HOME" --yes

assert_dir_exists "$CUSTOM_HOME/.config/dotfiles" "Config in custom home"

echo ""
echo "--- apply with --home $CUSTOM_HOME ---"
dotfiles apply --home "$CUSTOM_HOME" --profile minimal --yes --dry-run
assert_exit_code 0 dotfiles apply --home "$CUSTOM_HOME" --profile minimal --yes --dry-run

# Cleanup
rm -rf "$CUSTOM_HOME"

report
