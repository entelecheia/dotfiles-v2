#!/usr/bin/env bash
# Scenario: the seam lint rules (forbidigo, depguard) fail on real violations
#
# A lint rule that has only ever been observed passing is indistinguishable
# from a rule that matches nothing. forbidigo and depguard were enabled against
# a tree that plan 02-01 had already cleaned, so a green lint job on its own
# proves nothing about them. This scenario writes a violating Go file into an
# engine package, watches golangci-lint fire, and removes the file -- the
# standing proof that both rules are load-bearing. The internal/ui case proves
# the forbidigo exclusion is scoped to that path rather than disabling the rule
# everywhere.
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

echo "=== Scenario: lint-seams-gate ==="

# This scenario writes fixtures into repo packages and lints them with the
# repo's own config, so it needs the repo checked out. It runs in the `lint`
# job rather than in the ubuntu container, where /tests/scenarios has no repo
# above it.
REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$REPO_ROOT"

# Every fixture path is collected in FIXTURES and removed by the EXIT trap.
# The trap is registered before any fixture is written and before the linter
# ever runs, so a failure partway through cannot leave a Go file in the tree
# for a later commit to sweep up.
FIXTURES=()
trap 'for f in ${FIXTURES[@]+"${FIXTURES[@]}"}; do rm -f "$REPO_ROOT/$f"; done' EXIT

# write_fixture <repo-relative path> -- reads the Go source on stdin.
write_fixture() {
  cat > "$REPO_ROOT/$1"
  FIXTURES+=("$1")
}

clear_fixtures() {
  for f in ${FIXTURES[@]+"${FIXTURES[@]}"}; do rm -f "$REPO_ROOT/$f"; done
  FIXTURES=()
}

# run_lint <package pattern> -- combined output in LINT_OUT, status in LINT_EXIT.
run_lint() {
  LINT_OUT=$(golangci-lint run "$1" 2>&1) && LINT_EXIT=0 || LINT_EXIT=$?
}

expect_red() {
  local needle="$1" what="$2"
  if [ "$LINT_EXIT" -ne 0 ] && printf '%s' "$LINT_OUT" | grep -q "$needle"; then
    pass "$what (exit $LINT_EXIT)"
  else
    fail "$what: expected non-zero exit naming '$needle', got exit $LINT_EXIT
$LINT_OUT"
  fi
}

echo "--- preflight ---"
if command -v golangci-lint >/dev/null 2>&1; then
  pass "golangci-lint is on PATH"
else
  fail "golangci-lint not found on PATH"
fi
if [ -f "$REPO_ROOT/.golangci.yml" ]; then
  pass "lint config exists"
else
  fail "lint config missing at $REPO_ROOT/.golangci.yml"
fi

echo "--- a package-level formatted print in an engine package is red ---"
write_fixture internal/module/zz_lint_seams_gate_fixture.go <<'EOF'
package module

import "fmt"

func zzLintSeamsGatePrint() {
	fmt.Print("lint-seams-gate fixture")
}
EOF
run_lint ./internal/module/...
expect_red "forbidigo" "fmt.Print in an engine package fails the lint gate"

echo "--- the same print under the wizard path sees no forbidigo finding ---"
# Assert on the ABSENCE of a forbidigo finding, not on exit 0: this fixture is
# an unreferenced unexported function, so the already-enabled `unused` linter
# fires on it and the exit code is non-zero for a reason that has nothing to do
# with the rule under test. Asserting exit 0 here would fail on a correct
# configuration, which is the worst kind of gate.
write_fixture internal/ui/zz_lint_seams_gate_fixture.go <<'EOF'
package ui

import "fmt"

func zzLintSeamsGatePrint() {
	fmt.Print("lint-seams-gate fixture")
}
EOF
run_lint ./internal/ui/...
if printf '%s' "$LINT_OUT" | grep -q "forbidigo"; then
  fail "forbidigo fired under the wizard path; the exclusion is not scoped to it
$LINT_OUT"
else
  pass "no forbidigo finding under the wizard path (exclusion is path-scoped, not a global disable)"
fi
rm -f "$REPO_ROOT/internal/ui/zz_lint_seams_gate_fixture.go"
FIXTURES=("internal/module/zz_lint_seams_gate_fixture.go")

echo "--- a cobra import in an engine package is red ---"
write_fixture internal/module/zz_lint_seams_gate_cobra_fixture.go <<'EOF'
package module

import "github.com/spf13/cobra"

func zzLintSeamsGateCobra() *cobra.Command {
	return &cobra.Command{Use: "lint-seams-gate"}
}
EOF
run_lint ./internal/module/...
expect_red "depguard" "cobra import in an engine package fails the lint gate"

echo "--- the untouched tree is green ---"
# The baseline that makes the red assertions above meaningful rather than
# measurements of a permanently red build.
clear_fixtures
run_lint ./...
if [ "$LINT_EXIT" -eq 0 ]; then
  pass "golangci-lint run ./... exits 0 on the untouched tree"
else
  fail "golangci-lint run ./... failed on the untouched tree (exit $LINT_EXIT)
$LINT_OUT"
fi

echo "--- the scenario leaves the tree clean ---"
LEFTOVER=$(git status --porcelain internal/ tests/ .golangci.yml)
if [ -z "$LEFTOVER" ]; then
  pass "git status --porcelain internal/ tests/ .golangci.yml is empty"
else
  fail "scenario left the tree dirty:
$LEFTOVER"
fi

report
