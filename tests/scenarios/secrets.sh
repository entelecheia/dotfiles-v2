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

echo "=== Scenario: secrets ==="

WORK_DIR=$(mktemp -d /tmp/dotfiles-secrets-XXXX)
# Cleanup on the failure path too: every home below holds a generated age
# identity and a plaintext sentinel, and the container outlives this script.
trap 'rm -rf "$WORK_DIR"' EXIT

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

report
