#!/usr/bin/env bash
# Fail the build when one of Phase 09's vacuity guards stops discriminating.
#
# Phase 09 removed a class of defect: a CI step that reports green having
# measured nothing. It left five guards behind, each proven once by an ad-hoc
# local fixture during execution and then never re-run. A guard that has only
# ever been observed passing is indistinguishable from a guard that does not
# run, which is the same defect one level up. This gate is the standing version
# of those one-time proofs.
#
#   G1  tests/scenarios/sync.sh's rsync/git precondition exits non-zero, and its
#       column-one diagnostic still trips the workflow's ^SKIP: arm (D-09).
#   G2  sync.sh's final verdict conjoins the pass counter, so a run of 0 passed
#       and 0 failed is a failure (D-10).
#   G3  the linux job's Scenarios step fails a zero-entry SCENARIOS, an empty
#       log and a column-one SKIP:, while the deliberate indented
#       `~ SKIPPED:` partial skips stay green (D-08).
#   G4  that loop's verdict is order-independent: every failing scenario is
#       accumulated and none exits the loop early.
#   G5  both unit-job lint steps fail when their globs match no file (D-04).
#
# Two rules shape every arm below.
#
# First, each arm runs the guard's OWN text: the three step bodies are pulled
# out of the workflow with yaml.safe_load and executed, and sync.sh is copied
# byte-for-byte. A test written against a retyped paraphrase proves nothing
# about what CI actually runs.
#
# Second, each arm is paired with a mutant in which that one guard is disabled
# and nothing else changes. "It went red" is not evidence that the guard is what
# turned it red; the mutant going green next to it is.
#
# `docker run` is stubbed from a fixture directory, so this needs no image and
# no daemon and runs on any runner.
#
# Usage: check-vacuity-guards.sh [workflow-path] [sync-scenario-path]
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)

workflow=${1:-$repo_root/.github/workflows/test.yaml}
sync_scenario=${2:-$repo_root/tests/scenarios/sync.sh}

abort() {
  echo "check-vacuity-guards: $*" >&2
  exit 1
}

[ -r "$workflow" ] || abort "workflow file $workflow is not readable"
[ -r "$sync_scenario" ] || abort "scenario file $sync_scenario is not readable"
command -v python3 >/dev/null || abort "python3 is required to extract the workflow step bodies"
command -v shellcheck >/dev/null || abort "shellcheck is required: the populated arm of the shellcheck step body invokes it"

PASS=0
FAIL=0
pass() {
  PASS=$((PASS + 1))
  echo "  ✓ $1"
}
fail() {
  FAIL=$((FAIL + 1))
  echo "  ✗ $1"
}

work=$(mktemp -d "${TMPDIR:-/tmp}/vacuity-guards-XXXXXX")
trap 'rm -rf "$work"' EXIT

RC=0
OUT=""

# Run a command in a directory, capturing its combined output and exit status.
run_in() {
  local dir=$1
  shift
  RC=0
  OUT=$(cd "$dir" && "$@" 2>&1) || RC=$?
}

expect_rc() {
  local want=$1 what=$2
  if [ "$RC" -eq "$want" ]; then
    pass "$what"
  else
    fail "$what: expected exit $want, got $RC"$'\n'"$OUT"
  fi
}

expect_nonzero() {
  local what=$1
  if [ "$RC" -ne 0 ]; then
    pass "$what"
  else
    fail "$what: expected a non-zero exit, got 0"$'\n'"$OUT"
  fi
}

expect_out() {
  local needle=$1 what=$2
  if printf '%s\n' "$OUT" | grep -Fq -- "$needle"; then
    pass "$what"
  else
    fail "$what: output did not contain '$needle'"$'\n'"$OUT"
  fi
}

refute_out() {
  local needle=$1 what=$2
  if printf '%s\n' "$OUT" | grep -Fq -- "$needle"; then
    fail "$what: output unexpectedly contained '$needle'"$'\n'"$OUT"
  else
    pass "$what"
  fi
}

# Whole-line match: proves both the wording and the column-one position, which
# is what the workflow's line-anchored grep depends on.
expect_line() {
  local line=$1 what=$2
  if printf '%s\n' "$OUT" | grep -Fxq -- "$line"; then
    pass "$what"
  else
    fail "$what: no line of the output was exactly '$line'"$'\n'"$OUT"
  fi
}

# Disable exactly one guard and change nothing else. A target that matches zero
# or several lines aborts rather than silently mutating the wrong thing, so a
# reworded guard fails this gate loudly instead of quietly losing its mutant.
mutate_once() {
  local src=$1 dst=$2 old=$3 new=$4 hits
  hits=$(awk -v old="$old" 'index($0, old) { n++ } END { print n + 0 }' "$src")
  if [ "$hits" -ne 1 ]; then
    abort "mutation target matched $hits line(s), expected 1, in $src: $old"
  fi
  awk -v old="$old" -v new="$new" '
    {
      i = index($0, old)
      if (i > 0) {
        print substr($0, 1, i - 1) new substr($0, i + length(old))
      } else {
        print
      }
    }
  ' "$src" >"$dst"
}

