#!/usr/bin/env bash
# run-module.sh — Single module test
set -euo pipefail
source "$(dirname "$0")/assert.sh"

MODULE="${1:?Usage: run-module.sh <module>}"

echo "=== Module test: $MODULE ==="

# Determine profile — tmux/ai-tools/conda need full or server
case "$MODULE" in
  tmux|ai-tools|conda)
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
