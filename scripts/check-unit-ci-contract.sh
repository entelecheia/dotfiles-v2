#!/usr/bin/env bash
# Fail the build when the unit job's tool/order/fail-fast contract drifts
# (BUG-18, BUG-19, G-07-08). Exact-SHA run 32977743165 failed on macOS because
# the glyph step used `rg` before any provisioning, and the default matrix
# fail-fast then cancelled the Ubuntu leg before its evidence steps. This gate
# makes that contract fail closed: unique anchors only, strict source order,
# and no selecting the first match when an anchor is missing or duplicated.
#
# Usage: check-unit-ci-contract.sh [workflow-path]
#   Defaults to .github/workflows/test.yaml relative to the caller's cwd.
set -euo pipefail

workflow=${1:-.github/workflows/test.yaml}

fail() {
  echo "check-unit-ci-contract: $*" >&2
  exit 1
}

[[ -f "$workflow" ]] || fail "workflow file $workflow does not exist"
[[ -r "$workflow" ]] || fail "workflow file $workflow is not readable"

# Count fixed-string occurrences in the half-open line range [start, end).
count_in_range() {
  awk -v s="$1" -v e="$2" -v pat="$3" \
    'NR >= s && NR < e && index($0, pat) { n++ } END { print n + 0 }' "$workflow"
}

# First matching line in the range, or empty.
line_in_range() {
  awk -v s="$1" -v e="$2" -v pat="$3" \
    'NR >= s && NR < e && index($0, pat) { print NR; exit }' "$workflow"
}

# Last matching line in the range, or empty.
last_line_in_range() {
  awk -v s="$1" -v e="$2" -v pat="$3" \
    'NR >= s && NR < e && index($0, pat) { last = NR } END { if (last) print last }' "$workflow"
}

# An anchor that occurs zero times certifies nothing, and one that occurs more
# than once makes any order comparison ambiguous; both fail rather than
# silently picking a match.
require_unique_in_range() {
  local start=$1 end=$2 pat=$3 desc=$4 n
  n=$(count_in_range "$start" "$end" "$pat")
  if [[ "$n" -eq 0 ]]; then
    fail "$desc: anchor '$pat' is missing"
  fi
  if [[ "$n" -gt 1 ]]; then
    fail "$desc: anchor '$pat' appears $n times; refusing to select the first match"
  fi
}

# --- Unit job slice -------------------------------------------------------
# Job keys sit at two-space indent; the unit slice ends where the next job
# (integration) begins, so anchors elsewhere in the workflow (for example the
# integration job's own fail-fast) cannot satisfy or pollute these checks.
unit_start=$(awk '/^  unit:$/ { print NR; exit }' "$workflow")
[[ -n "$unit_start" ]] || fail "no '  unit:' job anchor found in $workflow"
unit_end=$(awk -v s="$unit_start" \
  'NR > s && /^  [A-Za-z0-9_-]+:$/ { print NR; exit }' "$workflow")
[[ -n "$unit_end" ]] || fail "no job anchor after the unit job; cannot bound the unit slice"

count_in_unit() { count_in_range "$unit_start" "$unit_end" "$1"; }
line_in_unit() { line_in_range "$unit_start" "$unit_end" "$1"; }
require_unique_in_unit() { require_unique_in_range "$unit_start" "$unit_end" "$1" "$2"; }

# --- fail-fast (T-07-55) ----------------------------------------------------
# A single failing leg must not cancel the other platform's BUG-18/BUG-19
# evidence; the field must be explicit, not a default the runner image owns.
require_unique_in_unit "fail-fast: false" "unit matrix fail-fast"
ff_line=$(line_in_unit "fail-fast: false")

# --- Full race command keeps its selection and gains verbose evidence ------
# The packages, race mode, count, and coverage file are the existing contract;
# -v is what lets 07-17 audit named Linux scheduler, lock, and service-domain
# PASS/no-SKIP lines instead of inferring them from package-level 'ok' output.
require_unique_in_unit "go test ./... -race -count=1" "full race command"
race_line=$(line_in_unit "go test ./... -race -count=1")
race_text=$(awk -v n="$race_line" 'NR == n' "$workflow")
case " $race_text " in
  *" -v "*) ;;
  *) fail "full race command at line $race_line lacks verbose (-v) output; named Linux PASS/no-SKIP evidence would be absent" ;;
esac
case "$race_text" in
  *-coverprofile=coverage.out*) ;;
  *) fail "full race command at line $race_line no longer writes coverage.out" ;;
esac

