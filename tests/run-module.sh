#!/usr/bin/env bash
# run-module.sh — Single module test
set -euo pipefail
source "$(dirname "$0")/assert.sh"

MODULE="${1:?Usage: run-module.sh <module>}"

echo "=== Module test: $MODULE ==="

# Determine profile — some modules need full or server
case "$MODULE" in
  node|tmux|ai-tools|conda)
    PROFILE="full"
    ;;
  *)
    PROFILE="minimal"
    ;;
esac

# Init with appropriate profile
dotfiles init --profile "$PROFILE" --yes

# Apply single module
echo ""
echo "--- apply module: $MODULE (profile: $PROFILE) ---"
dotfiles apply --profile "$PROFILE" --module "$MODULE" --yes

# Module-specific assertions
case "$MODULE" in
  packages)
    assert_command_exists dotfiles "dotfiles binary available"
    ;;
  shell)
    assert_dir_exists "$HOME/.config/shell" "Shell config directory"
    ;;
  node)
    assert_file_exists "$HOME/.npmrc" "npmrc exists"
    assert_file_contains "$HOME/.npmrc" "virtual-store-dir=" "npmrc has virtual-store-dir"
    assert_file_contains "$HOME/.npmrc" "store-dir=" "npmrc has store-dir"
    assert_file_contains "$HOME/.npmrc" "cache-dir=" "npmrc has cache-dir"
    assert_dir_exists "$HOME/.local/share/pnpm/virtual-store" "pnpm virtual-store directory"
    assert_dir_exists "$HOME/.local/share/pnpm/store" "pnpm store directory"
    assert_dir_exists "$HOME/.cache/pnpm" "pnpm cache directory"
    ;;
  git)
    assert_file_exists "$HOME/.config/git/config" "Git config exists"
    ;;
  ssh)
    assert_dir_exists "$HOME/.ssh" "SSH directory exists"
    ;;
  terminal)
    assert_file_exists "$HOME/.config/starship.toml" "Starship config exists"
    ;;
  tmux)
    assert_file_exists "$HOME/.tmux.conf" "tmux config exists"
    ;;
  ai-tools)
    assert_dir_exists "$HOME/.config" "Config directory exists"
    ;;
  conda)
    echo "  (conda module — shell init check skipped in CI)"
    PASS=$((PASS + 1))
    ;;
esac

report
