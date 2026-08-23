#!/usr/bin/env bash
# Scenario: differential.sh decides correctly on all three registry verdict arms
#
# The not-stale check at the end of tests/scenarios/differential.sh is what stops
# tests/expected-diff.tsv from accumulating dead excuses, and it is bash, so
# nothing in `go test ./...` covers it. It shipped with two arms and got one of
# them wrong: a row whose field the preflight had disabled was reported as a
# reverted change and demanded removal, on every developer machine, for three
# committed rows CI registers correctly (BUG-17). Removing those rows on that
# evidence would have turned CI red.
#
# BUG-17's fix adds a third arm, and a third arm is exactly the shape that goes
# wrong quietly: an excuse that fires too widely suppresses a genuine stale row
# and nothing turns red. So the arms are proven here rather than by one
# observation, driven through the harness's real entry point with synthetic
# registries and stub binaries:
#
#   1. fail-closed  - a row on a field the run COMPARED, matching no difference,
#                     still fails with the remove-this-row message (D-08)
#   2. unchecked    - a row on a field the run SKIPPED is the third state and
#                     does not fail the run (D-07)
#   3. registered   - a row whose difference still fires passes with its reason
#   4. scoping      - the SAME row as case 2, run where the field WAS compared,
#                     fails. The third state is scoped to what this run skipped,
#                     not to the field's name; a fix that blanket-excused every
#                     `tree` row goes red here.
#
# Scope, stated so it is not overclaimed: the binaries are stubs, so nothing
# here exercises the real `dot`/`dot-baseline` comparison. That is the linux
# job's `differential` scenario, not this file. This file tests the verdict.
#
# Platform: the copied harness calls SNAPSHOT_TREE, which needs GNU `find
# -printf`, so this gate runs on Linux like the harness it tests. It lives in
# the `lint` job (ubuntu-latest) rather than the macOS unit job, and there
# rather than in the ubuntu:22.04 container because /tests/scenarios in the
# image has no repo above it to copy from.
# shellcheck source=tests/assert.sh disable=SC1091
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

pass() {
  PASS=$((PASS + 1))
  echo "  ✓ $1"
}
fail() {
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: $1")
  echo "  ✗ $1"
}

echo "=== Scenario: differential-registry-gate ==="

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)

# Recorded before anything is written, so the containment claim at the end is a
# comparison rather than an assertion about git's opinion of the working tree.
COMMITTED_FILES="$REPO_ROOT/tests/expected-diff.tsv $REPO_ROOT/tests/scenarios/differential.sh"
# shellcheck disable=SC2086 # deliberate word split: COMMITTED_FILES is a path list this file controls
HASHES_BEFORE=$(sha256sum $COMMITTED_FILES)

# The harness resolves its registry as <its own dir>/../expected-diff.tsv and
# sources <its own dir>/../assert.sh, so the temp tree mirrors the repo's shape:
# harness under scenarios/, registry and assert.sh one level up. Writing the
# fixtures beside a COPY is what keeps the committed expected-diff.tsv untouched
# even if this scenario dies mid-run, and what makes it safe to run in the same
# job as the real differential.
#
# The trap is registered before the first file is written, not after the last.
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/differential-registry-gate-XXXXXX")
trap 'rm -rf "$WORK_DIR"' EXIT
mkdir -p "$WORK_DIR/scenarios" "$WORK_DIR/bin" "$WORK_DIR/fake-brew/bin"
cp "$REPO_ROOT/tests/scenarios/differential.sh" "$WORK_DIR/scenarios/differential.sh"
cp "$REPO_ROOT/tests/assert.sh" "$WORK_DIR/assert.sh"
HARNESS="$WORK_DIR/scenarios/differential.sh"
FIXTURE_REGISTRY="$WORK_DIR/expected-diff.tsv"

# WRITE_STUBS <identical|status-differs> installs `dot` and `dot-baseline` in a
# temp bin dir. They exit 0 for every invocation the harness makes — every
# matrix entry plus the trailing `--home <dir>`, under `env -i` with only HOME
# and PATH — and print a constant, so nothing host-derived can reach the
# harness's own CHECK_ISOLATION. In status-differs mode only the current binary
# and only the `status` entry print something else, so exactly one command's
# stdout differs and every other field on every other command stays identical.
WRITE_STUBS() {
  local MODE="$1" NAME
  for NAME in dot dot-baseline; do
    cat > "$WORK_DIR/bin/$NAME" <<EOF
#!/usr/bin/env bash
if [ "$MODE" = status-differs ] && [ "\$1" = status ] && [ "\$(basename "\$0")" = dot ]; then
  echo "stub: status output as the current binary renders it"
  exit 0
fi
echo "stub: fixed output"
exit 0
EOF
    chmod +x "$WORK_DIR/bin/$NAME"
  done
}

# RUN_HARNESS <registry body> <prefix list> -> HARNESS_OUT, HARNESS_EXIT
RUN_HARNESS() {
  printf '%s\n' "$1" > "$FIXTURE_REGISTRY"
  HARNESS_OUT=$(PATH="$WORK_DIR/bin:$PATH" DIFFERENTIAL_BREW_PREFIXES="$2" \
    bash "$HARNESS" 2>&1) && HARNESS_EXIT=0 || HARNESS_EXIT=$?
  # Fail-closed on this gate's own emptiness. Every expectation below is a grep,
  # and a grep against a run that died before it printed anything matches
  # nothing — which for an expect_absent would read as success. Assert here that
  # the verdict section and the report both exist, so no case can pass on a run
  # that never produced a verdict.
  if ! printf '%s\n' "$HARNESS_OUT" | grep -q 'expected-diff.tsv is not stale'; then
    fail "the copied harness never reached its not-stale section, so every expectation below would be matching an empty run
$HARNESS_OUT"
  fi
  if ! printf '%s\n' "$HARNESS_OUT" | grep -q '^Results: '; then
    fail "the copied harness produced no report line, so its verdict cannot be read
$HARNESS_OUT"
  fi
}

