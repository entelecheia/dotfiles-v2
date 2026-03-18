#!/usr/bin/env bash
# Scenario: init → apply → check
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

echo "=== Scenario: init-and-apply ==="

echo ""
echo "--- init ---"
dotfiles init --yes
assert_exit_code 0 dotfiles init --yes

echo ""
echo "--- apply ---"
dotfiles apply --yes
assert_exit_code 0 dotfiles apply --yes

echo ""
echo "--- check ---"
dotfiles check
assert_exit_code 0 dotfiles check

assert_dir_exists "$HOME/.config/dotfiles" "Config directory created"
assert_file_exists "$HOME/.config/dotfiles/config.yaml" "Config file created"

report
