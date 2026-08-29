#!/usr/bin/env bash
# Scenario: a real ssh + rsync loopback round trip inside the image (CI-01, D-05)
#
# The claim under test is that this image can complete an actual transfer
# between two accounts. Every check made of that claim so far would have been
# `command -v rsync ssh sshd` plus `id peeruser` — a presence assertion, which
# is the same vacuous-green shape this milestone exists to remove. A presence
# check passes on an image whose PAM stack refuses every login: jammy makes
# `pam_loginuid.so` `required` in /etc/pam.d/sshd and it fails when
# /proc/self/loginuid is not writable, the common container case. Per D-05 that
# failure is met here, where it reads as what it is, rather than inside Phase
# 11's peer fixture, where it would be misread as a fixture bug.
set -euo pipefail
# shellcheck source=tests/assert.sh disable=SC1091
source "$(dirname "$0")/../assert.sh"

# assert.sh carries no generic pass/fail. Both append to ERRORS so the terminal
# report keeps the detail rather than only the count.
pass() {
  PASS=$((PASS + 1))
  echo "  ✓ $1"
}
fail() {
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: $1")
  echo "  ✗ $1"
}

# ABORT records a fixture failure the rest of the run cannot proceed past. A
# half-built fixture turns every later assertion into noise, so the report is
# printed at the point of failure rather than after a cascade that all names
# the wrong cause.
ABORT() {
  fail "$1"
  report || true
  exit 1
}

echo "=== Scenario: ssh-loopback ==="

# Not a reachability control: the daemon binds loopback (below) and the
# workflow runs this container with no published ports, so nothing outside can
# reach port 2222 either. It is non-default so the daemon cannot collide with
# anything the base image or a future layer puts on 22.
SSH_PORT=2222
SSHD_BIN=/usr/sbin/sshd
PIDFILE=/run/sshd/ssh-loopback.pid

# One directory for all runtime state. Key material is generated into it at
# runtime and removed by the trap; nothing of the sort is baked into the image
# or committed to the repository.
tmpdir=$(mktemp -d)

# Single-quoted so the expansions happen at trap time, not at trap-set time
# (SC2064). The container is `docker run --rm`, so the daemon cannot outlive
# it in any case; the kill is hygiene, and its failure is not a test failure.
cleanup() {
  if [ -f "$PIDFILE" ]; then
    sudo kill "$(sudo cat "$PIDFILE")" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmpdir"
}
trap 'cleanup' EXIT

# ── fixture gates ────────────────────────────────────────────────────────────
# Checked before anything is started. Any of these missing is a fixture
# failure, not a skip: this scenario runs only in the linux container, where
# the image guarantees every binary it needs, so a miss means the image
# regressed and the job must go red.
assert_command_exists rsync
assert_command_exists ssh
command -v rsync >/dev/null 2>&1 || ABORT "fixture: rsync is not in the image"
command -v ssh >/dev/null 2>&1 || ABORT "fixture: ssh is not in the image"

# sshd is not on an unprivileged user's PATH, so it is tested by absolute path
# rather than through assert_command_exists.
if [ -x "$SSHD_BIN" ]; then
  pass "$SSHD_BIN is executable"
else
  ABORT "fixture: $SSHD_BIN is not executable (openssh-server missing from the image)"
fi

# D-07: the second account is the only way to get a second $HOME, because sshd
# sets HOME from passwd.
if getent passwd peeruser >/dev/null; then
  pass "peeruser resolves in passwd"
else
  ABORT "fixture: peeruser does not resolve in passwd"
fi

# ── daemon ───────────────────────────────────────────────────────────────────
sudo mkdir -p /run/sshd
sudo ssh-keygen -A >/dev/null

# Port is passed before ListenAddress on purpose: sshd applies a bare
# ListenAddress against the Port directives seen so far, so the reverse order
# would leave a listener on 22.
#
# UsePAM=no          jammy's /etc/pam.d/sshd has pam_loginuid.so as `required`
#                    and it fails when /proc/self/loginuid is not writable.
#                    Accepted only because this is a throwaway daemon in an
#                    ephemeral container (T-09-19) — do not copy it into a
#                    production sshd_config.
# PasswordAuthentication=no  public key only; a password path would let a
#                    misconfigured account in without the generated key.
# PermitRootLogin=no the session under test is an unprivileged peer.
# ListenAddress=127.0.0.1  the socket must not cross the container boundary.
# PidFile            gives the EXIT trap something to kill.
#
# No foreground flag: sshd daemonizes by default and the scenario needs it in
# the background.
sudo "$SSHD_BIN" \
  -o "Port=$SSH_PORT" \
  -o UsePAM=no \
  -o PasswordAuthentication=no \
  -o PermitRootLogin=no \
  -o ListenAddress=127.0.0.1 \
  -o "PidFile=$PIDFILE"

# Bounded readiness poll, roughly ten seconds. An unbounded wait would hang to
# the job's 45-minute ceiling and report nothing useful (T-09-20).
sshd_ready=""
attempt=0
while [ "$attempt" -lt 20 ]; do
  if (: </dev/tcp/127.0.0.1/"$SSH_PORT") >/dev/null 2>&1; then
    sshd_ready=yes
    break
  fi
  attempt=$((attempt + 1))
  sleep 0.5
