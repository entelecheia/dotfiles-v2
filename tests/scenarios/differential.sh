#!/usr/bin/env bash
# Scenario: differential — the binary built from the pinned milestone-start
# commit against the binary built from the current tree, over the read and
# --dry-run surfaces, diffing stdout, stderr, exit code and filesystem tree
# (GUARD-05)
set -euo pipefail
# shellcheck source=tests/assert.sh disable=SC1091
source "$(dirname "$0")/../assert.sh"

# Pinned so the mode field the tree snapshot records is host-independent.
# Without it a host running under umask 077 reports a mode difference that is
# the developer's shell talking, not the binary's behavior.
umask 022

START_SECONDS=$SECONDS
BUDGET_SECONDS=60
EXCERPT_LINES=40

# D-02: fixed for the whole milestone and moved only by hand, so accumulated
# drift cannot creep in one phase at a time. The workflow that builds the
# baseline carries the same SHA; this copy is here so a failure message can name
# what `dot-baseline` actually is.
BASELINE_COMMIT="db057e7ebbd21e0a7256dcd7c1b26ff3870a5478"

# Local helpers on top of assert.sh counters (no generic pass/fail there).
# Both append to ERRORS: a variant that only bumps the counter drops the failure
# detail from the terminal report.
pass() {
  PASS=$((PASS + 1))
  echo "  ✓ $1"
}
fail() {
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: $1")
  echo "  ✗ $1"
}

# ---------------------------------------------------------------------------
# The command matrix. Read and --dry-run surfaces only: this harness must never
# mutate anything outside the temp HOME it creates for each individual run.
# Ordered cheapest first so a failure surfaces early.
#
# `version` is deliberately IN the matrix rather than excluded. Its build
# metadata is normalized at the build step (identical -ldflags plus a pinned
# GOTOOLCHAIN, .github/workflows/test.yaml), so including it turns that
# normalization into something this harness proves on every run rather than
# something a comment claims. It is forbidden from the registry — see
# CHECK_REGISTRY_ROW below.
# ---------------------------------------------------------------------------
MATRIX=(
  "--help"
  "version"
  "status"
  "check"
  "diff"
  "sync status --json"
  "peer status --json"
  "ai skills status --json"
  "apply --profile minimal --yes --dry-run"
  "apply --profile full --yes --dry-run"
  "apply --profile server --yes --dry-run"
)

# ---------------------------------------------------------------------------
# The expected-diff registry (D-04), loaded once into four parallel indexed
# arrays. bash 3.2 compatible: no associative array, no readarray.
# ---------------------------------------------------------------------------
REGISTRY="$(dirname "$0")/../expected-diff.tsv"
REG_COMMANDS=()
REG_FIELDS=()
REG_REASONS=()
REG_SEEN=()

# CHECK_REGISTRY_ROW rejects a row this file is not allowed to hold. Enforced
# here rather than left as a comment in the TSV, because a rule nobody runs is
# a rule that erodes: a `version` row would silently forgive exactly the
# build-metadata drift the normalization exists to remove.
CHECK_REGISTRY_ROW() {
  if [ "$1" = "version" ]; then
    fail "expected-diff.tsv registers the 'version' command (field: $2), which is forbidden: both binaries are built with identical -ldflags and a pinned GOTOOLCHAIN, so a version difference means the build normalization broke rather than that behavior changed. Fix the build, remove the row."
    return 1
  fi
  return 0
}

LOAD_REGISTRY() {
  local R_COMMAND R_FIELD R_REASON
  while IFS=$'\t' read -r R_COMMAND R_FIELD R_REASON || [ -n "${R_COMMAND:-}" ]; do
    case "$R_COMMAND" in
      '#'*) continue ;;
    esac
    [ -n "${R_COMMAND// /}" ] || continue
    CHECK_REGISTRY_ROW "$R_COMMAND" "$R_FIELD" || continue
    REG_COMMANDS+=("$R_COMMAND")
    REG_FIELDS+=("$R_FIELD")
    REG_REASONS+=("$R_REASON")
    REG_SEEN+=(0)
  done < "$REGISTRY"
}

# REGISTRY_MATCH prints the index of the row registering this command/field
# pair, or returns 1 when the difference is unregistered.
REGISTRY_MATCH() {
  local WANT_COMMAND="$1"
  local WANT_FIELD="$2"
  local I=0
  while [ "$I" -lt "${#REG_COMMANDS[@]}" ]; do
    if [ "${REG_COMMANDS[$I]}" = "$WANT_COMMAND" ] && [ "${REG_FIELDS[$I]}" = "$WANT_FIELD" ]; then
      printf '%s' "$I"
      return 0
    fi
    I=$((I + 1))
  done
  return 1
}

