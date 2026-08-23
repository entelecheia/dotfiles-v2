#!/usr/bin/env bash
# Scenario: dot noindex end to end (TEST-05, D-10)
#
# internal/noindex already sits at 91.2% unit coverage, so what is missing is
# not branch coverage: it is whether the BUILT binary, invoked the way a user
# invokes it, marks the right directories under the home it was pointed at and
# leaves the rest alone. One invocation walks every project tree under a home
# and writes files into directories the tool does not own, so the blast radius
# of a wrong predicate is every project the user has.
#
# Portability: only POSIX `find` features are used (`-name`), unlike
# tests/scenarios/secrets.sh which needs GNU `find -printf`. That keeps this
# scenario runnable against a locally built binary as well as in the container.
set -euo pipefail
# shellcheck source=tests/assert.sh disable=SC1091
source "$(dirname "$0")/../assert.sh"

# assert.sh carries no generic pass/fail/skip. pass and fail both feed the
# terminal report; skip is a third state for a claim this host cannot make, and
# it is printed separately so it can never be read as a pass.
pass() {
  PASS=$((PASS + 1))
  echo "  ✓ $1"
}
fail() {
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: $1")
  echo "  ✗ $1"
}
SKIPPED=()
skip() {
  SKIPPED+=("$1")
  echo "  ~ SKIPPED: $1"
}

# ABORT records a fixture failure the rest of the run cannot proceed past, and
# prints the report at the point of failure rather than after a cascade of
# assertions that were all about a half-built fixture.
ABORT() {
  fail "$1"
  print_skipped
  report || true
  exit 1
}