# sync.sh carries two `exit 1` lines at the same indent, so the precondition
# bail is addressed by its context: the one directly under the SKIP diagnostic.
mutate_skip_exit() {
  awk '
    prev ~ /SKIP: rsync and git are required/ && $0 ~ /^[[:space:]]*exit 1$/ {
      sub(/exit 1/, "exit 0")
      n++
    }
    { print; prev = $0 }
    END { if (n != 1) exit 3 }
  ' "$1" >"$2" || abort "could not find exactly one 'exit 1' under the SKIP diagnostic in $1"
}

# --- The step bodies CI actually runs ---------------------------------------
python3 - "$workflow" "$work" <<'PY' || abort "could not extract the workflow step bodies"
import sys

import yaml

workflow_path, outdir = sys.argv[1], sys.argv[2]
with open(workflow_path) as handle:
    workflow = yaml.safe_load(handle)

targets = {
    "scenarios-body.sh": ("linux", "failed scenarios:"),
    "shellcheck-body.sh": ("unit", "shellcheck lint coverage:"),
    "bash-n-body.sh": ("unit", "bash -n syntax coverage:"),
}

for filename, (job, marker) in sorted(targets.items()):
    steps = workflow["jobs"][job]["steps"]
    hits = [s["run"] for s in steps if isinstance(s.get("run"), str) and marker in s["run"]]
    if len(hits) != 1:
        sys.exit(
            "check-vacuity-guards: marker %r matched %d run bodies in job %s; "
            "refusing to guess which one CI executes" % (marker, len(hits), job)
        )
    with open("%s/%s" % (outdir, filename), "w") as handle:
        handle.write(hits[0])
PY

for extracted in scenarios-body.sh shellcheck-body.sh bash-n-body.sh; do
  [ -s "$work/$extracted" ] || abort "extracted step body $extracted is empty"
done
echo "check-vacuity-guards: extracted 3 step bodies from $workflow (linux Scenarios, unit shellcheck, unit bash -n)"
echo

# --- Scenarios-loop harness --------------------------------------------------
fixtures="$work/fixtures"
loopdir="$work/loop"
mkdir -p "$fixtures" "$loopdir"

# fixture <name> <exit-status> [output-line...]
fixture() {
  local name=$1 status=$2
  shift 2
  if [ "$#" -gt 0 ]; then
    printf '%s\n' "$@" >"$fixtures/$name.out"
  else
    : >"$fixtures/$name.out"
  fi
  printf '%s\n' "$status" >"$fixtures/$name.rc"
}

cat >"$work/docker-stub.sh" <<'STUB'
# The Scenarios body appended below is the workflow's own text. The only thing
# it needs from the environment is a `docker run` that yields a scenario's
# output and exit status; $FIXTURES/<name>.out and <name>.rc supply both.
docker() {
  local last=""
  local arg
  for arg in "$@"; do last="$arg"; done
  local name
  name=$(basename "$last" .sh)
  cat "$FIXTURES/$name.out"
  return "$(cat "$FIXTURES/$name.rc")"
}
STUB

# run_loop <scenarios-string> <body-file-name>
run_loop() {
  local scenarios=$1 body=$2
  cat "$work/docker-stub.sh" "$work/$body" >"$work/loop-run.sh"
  rm -f "$loopdir"/scenario-*.log
  RC=0
  OUT=$(cd "$loopdir" && env SCENARIOS="$scenarios" TAG=stub FIXTURES="$fixtures" \
    bash -e "$work/loop-run.sh" 2>&1) || RC=$?
}

# The verdict line may repeat a name: a scenario that both exits non-zero and
# prints a column-one SKIP: lands in `failed` twice (observed as `sync sync` in
# run 33261038906). That duplication is accepted, so compare sets, not strings.
failed_set() {
  printf '%s\n' "$OUT" |
    sed -n 's/^failed scenarios: //p' |
    tr ' ' '\n' |
    sed '/^$/d' |
    LC_ALL=C sort -u |
    tr '\n' ' ' |
    sed 's/[[:space:]]*$//'
}

fixture empty 0
fixture skipper 0 'SKIP: rsync and git are required'
fixture partial 0 '  ~ SKIPPED: the registry arm' '  PASS: everything else'
fixture normal 0 '  PASS: a' '=== Results: 1 passed, 0 failed ==='
fixture alpha 1 '  FAIL: an ordinary red scenario'

