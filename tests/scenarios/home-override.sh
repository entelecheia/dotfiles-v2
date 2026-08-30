#!/usr/bin/env bash
# Scenario: --home override puts state/config in custom directory
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

# Local helpers on top of assert.sh counters (no generic pass/fail there).
pass() {
  PASS=$((PASS + 1))
  echo "  ✓ $1"
}
fail() {
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: $1")
  echo "  ✗ $1"
}

echo "=== Scenario: home-override ==="

CUSTOM_HOME=$(mktemp -d /tmp/dotfiles-home-XXXX)

echo ""
echo "--- init with --home $CUSTOM_HOME ---"
dot init --home "$CUSTOM_HOME" --yes

assert_dir_exists "$CUSTOM_HOME/.config/dotfiles" "Config in custom home"

echo ""
echo "--- apply with --home $CUSTOM_HOME ---"
dot apply --home "$CUSTOM_HOME" --profile minimal --yes --dry-run
assert_exit_code 0 dot apply --home "$CUSTOM_HOME" --profile minimal --yes --dry-run

# BUG-27 (internal/cli/peer_status.go:120 resolves the peer layout by profile alone,
# dropping --home) is latent, so this is anchored on the SURVIVING threading at
# peer_status.go:61: removing it moves workspacePath/storeDir/logPath/homePathsPath
# into the invoking home. NOT evidence for the peer_status.go:120 deletion; that
# document's jobs array is overwritten before it is encoded.
echo ""
echo "--- peer status --json with --home $CUSTOM_HOME ---"
PEER_RC=0
PEER_JSON=$(dot peer status --json --home "$CUSTOM_HOME") || PEER_RC=$?

# Non-vacuity guard first: without it the negative half below passes on an empty
# string, which is a guard that measures nothing.
if [ -n "$PEER_JSON" ] && printf '%s' "$PEER_JSON" | grep -q 'schemaVersion'; then
  pass "peer status --json emits a document under --home"

  if printf '%s' "$PEER_JSON" | grep -Fq -- "$CUSTOM_HOME"; then
    pass "peer status document names the target home"
  else
    fail "peer status document does not name $CUSTOM_HOME, got: $PEER_JSON"
  fi

  if printf '%s' "$PEER_JSON" | grep -Fq -- "$HOME/"; then
    fail "peer status document names the invoking home $HOME/, got: $PEER_JSON"
  else
    pass "peer status document does not name the invoking home"
  fi
else
  fail "peer status --json --home emitted no usable document (exit $PEER_RC), got: $PEER_JSON"
fi

# Cleanup
rm -rf "$CUSTOM_HOME"

report
