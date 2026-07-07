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

# Resolve the dot binary. The macOS CI job builds ./bin/dot without adding it
# to PATH, while the Docker-based linux job installs dot on PATH — search both.
DOT="${1:-}"
if [[ -z "$DOT" || ! -x "$DOT" ]]; then
  for candidate in ./bin/dot ./dot /usr/local/bin/dot "$(command -v dot 2>/dev/null)"; do
    if [[ -n "$candidate" && -x "$candidate" ]]; then
      DOT="$candidate"
      break
    fi
  done
fi
if [[ -z "$DOT" || ! -x "$DOT" ]]; then
  echo "  ✗ dot binary not found"
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: dot binary not found")
  report
  exit 1
fi

# apps list always succeeds (static catalog)
assert_exit_code 0 "$DOT" apps list

# apps status --from points at a throwaway dir; expected to enumerate tracked apps
tmpbk=$(mktemp -d)
assert_exit_code 0 "$DOT" apps status --from "$tmpbk"

# dry-run backup of a single known-safe app
if "$DOT" apps backup --dry-run moom --to "$tmpbk" >/dev/null 2>&1; then
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
assert_exit_code 0 "$DOT" apply --profile full --yes --dry-run --module macapps

rm -rf "$tmpbk"
report