done
[ -n "$sshd_ready" ] || ABORT "fixture: sshd never accepted a connection on port $SSH_PORT"

# ── key material ─────────────────────────────────────────────────────────────
# Generated per run into the temp directory. StrictModes makes the modes below
# load-bearing: sshd refuses a loose-permissioned ~/.ssh and the login would
# fail with nothing in the client's output explaining why.
ssh-keygen -t ed25519 -N '' -C ssh-loopback-scenario -f "$tmpdir/id_ed25519" >/dev/null
sudo install -d -m 700 -o peeruser -g peeruser /home/peeruser/.ssh
sudo install -m 600 -o peeruser -g peeruser "$tmpdir/id_ed25519.pub" /home/peeruser/.ssh/authorized_keys

# One option string for every client invocation. BatchMode=yes so a prompt
# fails instead of hanging. Host key checking is off against a known-hosts file
# inside the temp directory: a per-run daemon has a per-run host key, so there
# is no trust to preserve between runs.
SSH_OPTS=(
  -i "$tmpdir/id_ed25519"
  -p "$SSH_PORT"
  -o BatchMode=yes
  -o StrictHostKeyChecking=no
  -o "UserKnownHostsFile=$tmpdir/known_hosts"
  -o LogLevel=ERROR
)

# ── assertions ───────────────────────────────────────────────────────────────
remote_user=$(ssh "${SSH_OPTS[@]}" peeruser@127.0.0.1 'id -un' 2>/dev/null || true)
if [ "$remote_user" = "peeruser" ]; then
  pass "ssh login succeeded and the remote identity is peeruser"
else
  fail "ssh login: expected remote id -un to be peeruser, got '$remote_user'"
fi

# Both facts, not just presence. A single-account container would satisfy a
# presence check on $HOME and defeat the whole point of the second account —
# two distinct homes are what the peer inventory actually compares (D-07).
remote_home=$(ssh "${SSH_OPTS[@]}" peeruser@127.0.0.1 'printf %s "$HOME"' 2>/dev/null || true)
if [ "$remote_home" = "/home/peeruser" ] && [ "$remote_home" != "$HOME" ]; then
  pass "peer \$HOME is /home/peeruser and differs from the invoking user's $HOME"
else
  fail "peer \$HOME: expected /home/peeruser distinct from $HOME, got '$remote_home'"
fi

# Verbatim the probe RemoteRsyncPath sends. A nologin or restricted peer shell
# fails this pipeline before any transfer starts, which is why D-07 specifies a
# bash login shell for peeruser rather than treating the shell as cosmetic.
banner=$(ssh "${SSH_OPTS[@]}" peeruser@127.0.0.1 'rsync --version 2>&1 | head -2' 2>/dev/null || true)
if printf '%s' "$banner" | grep -q 'rsync.*version'; then
  pass "peer login shell executes a pipeline and rsync reports its version"
else
  fail "peer login shell pipeline: no rsync version banner, got '$banner'"
fi

# Unmistakably synthetic, in the register secrets.sh uses: it is greppable and
# can never read as a credential anyone could mistake for real.
SENTINEL="DOTFILES-TEST-SENTINEL-NOT-A-CREDENTIAL-0000000000"
printf '%s\n' "$SENTINEL" > "$tmpdir/payload.txt"
if rsync -e "ssh ${SSH_OPTS[*]}" "$tmpdir/payload.txt" peeruser@127.0.0.1:payload.txt >/dev/null 2>&1; then
  # Bytes, not existence: an empty file at the right path would satisfy a
  # `test -f` and prove nothing about the transfer.
  readback=$(ssh "${SSH_OPTS[@]}" peeruser@127.0.0.1 'cat ~/payload.txt' 2>/dev/null || true)
  if [ "$readback" = "$SENTINEL" ]; then
    pass "rsync transferred the payload and it read back byte-identical"
  else
    fail "rsync payload read back as '$readback', expected the sentinel"
  fi
else
  fail "rsync transfer to peeruser@127.0.0.1 exited non-zero"
fi

# D-07: peeruser has no sudoers entry and no password, so a permission failure
# on the peer path surfaces rather than being masked. Written as an explicit
# if-not-then-pass because under `set -e` a bare failing command would abort
# the script, and the failure is the expected outcome here.
if ssh "${SSH_OPTS[@]}" peeruser@127.0.0.1 'sudo -n true' >/dev/null 2>&1; then
  fail "peeruser escalated to root via sudo — the peer account is not unprivileged"
else
  pass "peeruser cannot escalate: sudo over the ssh session failed"
fi

# D-10: `report` is `[ $FAIL -eq 0 ]` alone (tests/assert.sh:141), so it returns
# success on zero assertions — success having measured nothing. That is the
# same vacuity D-10 closed in sync.sh, and a new file ending on `report` alone
# would inherit it, so the verdict conjoins a positive pass count on its own
# line. D-06: no shared tests/lib/sshd.sh — there is one consumer today, and
# extraction is Phase 11's call if a second appears.
report
[ "$PASS" -gt 0 ]
