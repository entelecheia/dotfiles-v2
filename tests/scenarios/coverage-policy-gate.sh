#!/usr/bin/env bash
# Scenario: coverage floor policy stays reachable and fail closed.
# shellcheck source=tests/assert.sh disable=SC1091
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

pass() {
  PASS=$((PASS + 1))
  echo "  PASS: $1"
}

fail() {
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: $1")
  echo "  FAIL: $1"
}

echo "=== Scenario: coverage-policy-gate ==="

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/coverage-policy-gate-XXXXXX")
trap 'rm -rf "$WORK_DIR"' EXIT
CONFIG="$WORK_DIR/.testcoverage.yml"
WORKFLOW="$WORK_DIR/test.yaml"
GATE="$REPO_ROOT/scripts/check-coverage-floors.sh"

CONFIG_HASH=$(shasum -a 256 "$REPO_ROOT/.testcoverage.yml")
WORKFLOW_HASH=$(shasum -a 256 "$REPO_ROOT/.github/workflows/test.yaml")

run_gate() {
  GATE_OUT=$(bash "$GATE" "$CONFIG" "$WORKFLOW" 2>&1) && GATE_EXIT=0 || GATE_EXIT=$?
}

expect_green() {
  if [ "$GATE_EXIT" -eq 0 ]; then
    pass "$1"
  else
    fail "$1: expected exit 0, got $GATE_EXIT\n$GATE_OUT"
  fi
}

expect_red_with() {
  local what="$1" needle="$2"
  if [ "$GATE_EXIT" -ne 0 ]; then
    pass "$what exits non-zero"
  else
    fail "$what: expected non-zero exit, got 0\n$GATE_OUT"
  fi
  if printf '%s' "$GATE_OUT" | grep -Fq "$needle"; then
    pass "$what says $needle"
  else
    fail "$what: output did not mention $needle\n$GATE_OUT"
  fi
}

copy_inputs() {
  cp "$REPO_ROOT/.testcoverage.yml" "$CONFIG"
  cp "$REPO_ROOT/.github/workflows/test.yaml" "$WORKFLOW"
}

echo "--- baseline ---"
copy_inputs
run_gate
expect_green "committed policy is green"
if printf '%s' "$GATE_OUT" | grep -Fq '25 package(s), 23 override(s), 2 exclusion(s)'; then
  pass "baseline reports the 25/23/2 partition"
else
  fail "baseline did not report the 25/23/2 partition\n$GATE_OUT"
fi

echo "--- workflow trigger mutation ---"
copy_inputs
grep -Fv '      - ".testcoverage.yml"' "$WORKFLOW" >"$WORKFLOW.next"
mv "$WORKFLOW.next" "$WORKFLOW"
run_gate
expect_red_with "missing floor-policy pull-request path" '.testcoverage.yml'

echo "--- zero override mutation ---"
copy_inputs
sed 's/^    threshold: 62$/    threshold: 0/' "$CONFIG" >"$CONFIG.next"
mv "$CONFIG.next" "$CONFIG"
run_gate
expect_red_with "zero override floor" '^internal/aisettings$'
expect_red_with "zero override floor" '0'

if [ "$CONFIG_HASH" = "$(shasum -a 256 "$REPO_ROOT/.testcoverage.yml")" ] && \
  [ "$WORKFLOW_HASH" = "$(shasum -a 256 "$REPO_ROOT/.github/workflows/test.yaml")" ]; then
  pass "committed policy inputs remain unchanged"
else
  fail "scenario mutated a committed policy input"
fi

report
