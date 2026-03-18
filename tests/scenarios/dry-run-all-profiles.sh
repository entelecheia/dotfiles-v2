#!/usr/bin/env bash
# Scenario: dry-run all profiles — all exit successfully, no user-facing files written
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

echo "=== Scenario: dry-run all profiles ==="

for profile in minimal full server; do
  echo ""
  echo "--- dry-run: $profile ---"
  dotfiles apply --profile "$profile" --yes --dry-run
  assert_exit_code 0 dotfiles apply --profile "$profile" --yes --dry-run
done

# Verify no user-facing config files were written (internal state dirs are OK)
assert_file_not_exists "$HOME/.zshrc" "No .zshrc created by dry-run"
assert_file_not_exists "$HOME/.tmux.conf" "No .tmux.conf created by dry-run"
assert_file_not_exists "$HOME/.config/git/config" "No git config created by dry-run"
assert_file_not_exists "$HOME/.config/starship.toml" "No starship config created by dry-run"

report