# --- G5: both unit-job lint steps fail on an empty glob set ------------------
echo "=== G5 (CI-03): a lint step whose globs match nothing fails ==="
lint_tree="$work/lint-tree"
mkdir -p "$lint_tree/scripts" "$lint_tree/tests/scenarios"

check_lint_body() {
  local body=$1 title=$2 evidence=$3

  run_in "$lint_tree" bash -e "$work/$body"
  expect_nonzero "$body fails with scripts/ and tests/scenarios/ empty"
  expect_out "::error title=$title::" "$body attributes it to $title"
  expect_out "measured nothing" "$body says it would have reported success having measured nothing"

  printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' 'echo covered' \
    >"$lint_tree/scripts/covered.sh"
  run_in "$lint_tree" bash -e "$work/$body"
  expect_rc 0 "$body passes once one file matches"
  expect_out "$evidence 1 shell script(s)" "$body counts the single file it measured"

  # Same body, byte-identical, one file fewer: the count is what moves, so the
  # red above is the empty set and not a body that cannot run here at all.
  rm -f "$lint_tree/scripts/covered.sh"
  run_in "$lint_tree" bash -e "$work/$body"
  expect_nonzero "$body goes red again when that one file is removed"
}

check_lint_body shellcheck-body.sh "Shellcheck coverage" "shellcheck lint coverage: scanned"
check_lint_body bash-n-body.sh "Syntax-check coverage" "bash -n syntax coverage: checked"
echo

# --- G3: the Scenarios loop's three arms ------------------------------------
echo "=== G3 (CI-02): the Scenarios loop decides each arm correctly ==="
run_loop "" scenarios-body.sh
expect_nonzero "a zero-entry SCENARIOS fails the job"
expect_out "SCENARIOS resolved to zero entries" "  and names the empty list"

run_loop "empty" scenarios-body.sh
expect_nonzero "a scenario that writes nothing fails"
expect_out "::error title=Scenario produced no output::" "  through the empty-log arm"
refute_out "::error title=Scenario skipped::" "  and silence never satisfies the skip grep by absence"
expect_out "failed scenarios: empty" "  and the failure survives to the post-loop verdict"

run_loop "skipper" scenarios-body.sh
expect_nonzero "a column-one SKIP: fails the job even when the container exits 0"
expect_out "::error title=Scenario skipped::" "  through the ^SKIP: arm"
refute_out "::error title=Scenario produced no output::" "  and not by mistaking output for silence"

run_loop "partial" scenarios-body.sh
expect_rc 0 "an indented '~ SKIPPED:' partial skip stays green"

run_loop "normal" scenarios-body.sh
expect_rc 0 "ordinary PASS output stays green"

mutate_once "$work/scenarios-body.sh" "$work/loop-no-zero-guard.sh" \
  'if [ "$(echo $SCENARIOS | wc -w)" -eq 0 ]; then' 'if false; then'
run_loop "" loop-no-zero-guard.sh
expect_rc 0 "with only the zero-entry guard disabled the same empty list passes"

mutate_once "$work/scenarios-body.sh" "$work/loop-no-empty-arm.sh" \
  'if [ ! -s "$log" ]; then' 'if false; then'
run_loop "empty" loop-no-empty-arm.sh
expect_rc 0 "with only the empty-log arm disabled a silent scenario passes"

mutate_once "$work/scenarios-body.sh" "$work/loop-no-skip-arm.sh" \
  'elif grep -q "^SKIP:" "$log"; then' 'elif false; then'
run_loop "skipper" loop-no-skip-arm.sh
expect_rc 0 "with only the ^SKIP: arm disabled a bailing scenario passes"

run_loop "empty" loop-no-skip-arm.sh
expect_nonzero "silence still fails with the skip grep gone, so the empty-log arm is what catches it"
echo

# --- G4: order independence --------------------------------------------------
echo "=== G4 (CI-02): the loop's verdict does not depend on SCENARIOS order ==="
run_loop "alpha normal skipper partial empty" scenarios-body.sh
expect_nonzero "the default order fails when three of its five scenarios are bad"
default_failed=$(failed_set)
default_groups=$(printf '%s\n' "$OUT" | grep -c '^::group::scenario ' || true)

run_loop "empty partial skipper normal alpha" scenarios-body.sh
expect_nonzero "the shuffled order, whose first scenario is bad, fails too"
shuffled_failed=$(failed_set)
shuffled_groups=$(printf '%s\n' "$OUT" | grep -c '^::group::scenario ' || true)

# Non-emptiness is part of the condition: two orders that both collapsed to no
# verdict line at all would otherwise compare equal and pass having measured
# nothing.
if [ -n "$default_failed" ] && [ "$default_failed" = "$shuffled_failed" ]; then
  pass "both orders report the same failing scenarios: $default_failed"
