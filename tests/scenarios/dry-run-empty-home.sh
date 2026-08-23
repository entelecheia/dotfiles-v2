#!/usr/bin/env bash
# Scenario: dot apply --dry-run leaves an empty HOME byte-identical (GUARD-03, container half)
set -euo pipefail
# shellcheck source=tests/assert.sh disable=SC1091
source "$(dirname "$0")/../assert.sh"

# Pin the umask so the recorded mode field matches what the Go half
# (internal/cli/dryrun_property_test.go) and CI record. Without this a host
# running under umask 077 records different directory modes, the full-line
# entries below stop matching, and the pressure is to widen the table — the
# exact failure this guard exists to prevent.
umask 022

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
# Known-deviation table — mirrors knownDryRunDeviations in
# internal/cli/dryrun_property_test.go. Entries are FULL snapshot lines
# (path TAB type TAB mode, plus the content hash for a regular file), never bare
# paths: a path-only entry would forgive a mode change or a content change on a
# tolerated path, which is exactly what the byte-identity claim must not do.
#
# It is EMPTY, and empty is the intended resting state. Phase 5 fixed BUG-05
# (internal/cli/apply.go, the state save above the homeOverride fork) and pruned
# its five entries from both this table and the Go one in the same change,
# because the staleness check below fails on an entry whose deviation has stopped
# occurring just as loudly as an unattributed write fails the scan above.
#
# This is a record, not a suppression. Every entry names an existing requirement
# ID, its file:line, and a one-clause description, so it is resolvable from the
# tracked tree alone. Do not add an entry for a deviation that is not
# attributable to a recorded BUG — mint the requirement first.
#
# bash 3.2 compatible: two parallel indexed arrays, no associative array.
# ---------------------------------------------------------------------------
DEVIATION_KEYS=()
DEVIATION_OWNERS=()
DEVIATION_SEEN=()

ADD_DEVIATION() {
  DEVIATION_KEYS+=("$1")
  DEVIATION_OWNERS+=("$2")
  DEVIATION_SEEN+=(0)
}

# BUG-06 internal/exec/brew.go:293 - RefreshPath re-opens the PATH sandbox from
# an os.Stat, so read-only probes run real third-party binaries that write into
# HOME and reach the network. It has no entry here on purpose: ubuntu:22.04
# ships no Homebrew prefix, so the escape never fires and this scenario carries
# the assertion unconditionally. That is why the Go half may skip on a developer
# machine and CI still sees a strict check.

# DEVIATION_MATCH prints the index of the entry matching a full snapshot line,
# or returns 1 when the line is not attributable to any recorded requirement.
DEVIATION_MATCH() {
  local LINE="$1"
  local I=0
  while [ "$I" -lt "${#DEVIATION_KEYS[@]}" ]; do
    if [ "${DEVIATION_KEYS[$I]}" = "$LINE" ]; then
      printf '%s' "$I"
      return 0
    fi
    I=$((I + 1))
  done
  return 1
}

# SNAPSHOT_HOME records the WHOLE tree, not just regular files: relative path,
# entry type, and mode for directories, symlinks, and files alike, plus a content
# hash for regular files. The two tree-comparison helpers in tests/assert.sh are
# deliberately not used — they walk with `find -type f`, so they miss directories
# and symlinks, and hash content but not modes. `dot` writes
# symlinks as its primary artifact, so a symlink-blind snapshot cannot support a
# byte-identity claim. GNU findutils and coreutils are installed in the image.
SNAPSHOT_HOME() {
  local DIR="$1"
  find "$DIR" -mindepth 1 -printf '%P\t%y\t%m\n' |
    while IFS=$'\t' read -r ENTRY_PATH ENTRY_TYPE ENTRY_MODE; do
      if [ "$ENTRY_TYPE" = "f" ]; then
        printf '%s\t%s\t%s\t%s\n' "$ENTRY_PATH" "$ENTRY_TYPE" "$ENTRY_MODE" \
          "$(sha256sum "$DIR/$ENTRY_PATH" | cut -d' ' -f1)"
      elif [ "$ENTRY_TYPE" = "l" ]; then
        # A symlink's payload is its target. Without it, a dry-run that created
        # a symlink pointing somewhere new would compare as identical.
        printf '%s\t%s\t%s\t-> %s\n' "$ENTRY_PATH" "$ENTRY_TYPE" "$ENTRY_MODE" \
          "$(readlink "$DIR/$ENTRY_PATH")"
      else
        printf '%s\t%s\t%s\n' "$ENTRY_PATH" "$ENTRY_TYPE" "$ENTRY_MODE"
      fi
    done | LC_ALL=C sort
}

echo "=== Scenario: dry-run-empty-home ==="

WORK_DIR=$(mktemp -d /tmp/dotfiles-dryrun-empty-XXXX)

for PROFILE in minimal full server; do
  echo ""
  echo "--- dry-run into an empty HOME: $PROFILE ---"

  CUSTOM_HOME=$(mktemp -d "$WORK_DIR/home-XXXX")
  BEFORE_FILE="$WORK_DIR/before-$PROFILE"
  AFTER_FILE="$WORK_DIR/after-$PROFILE"

  SNAPSHOT_HOME "$CUSTOM_HOME" > "$BEFORE_FILE"
  assert_exit_code 0 dot apply --profile "$PROFILE" --yes --dry-run --home "$CUSTOM_HOME"
  SNAPSHOT_HOME "$CUSTOM_HOME" > "$AFTER_FILE"

  DEVIATION_COUNT=0
  UNATTRIBUTED=0
  while IFS= read -r LINE; do
    [ -n "$LINE" ] || continue
    DEVIATION_COUNT=$((DEVIATION_COUNT + 1))
    if MATCH_INDEX=$(DEVIATION_MATCH "$LINE"); then
      DEVIATION_SEEN[MATCH_INDEX]=1
    else
      UNATTRIBUTED=$((UNATTRIBUTED + 1))
      fail "dry-run ($PROFILE) wrote $(printf '%s' "$LINE" | cut -f1) into an empty HOME and it is not attributable to a recorded requirement (full snapshot line: $LINE)"
    fi
  done < <(LC_ALL=C comm -13 "$BEFORE_FILE" "$AFTER_FILE")

  if [ "$UNATTRIBUTED" -eq 0 ] && [ "$DEVIATION_COUNT" -eq 0 ]; then
    pass "dry-run ($PROFILE) left an empty HOME byte-identical"
  elif [ "$UNATTRIBUTED" -eq 0 ]; then
    pass "dry-run ($PROFILE) left an empty HOME byte-identical except for $DEVIATION_COUNT recorded deviation(s)"
  fi

  rm -rf "$CUSTOM_HOME"
done

echo ""
echo "--- known-deviation table is not stale ---"
if [ "${#DEVIATION_KEYS[@]}" -eq 0 ]; then
  pass "the known-deviation table is empty: the dry-run byte-identity claim is unconditional"
fi
I=0
while [ "$I" -lt "${#DEVIATION_KEYS[@]}" ]; do
  if [ "${DEVIATION_SEEN[$I]}" -eq 1 ]; then
    pass "recorded deviation still occurs: $(printf '%s' "${DEVIATION_KEYS[$I]}" | cut -f1)"
  else
    fail "known-deviation entry never occurred, so the defect it records appears fixed — remove the entry: ${DEVIATION_KEYS[$I]} (recorded as: ${DEVIATION_OWNERS[$I]})"
  fi
  I=$((I + 1))
done

# Cleanup
rm -rf "$WORK_DIR"

report
