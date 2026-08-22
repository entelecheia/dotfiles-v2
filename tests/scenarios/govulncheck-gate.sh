#!/usr/bin/env bash
# Scenario: scripts/govulncheck-gate.sh decides correctly on every branch
#
# The gate is the only thing standing between a called CVE and a green build,
# and it is bash, so nothing in `go test ./...` covers it. Its branches were
# each proven by hand at authoring time -- and a one-time proof does not survive
# a refactor, which is exactly how two of them silently broke:
#
#   - `mktemp -t <bare prefix>` was rejected by GNU mktemp, so the gate exited 1
#     on every Linux run without evaluating a single finding (fixed in 5fea20d)
#   - an empty scan file made `jq -s` read [] and the gate report
#     "0 finding(s) seen" and pass, never having seen evidence a scan ran
#   - `expires: "never"` sorted above every ISO date under bash lexical
#     comparison and suppressed a finding indefinitely (both fixed in 3ceda5d)
#
# This scenario is the standing version of those proofs. A gate that has only
# ever been observed passing is indistinguishable from a gate that does not run.
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

echo "=== Scenario: govulncheck-gate ==="

# This scenario tests a REPO script, not the installed binary, so it needs the
# repo checked out. It runs in the `lint` job rather than in the ubuntu:22.04
# container, where /tests/scenarios has no repo above it.
REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
FIXTURES="$REPO_ROOT/tests/fixtures/govulncheck"

# The gate resolves its allowlist relative to its OWN directory, so exercising
# the allowlist branches means controlling that file. Copy the gate into a temp
# dir and write allowlists next to the copy: the committed
# .govulncheck-allow.json is never touched, so this scenario cannot leave a
# mutated policy file behind if it dies mid-run, and it is safe to run
# alongside the real gate in the same job.
# The gate resolves the allowlist as <its own dir>/../.govulncheck-allow.json,
# so the temp tree has to mirror the repo's shape: gate under scripts/, policy
# file one level up.
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/govulncheck-gate-scenario-XXXXXX")
trap 'rm -rf "$WORK_DIR"' EXIT
mkdir -p "$WORK_DIR/scripts"
cp "$REPO_ROOT/scripts/govulncheck-gate.sh" "$WORK_DIR/scripts/govulncheck-gate.sh"
GATE="$WORK_DIR/scripts/govulncheck-gate.sh"
ALLOW="$WORK_DIR/.govulncheck-allow.json"

# run_gate <fixture> -> prints combined output, sets GATE_EXIT
run_gate() {
  GATE_OUT=$(bash "$GATE" "$FIXTURES/$1" 2>&1) && GATE_EXIT=0 || GATE_EXIT=$?
}

expect_exit() {
  local want="$1" what="$2"
  if [ "$GATE_EXIT" = "$want" ]; then
    pass "$what (exit $GATE_EXIT)"
  else
    fail "$what: expected exit $want, got $GATE_EXIT
$GATE_OUT"
  fi
}

expect_says() {
  local needle="$1" what="$2"
  if printf '%s' "$GATE_OUT" | grep -q "$needle"; then
    pass "$what"
  else
    fail "$what: output did not mention '$needle'
$GATE_OUT"
  fi
}

echo "--- preflight ---"
if [ -f "$REPO_ROOT/scripts/govulncheck-gate.sh" ]; then
  pass "gate script exists"
else
  fail "gate script missing at $REPO_ROOT/scripts/govulncheck-gate.sh"
fi
if [ -f "$REPO_ROOT/.govulncheck-allow.json" ]; then
  pass "committed allowlist exists"
else
  fail "committed allowlist missing"
fi
for f in clean.json called-unallowed.json empty.json no-config.json; do
  if [ -f "$FIXTURES/$f" ]; then pass "fixture present: $f"; else fail "fixture missing: $f"; fi
done

echo "--- a clean scan passes ---"
printf '{"_comment":"scenario","allow":[]}\n' > "$ALLOW"
run_gate clean.json
expect_exit 0 "clean scan is green"
expect_says "0 called" "clean scan reports what it saw"

echo "--- a called, unallowlisted finding fails ---"
run_gate called-unallowed.json
expect_exit 1 "called finding turns the build red"
expect_says "GO-2026-5970" "the failure names the finding"

echo "--- the gate is not vacuous: no scan, no verdict ---"
run_gate empty.json
expect_exit 1 "empty scanner output is refused, not read as zero findings"
expect_says "no scanner config record" "the refusal says why"

run_gate no-config.json
expect_exit 1 "a findings stream with no config record is refused"

echo "--- allowlist suppresses only a live, well-formed entry ---"
printf '{"allow":[{"id":"GO-2026-5970","module":"golang.org/x/text","expires":"2099-01-01","reason":"scenario"}]}\n' > "$ALLOW"
run_gate called-unallowed.json
expect_exit 0 "a live allowlist entry suppresses the finding"
expect_says "1 suppressed" "the suppression is reported, not silent"

printf '{"allow":[{"id":"GO-2026-5970","module":"golang.org/x/text","expires":"2020-01-01","reason":"scenario"}]}\n' > "$ALLOW"
run_gate called-unallowed.json
expect_exit 1 "an expired entry stops suppressing"
expect_says "expired" "the failure says the entry expired"

printf '{"allow":[{"id":"GO-2026-5970","module":"golang.org/x/text","expires":"never","reason":"scenario"}]}\n' > "$ALLOW"
run_gate called-unallowed.json
expect_exit 1 "a malformed expires cannot buy indefinite suppression"
expect_says "malformed expires" "the failure names the malformed value"

printf '{"allow":[{"id":"GO-2026-5970","module":"stdlib","expires":"2099-01-01","reason":"scenario"}]}\n' > "$ALLOW"
run_gate clean.json
expect_exit 1 "a stdlib allowlist entry is rejected outright"
expect_says "never allowlistable" "the failure explains stdlib is never allowlistable"

report