# ---------------------------------------------------------------------------
# SNAPSHOT_TREE records the WHOLE tree — relative path, entry type and mode for
# directories, symlinks and regular files alike, plus a content hash for regular
# files. The two tree-comparison helpers in tests/assert.sh are deliberately not
# used: they walk with `find -type f`, so they miss directories and symlinks,
# and they hash content but not modes. `dot` writes symlinks as its primary
# artifact, so a symlink-blind or mode-blind snapshot cannot support a claim
# about the whole tree. Both helpers also have zero callers in this repo, so
# reusing them would not be the free win it looks like.
#
# GNU findutils and coreutils are installed in tests/Dockerfile.ubuntu-22.04.
# ---------------------------------------------------------------------------
SNAPSHOT_TREE() {
  local DIR="$1"
  find "$DIR" -mindepth 1 -printf '%P\t%y\t%m\n' |
    while IFS=$'\t' read -r ENTRY_PATH ENTRY_TYPE ENTRY_MODE; do
      if [ "$ENTRY_TYPE" = "f" ]; then
        printf '%s\t%s\t%s\t%s\n' "$ENTRY_PATH" "$ENTRY_TYPE" "$ENTRY_MODE" \
          "$(sha256sum "$DIR/$ENTRY_PATH" | cut -d' ' -f1)"
      else
        printf '%s\t%s\t%s\n' "$ENTRY_PATH" "$ENTRY_TYPE" "$ENTRY_MODE"
      fi
    done | LC_ALL=C sort
}

# NORMALIZE_OUTPUT erases exactly two things, both of which differ between two
# runs of the SAME binary and therefore carry no behavior signal at all. A
# normalizer that erases anything beyond that is erasing the signal this harness
# exists to carry.
#
#   1. This run's own temp HOME -> @HOME@. Two roots, not one: mktemp hands back
#      /tmp/... while the binary may report the resolved /private/tmp/... form.
#      The longer path is substituted first so the shorter one cannot
#      half-replace a nested match and leave a prefix behind.
#
#   2. The wall-clock value of slog's `time=` attribute -> @TIMESTAMP@.
#      slog.NewTextHandler stamps every record with time.Now() in RFC3339 and
#      the binary exposes no flag to suppress it, so `dot apply --dry-run` emits
#      three log lines per profile that never match across runs. Only the VALUE
#      is masked; the `time=` key, the level, the message and every other
#      attribute are compared byte for byte, so a refactor that drops, reorders
#      or reworders a log line is still caught. Two sed expressions rather than
#      one alternation because BSD sed has no \| in a basic regex.
#
#      This is deliberately fixed here and not in tests/expected-diff.tsv. A
#      registry row for it would be a row added to turn a red build green rather
#      than to record a decision, which is precisely what that file forbids.
NORMALIZE_OUTPUT() {
  local FILE="$1"
  local LOGICAL="$2"
  local PHYSICAL="$3"
  local STAMP='[0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\}T[0-9]\{2\}:[0-9]\{2\}:[0-9]\{2\}[.0-9]*'
  if [ "${#PHYSICAL}" -gt "${#LOGICAL}" ]; then
    sed -e "s|$PHYSICAL|@HOME@|g" -e "s|$LOGICAL|@HOME@|g" "$FILE"
  else
    sed -e "s|$LOGICAL|@HOME@|g" -e "s|$PHYSICAL|@HOME@|g" "$FILE"
  fi |
    sed -e "s|time=${STAMP}[+-][0-9]\{2\}:[0-9]\{2\}|time=@TIMESTAMP@|g" \
      -e "s|time=${STAMP}Z|time=@TIMESTAMP@|g"
}

# RUN_ONE runs one matrix entry with one binary against a HOME nobody else has
# touched, and leaves four artifacts behind: normalized stdout, normalized
# stderr, the exit code, and the tree the run left in that HOME. A fresh HOME
# per binary per command is what makes the matrix order-independent — no
# command's side effects can reach a later command's comparison.
RUN_ONE() {
  local BINARY="$1"
  local TAG="$2"
  local COMMAND="$3"
  local ARGS
  read -r -a ARGS <<< "$COMMAND"
  local RUN_HOME RUN_PHYSICAL CODE
  RUN_HOME=$(mktemp -d "$WORK_DIR/home-$TAG-XXXX")
  RUN_PHYSICAL=$(cd "$RUN_HOME" && pwd -P)
  CODE=0
  "$BINARY" "${ARGS[@]}" --home "$RUN_HOME" \
    >"$WORK_DIR/$TAG.stdout.raw" 2>"$WORK_DIR/$TAG.stderr.raw" || CODE=$?
  printf '%s\n' "$CODE" >"$WORK_DIR/$TAG.exit_code"
  NORMALIZE_OUTPUT "$WORK_DIR/$TAG.stdout.raw" "$RUN_HOME" "$RUN_PHYSICAL" >"$WORK_DIR/$TAG.stdout"
  NORMALIZE_OUTPUT "$WORK_DIR/$TAG.stderr.raw" "$RUN_HOME" "$RUN_PHYSICAL" >"$WORK_DIR/$TAG.stderr"
  SNAPSHOT_TREE "$RUN_HOME" >"$WORK_DIR/$TAG.tree"
  rm -rf "$RUN_HOME"
}

