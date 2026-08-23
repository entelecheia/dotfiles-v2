#!/usr/bin/env bash
# Scenario: dot secrets end to end against a real age binary (TEST-04, D-09)
#
# The claim under test is that `dot secrets init` produces ciphertext. Every
# check made of that claim so far ran against a stub `age` that copied its
# input to its output (internal/secrets/secrets_test.go stubAge), which
# satisfies a round trip perfectly and proves nothing about encryption. D-09
# put the real binary in the image for the two assertions below — the age file
# header is present, and the plaintext sentinel is absent — because those are
# the pair a copy-through stub cannot pass.
set -euo pipefail
# shellcheck source=tests/assert.sh disable=SC1091
source "$(dirname "$0")/../assert.sh"

# Pinned for the reason dry-run-empty-home.sh pins it: this scenario creates
# the files it later compares, and a host under a different umask records
# different modes for them.
umask 022

# assert.sh carries no generic pass/fail. Both append to ERRORS so the
# terminal report keeps the detail rather than only the count.
pass() {
  PASS=$((PASS + 1))
  echo "  ✓ $1"
}
fail() {
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: $1")
  echo "  ✗ $1"
}

# ABORT records a fixture failure the rest of the run cannot proceed past.
# A half-built fixture turns every later assertion into noise, so the report
# is printed at the point of failure rather than after a cascade.
ABORT() {
  fail "$1"
  report || true
  exit 1
}

# Unmistakably synthetic (T-06-14). It is the string the archive assertions
# grep for, so it must be greppable, and it must never read as a credential
# anyone could mistake for real.
SENTINEL="DOTFILES-TEST-SENTINEL-NOT-A-CREDENTIAL-0000000000"

# SECRETS_FIXTURE prepares one home: dot's own state file, a generated age
# identity, the secrets block naming it, and a plaintext carrying the
# sentinel.
#
# The secrets block is appended by hand because no CLI writes
# secrets.age_recipients — --scaffold creates plaintext templates and does not
# touch recipients. That couples this fixture to the state schema, so the
# append is guarded: yaml.v3 would not merge a second top-level `secrets:`
# key, it would leave a malformed file, and DEBT-01 is going to change what
# `dot init` writes. The guard makes that surface here as a clear failure.
SECRETS_FIXTURE() {
  local HOME_DIR="$1"
  local STATE="$HOME_DIR/.config/dotfiles/config.yaml"
  local RECIPIENT

  mkdir -p "$HOME_DIR"
  if ! dot init --home "$HOME_DIR" --yes >/dev/null 2>&1; then
    ABORT "dot init --home $HOME_DIR failed, so no state file exists to configure"
  fi
  if [ ! -f "$STATE" ]; then
    ABORT "dot init --home $HOME_DIR wrote no state file at $STATE"
  fi
  if grep -qE '^secrets:' "$STATE"; then
    ABORT "the generated state at $STATE already carries a top-level 'secrets:' key; appending the fixture block would duplicate a yaml key rather than merge into it"
  fi
  pass "generated state at $STATE carries no top-level 'secrets:' key, so the fixture append cannot duplicate one"

  age-keygen -o "$HOME_DIR/.age-identity.txt" >/dev/null 2>&1
  chmod 600 "$HOME_DIR/.age-identity.txt"
  # Read the public key back out of the identity rather than scraping
  # age-keygen's console output, which is a human-readable banner.
  RECIPIENT=$(age-keygen -y "$HOME_DIR/.age-identity.txt")

  # age_identity is ~/-relative on purpose: ResolveAgeIdentity expands a
  # leading ~/ against the session home, so one fixture works under --home
  # with no absolute path baked into the state file.
  cat >> "$STATE" <<YAML
secrets:
  age_identity: "~/.age-identity.txt"
  age_recipients:
    - $RECIPIENT
YAML

  mkdir -p "$HOME_DIR/.config/shell"
  printf 'export DOTFILES_TEST_SECRET=%s\n' "$SENTINEL" > "$HOME_DIR/.config/shell/90-secrets.sh"
  chmod 600 "$HOME_DIR/.config/shell/90-secrets.sh"
}

