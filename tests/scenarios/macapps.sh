#!/usr/bin/env bash
# Scenario: macOS apps module + apps subcommand smoke test.
# Runs only on darwin; no-ops on linux by design.
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

echo "=== Scenario: macapps module + apps subcommand ==="

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "  (skipped: non-darwin host)"
  report
  exit 0
fi

# apps list always succeeds (static catalog)
assert_exit_code 0 dotfiles apps list

# apps status --from points at a throwaway dir; expected to enumerate tracked apps
tmpbk=$(mktemp -d)
assert_exit_code 0 dotfiles apps status --from "$tmpbk"

# dry-run backup of a single known-safe app
if dotfiles apps backup --dry-run moom --to "$tmpbk" >/dev/null 2>&1; then
  PASS=$((PASS + 1))
  echo "  ✓ apps backup --dry-run moom exits cleanly"
else
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: apps backup --dry-run moom")
  echo "  ✗ apps backup --dry-run moom failed"
fi
# dry-run must not create any files inside the archive
if find "$tmpbk" -type f 2>/dev/null | grep -q .; then
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: dry-run produced files in $tmpbk")
  echo "  ✗ dry-run produced files"
else
  PASS=$((PASS + 1))
  echo "  ✓ dry-run wrote no files"
fi

# macapps module: apply dry-run with profile=full, module=macapps
assert_exit_code 0 dotfiles apply --profile full --yes --dry-run --module macapps

rm -rf "$tmpbk"
report