EXPECT_EXIT() {
  local WANT="$1" WHAT="$2"
  if [ "$HARNESS_EXIT" = "$WANT" ]; then
    pass "$WHAT (exit $HARNESS_EXIT)"
  else
    fail "$WHAT: expected exit $WANT, got $HARNESS_EXIT
$HARNESS_OUT"
  fi
}

EXPECT_SAYS() {
  local NEEDLE="$1" WHAT="$2"
  if printf '%s\n' "$HARNESS_OUT" | grep -qF "$NEEDLE"; then
    pass "$WHAT"
  else
    fail "$WHAT: output did not mention '$NEEDLE'
$HARNESS_OUT"
  fi
}

EXPECT_SILENT_ABOUT() {
  local NEEDLE="$1" WHAT="$2"
  if printf '%s\n' "$HARNESS_OUT" | grep -qF "$NEEDLE"; then
    fail "$WHAT: output mentioned '$NEEDLE'
$HARNESS_OUT"
  else
    pass "$WHAT"
  fi
}

# Both rows name `status`, a matrix entry with no committed registry row, so
# nothing here overlaps what the real registry records.
ROW_STDOUT=$'status\tstdout\tsynthetic row written by differential-registry-gate.sh'
ROW_TREE=$'status\ttree\tsynthetic row written by differential-registry-gate.sh'

# Two prefix lists, both inside this gate's temp tree, so every case below
# decides the same way on a clean container and on a developer machine carrying
# a real Homebrew.
#
# ABSENT_PREFIX is deliberately a path that is never created rather than the
# empty string: the harness reads the override with `${VAR:-default}`, which
# treats an explicitly empty value as unset and hands back the two committed
# paths. Pointing the probe at a directory that does not exist is how a case
# says "no prefix here", and it keeps an accidentally-empty environment
# variable meaning the safe committed default rather than silently changing
# what CI compares.
ABSENT_PREFIX="$WORK_DIR/absent-brew/bin"
GATE_PREFIX="$WORK_DIR/fake-brew/bin"

echo ""
echo "--- preflight ---"
if [ -f "$HARNESS" ] && [ -f "$WORK_DIR/assert.sh" ]; then
  pass "the harness and assert.sh were copied into the mirrored temp tree"
else
  fail "the temp tree is missing the harness or assert.sh"
fi
# The override's default is what CI and every developer run get, and it is the
# one thing this gate cannot observe from its own runs: it always sets the
# variable. Assert the literal instead, so dropping a path from the default
# turns this gate red rather than silently re-enabling the tree field on a
# Homebrew host.
if grep -qF 'DIFFERENTIAL_BREW_PREFIXES:-/opt/homebrew/bin /home/linuxbrew/.linuxbrew/bin' "$HARNESS"; then
  pass "the prefix probe defaults to the two committed paths when the override is unset"
else
  fail "the prefix probe's default no longer names both /opt/homebrew/bin and /home/linuxbrew/.linuxbrew/bin, so an unset override changes what CI compares"
fi

echo ""
echo "--- arm 1: a row on a COMPARED field that matches nothing still fails (D-08) ---"
WRITE_STUBS identical
RUN_HARNESS "$ROW_STDOUT" "$ABSENT_PREFIX"
EXPECT_EXIT 1 "a stale row on a compared field turns the run red"
EXPECT_SAYS "remove the row: status / stdout" "the failure names the row to remove"
EXPECT_SILENT_ABOUT "NOT CHECKED: status / stdout" "a compared field is never excused as unchecked"

echo ""
echo "--- arm 2: a row on a SKIPPED field is the third state (D-07) ---"
RUN_HARNESS "$ROW_TREE" "$GATE_PREFIX"
EXPECT_EXIT 0 "a row whose field the run skipped does not fail the run"
EXPECT_SAYS "NOT CHECKED: status / tree" "the row is reported, naming the field that was skipped"
EXPECT_SILENT_ABOUT "remove the row: status / tree" "the skipped row is not reported as reverted"
EXPECT_SAYS "NOT a full verification" "the run says it was not a full verification"

echo ""
echo "--- scoping: the SAME row fails where the field WAS compared ---"
RUN_HARNESS "$ROW_TREE" "$ABSENT_PREFIX"
EXPECT_EXIT 1 "the third state is scoped to the field this run skipped, not to the field's name"
EXPECT_SAYS "remove the row: status / tree" "a tree row on a tree-comparing run is stale like any other"
EXPECT_SILENT_ABOUT "NOT CHECKED" "nothing is excused when every field was compared"

echo ""
echo "--- arm 3: a row whose difference still fires passes with its reason ---"
WRITE_STUBS status-differs
RUN_HARNESS "$ROW_STDOUT" "$ABSENT_PREFIX"
EXPECT_EXIT 0 "a registered difference that still occurs keeps the run green"
EXPECT_SAYS "registered in expected-diff.tsv" "the forgiven difference is reported with its reason"
EXPECT_SAYS "registered change still occurs: status / stdout" "the row is confirmed live by the not-stale check"

echo ""
echo "--- the committed files are untouched ---"
# shellcheck disable=SC2086 # deliberate word split: COMMITTED_FILES is a path list this file controls
if [ "$(sha256sum $COMMITTED_FILES)" = "$HASHES_BEFORE" ]; then
  pass "tests/expected-diff.tsv and tests/scenarios/differential.sh are byte-identical to what this gate found"
else
  fail "this gate changed a committed file; every fixture belongs in its temp tree
$(sha256sum $COMMITTED_FILES)"
fi

report