# SNAPSHOT_HOME records the WHOLE tree, not just regular files: relative path,
# entry type, and mode for directories, symlinks and files alike, plus a content
# hash for regular files and the target for symlinks. Copied in shape from
# dry-run-empty-home.sh rather than reaching for tests/assert.sh, whose two tree
# helpers walk with `find -type f` and are therefore mode-blind and
# symlink-blind — neither can support the byte-identity claim group 4 makes.
# GNU findutils and coreutils are installed in the image.
SNAPSHOT_HOME() {
  local DIR="$1"
  find "$DIR" -mindepth 1 -printf '%P\t%y\t%m\n' |
    while IFS=$'\t' read -r ENTRY_PATH ENTRY_TYPE ENTRY_MODE; do
      if [ "$ENTRY_TYPE" = "f" ]; then
        printf '%s\t%s\t%s\t%s\n' "$ENTRY_PATH" "$ENTRY_TYPE" "$ENTRY_MODE" \
          "$(sha256sum "$DIR/$ENTRY_PATH" | cut -d' ' -f1)"
      elif [ "$ENTRY_TYPE" = "l" ]; then
        printf '%s\t%s\t%s\t-> %s\n' "$ENTRY_PATH" "$ENTRY_TYPE" "$ENTRY_MODE" \
          "$(readlink "$DIR/$ENTRY_PATH")"
      else
        printf '%s\t%s\t%s\n' "$ENTRY_PATH" "$ENTRY_TYPE" "$ENTRY_MODE"
      fi
    done | LC_ALL=C sort
}

echo "=== Scenario: secrets ==="

WORK_DIR=$(mktemp -d /tmp/dotfiles-secrets-XXXX)
# Cleanup on the failure path too: every home below holds a generated age
# identity and a plaintext sentinel, and the container outlives this script.
trap 'rm -rf "$WORK_DIR"' EXIT

# Recorded before anything runs, so group 3's absence claims are about THIS
# run rather than about whatever the machine already carried. The process home
# is deliberately not stripped: BUG-08 got its answer wrong precisely when
# $HOME was a real, different, writable directory, which is what it is here.
PROC_STORE="$HOME/.local/share/dotfiles-secrets"
PROC_PLAIN="$HOME/.config/shell/90-secrets.sh"
PROC_STATE="$HOME/.config/dotfiles/config.yaml"
PROC_STORE_BEFORE=absent
PROC_PLAIN_BEFORE=absent
PROC_STATE_BEFORE=absent
if [ -e "$PROC_STORE" ]; then PROC_STORE_BEFORE=present; fi
if [ -e "$PROC_PLAIN" ]; then PROC_PLAIN_BEFORE=present; fi
if [ -e "$PROC_STATE" ]; then PROC_STATE_BEFORE=present; fi

echo ""
echo "--- group 1: init, backup and restore round trip against a real age ---"

assert_command_exists age "a real age is on PATH"
assert_command_exists age-keygen "a real age-keygen is on PATH"
# An assertion rather than a bare call: a bare `age --version` under `set -e`
# would abort the run before `report` ever printed, and the exit status would
# come from age instead of from the scenario.
assert_exit_code 0 age --version
echo "  age --version: $(age --version 2>&1 | head -1 || true)"

SANDBOX="$WORK_DIR/home-roundtrip"
SECRETS_FIXTURE "$SANDBOX"

STORE="$SANDBOX/.local/share/dotfiles-secrets"
ARCHIVE="$STORE/90-secrets.sh.age"
PLAIN="$SANDBOX/.config/shell/90-secrets.sh"
ORIGINAL="$WORK_DIR/original-90-secrets.sh"
cp "$PLAIN" "$ORIGINAL"

INIT_LOG="$WORK_DIR/init.log"
INIT_RC=0
dot secrets init --home "$SANDBOX" --yes > "$INIT_LOG" 2>&1 || INIT_RC=$?
cat "$INIT_LOG"
if [ "$INIT_RC" -eq 0 ]; then
  pass "dot secrets init --home exited 0"
else
  fail "dot secrets init --home exited $INIT_RC"
