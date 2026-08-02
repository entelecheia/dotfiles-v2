#!/usr/bin/env bash
# sync.sh — E2E test for the unified `dot sync` (git-aware union filter,
# submodule exclusion, secrets opt-in, delete propagation) against a local
# tmp mirror. No scheduler/network involved.
set -euo pipefail

# Find binary
BIN="${1:-}"
if [ -z "$BIN" ] || [ ! -x "$BIN" ]; then
  for candidate in ./bin/dot ./dot /usr/local/bin/dot "$(command -v dot 2>/dev/null)" ./dotfiles "$(command -v dotfiles 2>/dev/null)"; do
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then
      BIN="$(cd "$(dirname "$candidate")" && pwd)/$(basename "$candidate")"
      break
    fi
  done
fi
if [ -z "$BIN" ] || [ ! -x "$BIN" ]; then
  echo "FAIL: dot binary not found"
  exit 1
fi
BIN="$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")"
if ! command -v rsync >/dev/null || ! command -v git >/dev/null; then
  echo "SKIP: rsync and git are required"
  exit 0
fi

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

export HOME=$(mktemp -d)
trap 'rm -rf "$HOME"' EXIT
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t

WS="$HOME/workspace/work"
MIRROR="$HOME/mirror"
mkdir -p "$WS" "$MIRROR"

echo "=== dot sync E2E (union filter) ==="
echo "Binary: $BIN"

# ── fixture workspace ────────────────────────────────────────────────────────
cd "$WS"
git init -q .
echo "tracked source" > notes.md
mkdir -p src && echo "print('hi')" > src/main.py
git add notes.md src/main.py

# untracked binary (allowlist) + untracked source (must NOT sync)
echo "%PDF-fake" > report.pdf
echo "untracked text" > stray.md

# junk + nested .git dir
mkdir -p node_modules/pkg && echo "x" > node_modules/pkg/index.js
mkdir -p proj/.git && echo "gitdata" > proj/.git/config
echo "%PDF-fake2" > proj/data.pdf

# secret
echo "TOKEN=sekret" > .env

# fake submodule: .gitmodules + gitlink index entry + checked-out content
mkdir -p sub && echo "submodule content" > sub/inner.md && echo "%PDF-sub" > sub/inner.pdf
cat > .gitmodules <<'EOF'
[submodule "sub"]
	path = sub
	url = https://example.com/sub.git
EOF
git add .gitmodules
git update-index --add --cacheinfo 160000 0000000000000000000000000000000000000001 sub

# ── init + target ────────────────────────────────────────────────────────────
"$BIN" sync init >/dev/null
"$BIN" sync target "local:$MIRROR" >/dev/null
[ -f "$WS/.dotfiles/sync/config.yaml" ] && pass "store created at .dotfiles/sync" || fail "store created at .dotfiles/sync"
[ -f "$WS/.dotfiles/sync/allow.txt" ] && pass "allow.txt scaffolded" || fail "allow.txt scaffolded"

# ── first push ───────────────────────────────────────────────────────────────
echo "--- push (force, with delete) ---"
"$BIN" sync push --mode=force --propagate=create,update,delete --yes >/dev/null 2>&1 || fail "push exited non-zero"

[ -f "$MIRROR/notes.md" ]        && pass "tracked source synced" || fail "tracked source synced"
[ -f "$MIRROR/src/main.py" ]     && pass "tracked nested source synced" || fail "tracked nested source synced"
[ -f "$MIRROR/report.pdf" ]      && pass "untracked binary synced" || fail "untracked binary synced"
[ -f "$MIRROR/proj/data.pdf" ]   && pass "binary next to nested .git synced" || fail "binary next to nested .git synced"
[ ! -e "$MIRROR/stray.md" ]      && pass "untracked source NOT synced" || fail "untracked source NOT synced"
[ ! -e "$MIRROR/.env" ]          && pass "secret NOT synced by default" || fail "secret NOT synced by default"
[ ! -e "$MIRROR/node_modules" ]  && pass "junk NOT synced" || fail "junk NOT synced"
[ ! -e "$MIRROR/sub" ]           && pass "submodule NOT synced" || fail "submodule NOT synced"
[ -z "$(find "$MIRROR" -name .git -print -quit)" ] && pass "no .git anywhere on mirror" || fail "no .git anywhere on mirror"
grep -q "notes.md" "$WS/.dotfiles/sync/baseline.manifest" && pass "tracked file in baseline" || fail "tracked file in baseline"

# ── secrets opt-in ───────────────────────────────────────────────────────────
echo "--- allow.txt opt-in ---"
echo "/.env" >> "$WS/.dotfiles/sync/allow.txt"
"$BIN" sync push --mode=force --propagate=create,update,delete --yes >/dev/null 2>&1 || fail "opt-in push exited non-zero"
[ -f "$MIRROR/.env" ] && pass "allowed secret synced" || fail "allowed secret synced"
STATUS_OUT=$("$BIN" sync status 2>/dev/null || true)
echo "$STATUS_OUT" | grep -q "allowed: 1" && pass "status warns about active allow" || fail "status warns about active allow"

# ── delete propagation for tracked files ─────────────────────────────────────
echo "--- delete propagation ---"
git rm -qf notes.md
"$BIN" sync push --mode=force --propagate=create,update,delete --yes >/dev/null 2>&1 || fail "delete push exited non-zero"
[ ! -e "$MIRROR/notes.md" ] && pass "deleted tracked file removed from mirror" || fail "deleted tracked file removed from mirror"

# ── filters show ─────────────────────────────────────────────────────────────
FILTERS_OUT=$("$BIN" sync filters show 2>/dev/null || true)
echo "$FILTERS_OUT" | grep -qi "submodule" && pass "filters show lists submodule layer" || fail "filters show lists submodule layer"

echo
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