else
  fail "order changed the verdict: default '$default_failed' vs shuffled '$shuffled_failed'"
fi
if [ "$default_failed" = "alpha empty skipper" ]; then
  pass "every bad scenario is accumulated, none of the three is lost"
else
  fail "expected the failing set 'alpha empty skipper', got '$default_failed'"
fi
if [ "$default_groups" -eq 5 ] && [ "$shuffled_groups" -eq 5 ]; then
  pass "all five scenarios ran under both orders, so a red one does not exit the loop"
else
  fail "loop stopped early: $default_groups groups in the default order, $shuffled_groups shuffled (expected 5 and 5)"
fi
echo

# --- G1: sync.sh's precondition bail -----------------------------------------
echo "=== G1 (CI-02): sync.sh bails non-zero and keeps its anchor line ==="
shim="$work/shim"
mkdir -p "$shim"
for tool in dirname basename; do
  tool_path=$(command -v "$tool") || abort "$tool not found; sync.sh's BIN normalization needs it in the PATH shim"
  ln -s "$tool_path" "$shim/$tool"
done
bash_bin=$(command -v bash)

# A PATH holding only dirname and basename, not an empty one: an empty PATH
# aborts at the BIN normalization with rc 127 before the diagnostic is ever
# printed, which would make the exit status prove nothing about this guard.
cp "$sync_scenario" "$work/sync-real.sh"

run_in "$work" env -i "PATH=$shim" "$bash_bin" "$work/sync-real.sh" "$bash_bin"
expect_nonzero "sync.sh exits non-zero when rsync and git are absent"
expect_line "SKIP: rsync and git are required" "the diagnostic is unchanged and at column one"
skip_output=$OUT

# The mutant is built after the assertion above, so a sync.sh that regressed to
# `exit 0` fails that assertion by name instead of aborting here on a missing
# mutation target.
mutate_skip_exit "$work/sync-real.sh" "$work/sync-exit0.sh"
run_in "$work" env -i "PATH=$shim" "$bash_bin" "$work/sync-exit0.sh" "$bash_bin"
expect_rc 0 "the same run exits 0 with that one 'exit 1' turned back to 'exit 0'"

# The column-one position is a contract with the workflow's ^SKIP: anchor, so
# assert it by feeding sync.sh's real output to the real loop rather than by
# grepping the source for the string.
printf '%s\n' "$skip_output" >"$fixtures/sync.out"
printf '%s\n' 1 >"$fixtures/sync.rc"
run_loop "sync" scenarios-body.sh
expect_nonzero "sync.sh's own bail output fails the workflow loop"
expect_out "::error title=Scenario skipped::" "the ^SKIP: anchor still matches what sync.sh actually prints"
if [ "$(failed_set)" = "sync" ]; then
  pass "the verdict names sync, tolerating the accepted double append"
else
  fail "expected the failing set 'sync', got '$(failed_set)'"
fi
echo

# --- G2: sync.sh's final verdict ---------------------------------------------
echo "=== G2 (CI-02): 0 passed / 0 failed is a failure ==="
awk -v anchor='=== Results: $PASS passed, $FAIL failed ===' '
  index($0, anchor) { found = 1 }
  found { print }
  END { if (!found) exit 3 }
' "$sync_scenario" >"$work/verdict-tail.sh" || abort "could not locate sync.sh's verdict tail"


# verdict_case <tail-file-name> <PASS> <FAIL>
verdict_case() {
  {
    echo 'set -euo pipefail'
    echo "PASS=$2"
    echo "FAIL=$3"
    cat "$work/$1"
  } >"$work/verdict-case.sh"
  run_in "$work" bash -e "$work/verdict-case.sh"
}

verdict_case verdict-tail.sh 0 0
expect_nonzero "the verdict fails a run that measured nothing"
expect_line "=== Results: 0 passed, 0 failed ===" "  having reached the verdict, not died earlier"

verdict_case verdict-tail.sh 17 0
expect_rc 0 "the verdict still passes a real green run of 17 assertions"

verdict_case verdict-tail.sh 17 1
expect_nonzero "the verdict still fails a run with one failed assertion"

# Built after the assertions above, for the same reason as G1's mutant.
mutate_once "$work/verdict-tail.sh" "$work/verdict-tail-nopass.sh" ' && [ "$PASS" -gt 0 ]' ''
verdict_case verdict-tail-nopass.sh 0 0
expect_rc 0 "with only the pass-counter conjunct removed the same 0/0 run passes"
echo

echo "check-vacuity-guards: === Results: $PASS passed, $FAIL failed ==="
# D-10's own shape, applied to this gate: an extraction that silently produced
# nothing to run must not be able to report success.
[ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ]