fi

assert_file_exists "$ARCHIVE" "the archive landed in the target home's store"
assert_file_contains "$INIT_LOG" "SSH key not found, skipping" \
  "the SSH key entry, whose plaintext does not exist, was reported as skipped rather than failing the run"

# The two encryption assertions. Each carries its own missing-archive branch:
# a bare `grep -q ... || pass` would report a missing archive as a successful
# encryption, which is the one wrong answer this pair exists to rule out.
if [ ! -f "$ARCHIVE" ]; then
  fail "the age header check could not run: no archive at $ARCHIVE"
elif [ "$(head -c 21 "$ARCHIVE")" = "age-encryption.org/v1" ]; then
  pass "the archive begins with the age file header"
else
  fail "the archive does not begin with the age file header (first 21 bytes, unprintables dotted: $(head -c 21 "$ARCHIVE" | tr -c '[:print:]' '.'))"
fi

if [ ! -f "$ARCHIVE" ]; then
  fail "the sentinel-absence check could not run: no archive at $ARCHIVE"
elif grep -qa "$SENTINEL" "$ARCHIVE"; then
  fail "the plaintext sentinel appears inside $ARCHIVE, so the archive is not encrypted"
else
  pass "the plaintext sentinel appears nowhere in the archive"
fi

# The destination is always explicit. The argument is optional and its
# default is the cloud backup root, so an omitted one is this scenario
# writing outside its sandbox.
BACKUP_DIR="$WORK_DIR/backup"
mkdir -p "$BACKUP_DIR"
assert_exit_code 0 dot secrets backup "$BACKUP_DIR" --home "$SANDBOX" --yes
assert_file_exists "$BACKUP_DIR/90-secrets.sh.age" "the archive reached the explicit destination"

rm -f "$PLAIN"
assert_file_not_exists "$PLAIN" "the plaintext is gone before the restore"
assert_exit_code 0 dot secrets restore "$BACKUP_DIR" --home "$SANDBOX" --yes
# cmp, not a grep for the sentinel: a truncated file that still holds the
# sentinel would pass a grep and fail the claim.
if cmp -s "$ORIGINAL" "$PLAIN"; then
  pass "the restored plaintext is byte-identical to the original"
else
  fail "the restored plaintext differs from the original: $(cmp "$ORIGINAL" "$PLAIN" 2>&1 || true)"
fi

echo ""
echo "--- group 2: init with no configured recipients is refused (D-10) ---"

NORECIP="$WORK_DIR/home-norecipients"
mkdir -p "$NORECIP"
# Deliberately NOT run through SECRETS_FIXTURE: the state this group needs is
# the one dot init writes on its own, with no secrets block at all.
if ! dot init --home "$NORECIP" --yes >/dev/null 2>&1; then
  ABORT "dot init --home $NORECIP failed, so the no-recipients state does not exist"
fi

NORECIP_STORE="$NORECIP/.local/share/dotfiles-secrets"
NORECIP_LOG="$WORK_DIR/norecipients.log"
NORECIP_RC=0
dot secrets init --home "$NORECIP" --yes > "$NORECIP_LOG" 2>&1 || NORECIP_RC=$?
cat "$NORECIP_LOG"
# All three of exit code, wording and absence are asserted. An exit code alone
# does not tell a refusal from a crash, and a refusal that had already created
# the store would still be a defect.
if [ "$NORECIP_RC" -ne 0 ]; then
  pass "dot secrets init --home refused a state with no recipients (exit $NORECIP_RC)"
else
  fail "dot secrets init --home exited 0 on a state with no configured recipients"
fi
assert_file_contains "$NORECIP_LOG" "secrets.age_recipients" \
  "the refusal names the setting an operator has to fix"
if [ -e "$NORECIP_STORE" ]; then
  fail "the refused run left a secrets store behind at $NORECIP_STORE"
else
  pass "the refused run created no secrets store"
fi

echo ""
echo "--- group 3: --home is honoured and the invoking user's home is untouched (BUG-08) ---"

