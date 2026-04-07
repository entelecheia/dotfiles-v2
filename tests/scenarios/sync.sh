#!/usr/bin/env bash
# sync.sh — Integration test for sync command.
# rclone is not available in CI Docker, so we test help/status flow only.
set -euo pipefail

# Find binary
BIN="${1:-}"
if [ -z "$BIN" ] || [ ! -x "$BIN" ]; then
  for candidate in ./dotfiles /usr/local/bin/dotfiles "$(command -v dotfiles 2>/dev/null)"; do
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then
      BIN="$candidate"
      break
    fi
  done
fi
if [ -z "$BIN" ] || [ ! -x "$BIN" ]; then
  echo "FAIL: dotfiles binary not found"
  exit 1
fi

PASS=0
FAIL=0

pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

export HOME=$(mktemp -d)
trap "rm -rf $HOME" EXIT

echo "=== sync integration tests ==="
echo "Binary: $BIN"
echo

# ── 1. Help ──────────────────────────────────────────────────────────────────
echo "--- help ---"
HELP_OUT=$($BIN help 2>&1) || true
echo "$HELP_OUT" | grep -q "sync" && pass "help lists sync" || fail "help lists sync"

SYNC_HELP=$($BIN sync --help 2>&1) || true
echo "$SYNC_HELP" | grep -q "setup" && pass "sync help shows setup" || fail "sync help shows setup"
echo "$SYNC_HELP" | grep -q "status" && pass "sync help shows status" || fail "sync help shows status"
echo "$SYNC_HELP" | grep -q "log" && pass "sync help shows log" || fail "sync help shows log"
echo "$SYNC_HELP" | grep -q "pause" && pass "sync help shows pause" || fail "sync help shows pause"
echo "$SYNC_HELP" | grep -q "resume" && pass "sync help shows resume" || fail "sync help shows resume"

# ── 2. Status (no rclone) ───────────────────────────────────────────────────
echo "--- status ---"
STATUS_OUT=$($BIN sync status 2>&1) || true
echo "$STATUS_OUT" | grep -q "Workspace Sync Status" && pass "status shows header" || fail "status shows header"
echo "$STATUS_OUT" | grep -q "not installed" && pass "status shows scheduler not installed" || fail "status shows scheduler not installed"

# ── 3. Sync without setup ───────────────────────────────────────────────────
echo "--- sync without setup ---"
SYNC_OUT=$($BIN sync 2>&1) || true
# Should suggest setup (rclone not installed or filter missing)
echo "$SYNC_OUT" | grep -qi "setup\|not installed\|not found" && pass "sync suggests setup" || fail "sync suggests setup"

# ── 4. Log without log file ─────────────────────────────────────────────────
echo "--- log ---"
LOG_OUT=$($BIN sync log 2>&1) || true
echo "$LOG_OUT" | grep -qi "no log\|log file" && pass "log handles missing file" || fail "log handles missing file"

# ── 5. Pause without scheduler ──────────────────────────────────────────────
echo "--- pause/resume ---"
PAUSE_OUT=$($BIN sync pause 2>&1) || true
echo "$PAUSE_OUT" | grep -qi "not installed\|setup" && pass "pause without scheduler" || fail "pause without scheduler"

echo
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] || exit 1