# --- Shared ripgrep provisioning step --------------------------------------
require_unique_in_unit "name: Ensure ripgrep for glyph evidence" "ripgrep provisioning step"
ensure_line=$(line_in_unit "name: Ensure ripgrep for glyph evidence")
ensure_end=$(awk -v s="$ensure_line" -v e="$unit_end" \
  'NR > s && NR < e && /^      - / { print NR; exit }' "$workflow")
ensure_end=${ensure_end:-$unit_end}

# Both platform install branches, native package managers only: Homebrew on
# macOS, apt on Ubuntu/Linux. No downloaded installer, action, or lockfile.
require_unique_in_range "$ensure_line" "$ensure_end" "brew install ripgrep" "macOS ripgrep install branch"
require_unique_in_range "$ensure_line" "$ensure_end" "sudo apt-get update" "Ubuntu apt update branch"
require_unique_in_range "$ensure_line" "$ensure_end" "sudo apt-get install -y ripgrep" "Ubuntu ripgrep install branch"

# Any other runner OS must fail, not silently skip provisioning.
if [[ $(count_in_range "$ensure_line" "$ensure_end" "RUNNER_OS") -eq 0 ]]; then
  fail "ripgrep provisioning step never branches on RUNNER_OS; an unknown runner OS would pass unprovisioned"
fi
if [[ $(count_in_range "$ensure_line" "$ensure_end" '*)') -eq 0 ]]; then
  fail "ripgrep provisioning step has no catch-all branch rejecting unknown RUNNER_OS values"
fi

# A preinstalled but unusable tool cannot pass by assumption: the step must
# probe for absence AND finish with executable and version assertions, with
# both assertions after every install branch.
rg_cmd_count=$(count_in_range "$ensure_line" "$ensure_end" "command -v rg")
if [[ "$rg_cmd_count" -ne 2 ]]; then
  fail "ripgrep provisioning step must contain the absence probe plus the final executable assertion (2 'command -v rg' lines); found $rg_cmd_count"
fi
require_unique_in_range "$ensure_line" "$ensure_end" "rg --version" "final ripgrep version assertion"

brew_line=$(line_in_range "$ensure_line" "$ensure_end" "brew install ripgrep")
apt_line=$(line_in_range "$ensure_line" "$ensure_end" "sudo apt-get install -y ripgrep")
final_rg_line=$(last_line_in_range "$ensure_line" "$ensure_end" "command -v rg")
rgver_line=$(line_in_range "$ensure_line" "$ensure_end" "rg --version")
if [[ "$final_rg_line" -le "$brew_line" || "$final_rg_line" -le "$apt_line" ]]; then
  fail "final 'command -v rg' assertion (line $final_rg_line) must follow both install branches (brew @$brew_line, apt @$apt_line)"
fi
if [[ "$rgver_line" -le "$final_rg_line" ]]; then
  fail "'rg --version' (line $rgver_line) must follow the final 'command -v rg' assertion (line $final_rg_line)"
fi

# --- Glyph and shellcheck order --------------------------------------------
# Provisioning precedes the glyph gate; the glyph gate keeps its absolute
# /bin/bash invocation and stays ahead of the existing shellcheck gate.
require_unique_in_unit "name: Shared glyph alphabet has no inline literals" "glyph step"
glyph_line=$(line_in_unit "name: Shared glyph alphabet has no inline literals")
require_unique_in_unit "/bin/bash scripts/check-glyph-alphabet.sh" "absolute-bash glyph invocation"
if [[ "$ensure_line" -ge "$glyph_line" ]]; then
  fail "ripgrep provisioning step (line $ensure_line) must precede the glyph step (line $glyph_line)"
fi

require_unique_in_unit "name: Ensure shellcheck (macOS)" "shellcheck provisioning step"
require_unique_in_unit "name: Shellcheck scripts" "shellcheck step"
shellcheck_line=$(line_in_unit "name: Shellcheck scripts")
if [[ "$glyph_line" -ge "$shellcheck_line" ]]; then
  fail "glyph step (line $glyph_line) must precede the shellcheck step (line $shellcheck_line)"
fi

echo "check-unit-ci-contract: unit job lines $unit_start-$unit_end of $workflow"
echo "check-unit-ci-contract: fail-fast:false @$ff_line; verbose race @$race_line; ripgrep step lines $ensure_line-$ensure_end (brew @$brew_line, apt @$apt_line, command -v rg @$final_rg_line, rg --version @$rgver_line); glyph @$glyph_line; shellcheck @$shellcheck_line"
echo "check-unit-ci-contract: all anchors unique; provisioning precedes glyph; glyph precedes shellcheck; both platform install branches and final tool assertions present"