if [ "$PROC_STORE_BEFORE" = absent ]; then
  pass "the invoking user's home carried no secrets store before group 1, so the absence claims below are about this run"
else
  fail "the invoking user's home already carried $PROC_STORE before group 1; a leak cannot be told apart from pre-existing state"
fi

# Absence in the process home, not only presence in the target home: a run that
# wrote to BOTH homes would sail through a presence check alone.
if [ -e "$PROC_STORE" ]; then
  fail "the --home round trip leaked a secrets store into the invoking user's home at $PROC_STORE"
else
  pass "the invoking user's home carries no secrets store after a full --home round trip"
fi
if [ "$PROC_PLAIN_BEFORE" = present ]; then
  fail "the invoking user's home already carried $PROC_PLAIN before group 1; the leak claim below cannot be made"
elif [ -e "$PROC_PLAIN" ]; then
  fail "the --home restore wrote decrypted plaintext into the invoking user's home at $PROC_PLAIN"
else
  pass "the --home restore wrote no plaintext into the invoking user's home"
fi
# The backup subcommand records its last-backup entry through the session, so
# the state file is a second, independent seam the same defect would show at.
if [ "$PROC_STATE_BEFORE" = present ]; then
  fail "the invoking user's home already carried $PROC_STATE before group 1; the leak claim below cannot be made"
elif [ -e "$PROC_STATE" ]; then
  fail "a --home run wrote state into the invoking user's home at $PROC_STATE"
else
  pass "no --home run wrote state into the invoking user's home"
fi

assert_file_exists "$ARCHIVE" "the archive is in the TARGET home's store"
assert_file_exists "$PLAIN" "the restored plaintext is in the TARGET home"
assert_file_contains "$SANDBOX/.config/dotfiles/config.yaml" "last_backup" \
  "the last-backup record landed in the TARGET home's state file"

echo ""
echo "--- group 4: init --dry-run leaves a configured home byte-identical (BUG-13) ---"

# A configured home rather than an empty one: secrets init reaches its
# interesting paths only when recipients and a plaintext both exist.
DRYHOME="$WORK_DIR/home-dryrun"
SECRETS_FIXTURE "$DRYHOME"
DRY_BEFORE="$WORK_DIR/dryrun-before"
DRY_AFTER="$WORK_DIR/dryrun-after"
SNAPSHOT_HOME "$DRYHOME" > "$DRY_BEFORE"
assert_exit_code 0 dot secrets init --home "$DRYHOME" --yes --dry-run
SNAPSHOT_HOME "$DRYHOME" > "$DRY_AFTER"

DRY_DIFFS=0
while IFS= read -r LINE; do
  [ -n "$LINE" ] || continue
  DRY_DIFFS=$((DRY_DIFFS + 1))
  fail "dot secrets init --dry-run changed a configured home (differing snapshot line: $LINE)"
done < <(LC_ALL=C diff "$DRY_BEFORE" "$DRY_AFTER" | grep -E '^[<>]' || true)
if [ "$DRY_DIFFS" -eq 0 ]; then
  pass "dot secrets init --dry-run left the configured home byte-identical, modes and symlink targets alike"
fi

echo ""
echo "--- group 5: the read surfaces report the archive that exists ---"

STATUS_LOG="$WORK_DIR/status.log"
STATUS_RC=0
dot secrets status --home "$SANDBOX" > "$STATUS_LOG" 2>&1 || STATUS_RC=$?
if [ "$STATUS_RC" -eq 0 ]; then
  pass "dot secrets status --home exited 0"
else
  fail "dot secrets status --home exited $STATUS_RC"
fi
assert_file_contains "$STATUS_LOG" "90-secrets.sh.age" "status names the archive that exists"

LIST_LOG="$WORK_DIR/list.log"
LIST_RC=0
dot secrets list --home "$SANDBOX" > "$LIST_LOG" 2>&1 || LIST_RC=$?
if [ "$LIST_RC" -eq 0 ]; then
  pass "dot secrets list --home exited 0"
else
  fail "dot secrets list --home exited $LIST_RC"
fi
assert_file_contains "$LIST_LOG" "90-secrets.sh.age" "list names the archive that exists"

report