# COMPARE_FIELD is the verdict: identical passes, a registered difference passes
# loudly with its reason so the CI log shows what was forgiven and why, and an
# unregistered difference fails with a bounded excerpt.
COMPARE_FIELD() {
  local COMMAND="$1"
  local FIELD="$2"
  local BASE_FILE="$3"
  local CUR_FILE="$4"
  local INDEX
  if diff -q "$BASE_FILE" "$CUR_FILE" >/dev/null 2>&1; then
    pass "dot $COMMAND — $FIELD identical"
    return 0
  fi
  if INDEX=$(REGISTRY_MATCH "$COMMAND" "$FIELD"); then
    REG_SEEN[INDEX]=1
    pass "dot $COMMAND — $FIELD differs, registered in expected-diff.tsv: ${REG_REASONS[$INDEX]}"
    return 0
  fi
  fail "dot $COMMAND — $FIELD differs between dot-baseline (pinned $BASELINE_COMMIT) and dot, and no row in tests/expected-diff.tsv registers it"
  echo "    --- $FIELD diff, first $EXCERPT_LINES lines (< baseline, > current) ---"
  { diff "$BASE_FILE" "$CUR_FILE" || true; } | head -n "$EXCERPT_LINES" | sed 's/^/    /' || true
  return 0
}

echo "=== Scenario: differential ==="

echo ""
echo "--- preflight ---"
PREFLIGHT_FAILED=0
for BINARY in dot dot-baseline; do
  if command -v "$BINARY" >/dev/null 2>&1; then
    pass "$BINARY is on PATH"
  else
    PREFLIGHT_FAILED=1
    fail "$BINARY is not on PATH — the differential compares two binaries. The linux job builds dot-baseline from $BASELINE_COMMIT on the runner and tests/Dockerfile.ubuntu-22.04 COPYs both into the image; one of those two steps is missing."
  fi
done

if [ -f "$REGISTRY" ]; then
  LOAD_REGISTRY
  pass "expected-diff.tsv loaded with ${#REG_COMMANDS[@]} registered change(s)"
else
  PREFLIGHT_FAILED=1
  fail "expected-diff.tsv not found at $REGISTRY — without the registry every difference would fail, including the ones somebody deliberately registered"
fi

if [ "$PREFLIGHT_FAILED" -ne 0 ]; then
  echo ""
  echo "Preflight failed — the comparison was not run."
  report || true
  exit 1
fi

WORK_DIR=$(mktemp -d /tmp/dotfiles-differential-XXXX)

for COMMAND in "${MATRIX[@]}"; do
  echo ""
  echo "--- dot $COMMAND ---"
  RUN_ONE dot-baseline baseline "$COMMAND"
  RUN_ONE dot current "$COMMAND"
  COMPARE_FIELD "$COMMAND" stdout "$WORK_DIR/baseline.stdout" "$WORK_DIR/current.stdout"
  COMPARE_FIELD "$COMMAND" stderr "$WORK_DIR/baseline.stderr" "$WORK_DIR/current.stderr"
  COMPARE_FIELD "$COMMAND" exit_code "$WORK_DIR/baseline.exit_code" "$WORK_DIR/current.exit_code"
  COMPARE_FIELD "$COMMAND" tree "$WORK_DIR/baseline.tree" "$WORK_DIR/current.tree"
done

echo ""
echo "--- dry-run applies still succeed on the current binary ---"
# The matrix above only proves the two binaries agree with each other. Two
# equally broken binaries agree perfectly, so assert the absolute contract too.
for PROFILE in minimal full server; do
  ASSERT_HOME=$(mktemp -d "$WORK_DIR/assert-XXXX")
  assert_exit_code 0 dot apply --profile "$PROFILE" --yes --dry-run --home "$ASSERT_HOME"
  rm -rf "$ASSERT_HOME"
done

echo ""
echo "--- expected-diff.tsv is not stale ---"
if [ "${#REG_COMMANDS[@]}" -eq 0 ]; then
  pass "expected-diff.tsv holds no data rows: the milestone is at full behavior preservation"
else
  I=0
  while [ "$I" -lt "${#REG_COMMANDS[@]}" ]; do
    if [ "${REG_SEEN[$I]}" -eq 1 ]; then
      pass "registered change still occurs: ${REG_COMMANDS[$I]} / ${REG_FIELDS[$I]}"
    else
      fail "expected-diff.tsv row matched no difference in this run, so the change it records appears reverted or superseded — remove the row: ${REG_COMMANDS[$I]} / ${REG_FIELDS[$I]} (${REG_REASONS[$I]})"
    fi
    I=$((I + 1))
  done
fi

echo ""
echo "--- budget ---"
ELAPSED=$((SECONDS - START_SECONDS))
echo "  comparison elapsed: ${ELAPSED}s (the baseline BUILD is outside this budget and is cached in CI)"
if [ "$ELAPSED" -le "$BUDGET_SECONDS" ]; then
  pass "comparison finished in ${ELAPSED}s, within the ${BUDGET_SECONDS}s budget"
else
  fail "comparison took ${ELAPSED}s, over the ${BUDGET_SECONDS}s budget"
fi

# Cleanup
rm -rf "$WORK_DIR"

report
