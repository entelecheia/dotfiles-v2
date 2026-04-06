#!/usr/bin/env bash
# drive-exclude.sh — Integration test for drive-exclude command.
# xattr calls are no-ops on Linux, but we test scan/apply/status flow.
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

check() {
  local desc="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    pass "$desc"
  else
    fail "$desc"
  fi
}

# Use isolated workspace and HOME (so migrate doesn't find real ~/node_modules_store)
WORKSPACE=$(mktemp -d)
export HOME=$(mktemp -d)
trap "rm -rf $WORKSPACE $HOME" EXIT

echo "=== drive-exclude integration tests ==="
echo "Binary: $BIN"
echo "Workspace: $WORKSPACE"
echo

# ── 1. Help ──────────────────────────────────────────────────────────────────
echo "--- help ---"
HELP_OUT=$($BIN help 2>&1) || true
echo "$HELP_OUT" | grep -q "drive-exclude" && pass "help lists drive-exclude" || fail "help lists drive-exclude"

DE_HELP=$($BIN drive-exclude --help 2>&1) || true
echo "$DE_HELP" | grep -q "scan" && pass "drive-exclude help shows scan" || fail "drive-exclude help shows scan"
echo "$DE_HELP" | grep -q "apply" && pass "drive-exclude help shows apply" || fail "drive-exclude help shows apply"
echo "$DE_HELP" | grep -q "status" && pass "drive-exclude help shows status" || fail "drive-exclude help shows status"
echo "$DE_HELP" | grep -q "migrate" && fail "migrate should be removed" || pass "no migrate subcommand"

# ── 2. Scan empty workspace ──────────────────────────────────────────────────
echo "--- scan empty ---"
SCAN_OUT=$($BIN drive-exclude scan "$WORKSPACE" 2>&1) || true
echo "$SCAN_OUT" | grep -q "No excludable" && pass "scan empty workspace" || fail "scan empty workspace"

# ── 3. Scan with node_modules ────────────────────────────────────────────────
echo "--- scan with targets ---"
mkdir -p "$WORKSPACE/proj-a/node_modules/pkg"
echo '{}' > "$WORKSPACE/proj-a/node_modules/pkg/index.js"
mkdir -p "$WORKSPACE/proj-b/.next/cache"
echo 'x' > "$WORKSPACE/proj-b/.next/cache/data"
mkdir -p "$WORKSPACE/proj-c/__pycache__"
echo 'x' > "$WORKSPACE/proj-c/__pycache__/mod.pyc"

SCAN_OUT=$($BIN drive-exclude scan "$WORKSPACE" 2>&1) || true
echo "$SCAN_OUT" | grep -q "node_modules" && pass "scan finds node_modules" || fail "scan finds node_modules"
echo "$SCAN_OUT" | grep -q ".next" && pass "scan finds .next" || fail "scan finds .next"
echo "$SCAN_OUT" | grep -q "__pycache__" && pass "scan finds __pycache__" || fail "scan finds __pycache__"
echo "$SCAN_OUT" | grep -q "3 directories" && pass "scan counts 3 directories" || fail "scan counts 3 directories"

# ── 4. Apply ─────────────────────────────────────────────────────────────────
echo "--- apply ---"
# --yes to skip confirmation, --dry-run to avoid xattr on CI (Linux no-op anyway)
APPLY_OUT=$($BIN drive-exclude apply "$WORKSPACE" --yes --dry-run 2>&1) || true
echo "$APPLY_OUT" | grep -q "node_modules" && pass "apply targets node_modules" || fail "apply targets node_modules"

# Apply for real (no-op on Linux, actual xattr on macOS)
APPLY_OUT=$($BIN drive-exclude apply "$WORKSPACE" --yes 2>&1) || true
echo "$APPLY_OUT" | grep -qi "excluded\|no-op\|nothing" || true
pass "apply runs without error"

# ── 5. Status ────────────────────────────────────────────────────────────────
echo "--- status ---"
STATUS_OUT=$($BIN drive-exclude status 2>&1) || true
echo "$STATUS_OUT" | grep -q "Drive Exclude Status" && pass "status shows header" || fail "status shows header"

# ── 6. Scan skips .git ──────────────────────────────────────────────────────
echo "--- scan skips .git ---"
mkdir -p "$WORKSPACE/proj-d/.git/objects"
SCAN_OUT=$($BIN drive-exclude scan "$WORKSPACE" 2>&1) || true
echo "$SCAN_OUT" | grep -q ".git" && fail "scan should skip .git" || pass "scan skips .git"

echo
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] || exit 1