print_skipped() {
  if [ ${#SKIPPED[@]} -gt 0 ]; then
    echo ""
    echo "Skipped (neither passed nor failed):"
    for s in "${SKIPPED[@]}"; do
      echo "  ~ $s"
    done
  fi
}

# MARKER is noindex.Marker (internal/noindex/noindex.go:28). The literal is
# here rather than read from the source because the test image copies only
# tests/ — there is no Go tree at runtime in the container. When the source IS
# present (a local run from a checkout) the two are compared below, so a
# renamed constant fails loudly instead of quietly making every marker
# assertion in this file vacuous.
MARKER=".metadata_never_index"

# FIXTURE builds one sandbox home.
#
# Every root name below is taken from a list in internal/noindex/noindex.go and
# nowhere else. A fixture whose roots are not in those lists produces a sweep
# that finds nothing, and a scenario that passes for the wrong reason:
#
#   workspace   walkRootNames  (noindex.go:30-41) -- walked, every match inside marked
#   Sites       walkRootNames  -- deliberately NOT created, see group 1
#   .npm        cacheRootNames (noindex.go:45-58) -- one marker at the top, not walked
#
# and the directories inside the project tree come from clean.DefaultPatterns
# (internal/clean/clean.go:62-84), split by noindex's own keepIndexed
# (noindex.go:87-90):
#
#   node_modules, .venv, dist, target   match dirPatterns  -> marked
#   build, out                          keepIndexed        -> NOT marked
#   src                                 not a pattern      -> NOT marked
FIXTURE() {
  local H="$1"
  mkdir -p \
    "$H/workspace/proj/node_modules/dep" \
    "$H/workspace/proj/.venv" \
    "$H/workspace/proj/dist" \
    "$H/workspace/proj/target" \
    "$H/workspace/proj/build" \
    "$H/workspace/proj/out" \
    "$H/workspace/proj/src" \
    "$H/.npm/inner/node_modules"
}

# EXPECTED_MARKED is the count of the five directories a full sweep of a fresh
# FIXTURE must mark: the .npm cache root itself, plus node_modules, .venv, dist
# and target inside the project tree. It is an exact count, not a floor:
# a floor would pass a sweep that also marked build/ and out/.
EXPECTED_MARKED=5

# MARKED_COUNT parses the count out of the success line rather than grepping
# for the number. The line embeds no paths today, but a `grep -q 5` against
# output that ever does would match a mktemp suffix and pass vacuously.
#
# Two separate BRE expressions, one per verb: `\|` alternation is a GNU sed
# extension and this scenario has to parse the same line under BSD sed.
MARKED_COUNT() {
  sed -n \
    -e 's/^marked \([0-9][0-9]*\) directories.*/\1/p' \
    -e 's/^would mark \([0-9][0-9]*\) directories.*/\1/p' \
    "$1" | head -1
}

# PRESENT_COUNT parses the already-marked count, which appears in both the
# "nothing to do (N already marked)" line and the trailing "(N already marked)"
# of a normal run.
PRESENT_COUNT() {
  sed -n 's/.*(\([0-9][0-9]*\) already marked).*/\1/p' "$1" | head -1
}

# MARKERS_UNDER lists every marker in a tree, one relative path per line.
MARKERS_UNDER() {
  find "$1" -name "$MARKER" 2>/dev/null | sed "s|^$1/||" | LC_ALL=C sort
}

echo "=== Scenario: noindex ==="

WORK_DIR=$(mktemp -d /tmp/dotfiles-noindex-XXXX)
# Cleanup on the failure path too: the container outlives this script and each
# home below is a multi-level tree.
trap 'rm -rf "$WORK_DIR"' EXIT

MARKER_SRC="$(dirname "$0")/../../internal/noindex/noindex.go"
if [ -f "$MARKER_SRC" ]; then
  SRC_MARKER=$(sed -n 's/^const Marker = "\(.*\)".*$/\1/p' "$MARKER_SRC" | head -1)
  if [ "$SRC_MARKER" = "$MARKER" ]; then
    pass "the marker name this scenario asserts on matches noindex.Marker in the source"
  else
    fail "noindex.Marker is '$SRC_MARKER' but this scenario asserts on '$MARKER'; every marker assertion below would be vacuous"
  fi
else
  skip "noindex.Marker could not be cross-checked: no Go source tree at $MARKER_SRC (expected in the test image, which copies only tests/)"
fi

echo ""
echo "--- group 1: a full sweep marks the regenerable directories under a project root ---"

SWEPT="$WORK_DIR/home-swept"
FIXTURE "$SWEPT"

# A default walk root that is absent must be skipped rather than erroring, and
# the claim is only worth making if the fixture really lacks one.
if [ -e "$SWEPT/Sites" ]; then
  ABORT "the fixture created $SWEPT/Sites; the absent-walk-root claim below needs it to be missing"
fi

SWEEP_LOG="$WORK_DIR/sweep.log"
SWEEP_RC=0
dot noindex --home "$SWEPT" > "$SWEEP_LOG" 2>&1 || SWEEP_RC=$?
cat "$SWEEP_LOG"
if [ "$SWEEP_RC" -eq 0 ]; then
  pass "dot noindex --home exited 0 with ~/Sites (a default walk root) absent, so a missing root is skipped rather than failing the sweep"
else
  ABORT "dot noindex --home exited $SWEEP_RC; every assertion below would be about a sweep that did not run"
fi

SWEEP_MARKED=$(MARKED_COUNT "$SWEEP_LOG")
if [ -z "$SWEEP_MARKED" ]; then
  fail "the sweep printed no 'marked N directories' line, so it reported nothing to do: $(head -1 "$SWEEP_LOG")"
elif [ "$SWEEP_MARKED" -eq "$EXPECTED_MARKED" ]; then
  pass "the sweep reported marking exactly $EXPECTED_MARKED directories, so it neither marked nothing nor over-marked"
else
  fail "the sweep reported marking $SWEEP_MARKED directories, expected $EXPECTED_MARKED; markers found: $(MARKERS_UNDER "$SWEPT" | tr '\n' ' ')"
fi

assert_file_exists "$SWEPT/workspace/proj/node_modules/$MARKER" \
  "a regenerable directory inside a walked project root carries the marker"
assert_file_exists "$SWEPT/workspace/proj/.venv/$MARKER" \
  "a second regenerable directory in the same project carries the marker"

# A cache root is stamped at the top INSTEAD of being walked. The pair is what
# says so: the top carries a marker and a matching directory buried inside it
# does not.
assert_file_exists "$SWEPT/.npm/$MARKER" \
  "the .npm cache root carries one marker at its top"
assert_file_not_exists "$SWEPT/.npm/inner/node_modules/$MARKER" \
  "a node_modules buried inside the cache root has no marker of its own, so the cache root was stamped rather than walked"

print_skipped

report
