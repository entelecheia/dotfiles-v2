#!/usr/bin/env bash
# Scenario: checksum-first installer replacement safety.
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$0")/../assert.sh"

SCRIPT="$(cd "$(dirname "$0")/../.." && pwd)/scripts/install.sh"

echo "=== Scenario: install-security ==="

echo ""
echo "--- checksum-first installer contract ---"
assert_exit_code 0 grep -q 'select_checksum_tool' "$SCRIPT"
assert_exit_code 0 grep -q 'verify_checksum' "$SCRIPT"
assert_exit_code 0 grep -q 'checksums.txt' "$SCRIPT"

SYSTEM_SHA256SUM=$(command -v sha256sum)
SYSTEM_SHASUM=$(command -v shasum)
SYSTEM_MV=$(command -v mv)
FIXTURE_ROOT=$(mktemp -d)
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

make_archive() {
  local dir="$1" body="$2" archive="$3"
  mkdir -p "$dir"
  printf '%s\n' '#!/bin/sh' "$body" > "$dir/dot"
  chmod 0755 "$dir/dot"
  tar czf "$archive" -C "$dir" dot
}

VALID_ARCHIVE="$FIXTURE_ROOT/valid.tar.gz"
INVALID_BINARY_ARCHIVE="$FIXTURE_ROOT/invalid-binary.tar.gz"
INVALID_ARCHIVE="$FIXTURE_ROOT/invalid.tar.gz"
make_archive "$FIXTURE_ROOT/valid" 'echo "dot version 9.9.9"' "$VALID_ARCHIVE"
make_archive "$FIXTURE_ROOT/invalid-binary" 'exit 1' "$INVALID_BINARY_ARCHIVE"
printf 'not a tar archive\n' > "$INVALID_ARCHIVE"

assert_case() {
  local mode="$1" digest_tool="$2" expect="$3"
  local root="$FIXTURE_ROOT/$mode-$digest_tool"
  local fake="$root/fake" install="$root/bin" archive="$VALID_ARCHIVE"
  local old_sum old_compatibility status output
  mkdir -p "$fake" "$install" "$root/home"
  printf '%s\n' '#!/bin/sh' 'echo "dot version 0.0.1"' > "$install/dot"
  chmod 0755 "$install/dot"
  if [[ "$mode" == "regular-promote" ]]; then
    printf 'legacy compatibility command\n' > "$install/dotfiles"
  else
    ln -s dot "$install/dotfiles"
  fi
  old_sum=$($SYSTEM_SHA256SUM "$install/dot" | sed 's/ .*//')
  old_compatibility=$(cat "$install/dotfiles" 2>/dev/null || readlink "$install/dotfiles")

  # GNU tar uses gzip from PATH for -z extraction. Keep that real decompressor
  # available while the fixture deliberately controls curl, digest tools, and mv.
  for tool in bash uname tr head grep sed dirname basename mkdir mktemp rm chmod ln readlink tar gzip cp sleep; do
    ln -s "$(command -v "$tool")" "$fake/$tool"
  done
  rm "$fake/uname"
  cat > "$fake/uname" <<'EOF'
#!/bin/bash
case "${1:-}" in
  -s) echo Linux ;;
  -m) echo x86_64 ;;
  *) /usr/bin/uname "$@" ;;
esac
EOF
  cat > "$fake/brew" <<'EOF'
#!/bin/bash
echo 'Homebrew test'
EOF
  cat > "$fake/curl" <<'EOF'
#!/bin/bash
set -euo pipefail
dest=""
for ((i = 1; i <= $#; i++)); do
  if [[ "${!i}" == "-o" ]]; then
    next=$((i + 1))
    dest="${!next}"
  fi
done
url="${!#}"
if [[ "$url" == *'/releases/latest' ]]; then
  printf '{"tag_name":"v9.9.9"}\n'
  exit 0
fi
if [[ "${INSTALL_FIXTURE_MODE:-}" == "curlfail" && "$url" == *.tar.gz ]]; then
  exit 22
fi
if [[ "$url" == *checksums.txt ]]; then
  read -r sum _ < <("$SYSTEM_SHA256SUM" "$INSTALL_FIXTURE_ARCHIVE")
  case "${INSTALL_FIXTURE_MODE:-}" in
    missing) printf '%064d  other.tar.gz\n' 0 > "$dest" ;;
    malformed) printf 'bad  dot_9.9.9_linux_amd64.tar.gz\n' > "$dest" ;;
    duplicate) printf '%s  dot_9.9.9_linux_amd64.tar.gz\n%s  dot_9.9.9_linux_amd64.tar.gz\n' "$sum" "$sum" > "$dest" ;;
    mismatch) printf '%064d  dot_9.9.9_linux_amd64.tar.gz\n' 0 > "$dest" ;;
    *) printf '%s  dot_9.9.9_linux_amd64.tar.gz\n' "$sum" > "$dest" ;;
  esac
  exit 0
fi
cp "$INSTALL_FIXTURE_ARCHIVE" "$dest"
EOF
  cat > "$fake/mv" <<'EOF'
#!/bin/bash
set -euo pipefail
if [[ "${INSTALL_FIXTURE_MODE:-}" == "promote" || "${INSTALL_FIXTURE_MODE:-}" == "regular-promote" ]] && [[ "${2:-}" == "$INSTALL_DIR/dot" && "${1:-}" == */extract/dot ]]; then
  exit 1
fi
if [[ "${INSTALL_FIXTURE_MODE:-}" == "interrupt" && "${2:-}" == "$INSTALL_DIR/dot" && "${1:-}" == */extract/dot ]]; then
  kill -TERM "$PPID"
  sleep 1
  exit 1
fi
exec "$SYSTEM_MV" "$@"
EOF
  chmod 0755 "$fake/uname" "$fake/brew" "$fake/curl" "$fake/mv"
  case "$digest_tool" in
    sha256sum)
      cat > "$fake/sha256sum" <<'EOF'
#!/bin/bash
exec "$SYSTEM_SHA256SUM" "$@"
EOF
      chmod 0755 "$fake/sha256sum"
      ;;
    shasum)
      cat > "$fake/shasum" <<'EOF'
#!/bin/bash
exec "$SYSTEM_SHASUM" "$@"
EOF
      chmod 0755 "$fake/shasum"
      ;;
  esac
  case "$mode" in
    extract) archive="$INVALID_ARCHIVE" ;;
    invalid-binary) archive="$INVALID_BINARY_ARCHIVE" ;;
  esac

  status=0
  output=$(HOME="$root/home" INSTALL_DIR="$install" INSTALL_FIXTURE_MODE="$mode" \
    INSTALL_FIXTURE_ARCHIVE="$archive" SYSTEM_SHA256SUM="$SYSTEM_SHA256SUM" SYSTEM_SHASUM="$SYSTEM_SHASUM" \
    SYSTEM_MV="$SYSTEM_MV" PATH="$install:$fake" "$SCRIPT" 2>&1) || status=$?
  if [[ "$expect" == "success" ]]; then
    if [[ "$status" -eq 0 && $("$install/dot" --version) == *9.9.9* && $(readlink "$install/dotfiles") == dot ]]; then
      PASS=$((PASS + 1)); echo "  ✓ $mode with $digest_tool installs only a verified archive"
    else
      FAIL=$((FAIL + 1)); ERRORS+=("FAIL: $mode with $digest_tool should install: $output")
    fi
    return
  fi
  if [[ "$status" -ne 0 && $($SYSTEM_SHA256SUM "$install/dot" | sed 's/ .*//') == "$old_sum" && $(cat "$install/dotfiles" 2>/dev/null || readlink "$install/dotfiles") == "$old_compatibility" && -z $(find "$root" -maxdepth 1 -name '.dot-install.*' -o -name '.dot.rollback.*' -o -name '.dotfiles.rollback.*') && "$output" == *'active binary remains unchanged'* ]]; then
    PASS=$((PASS + 1)); echo "  ✓ $mode preserves the active binary and symlink"
  else
    FAIL=$((FAIL + 1)); ERRORS+=("FAIL: $mode must preserve active state: $output")
  fi
}

assert_noop_repairs_compatibility_symlink() {
  local root="$FIXTURE_ROOT/noop" fake="$FIXTURE_ROOT/noop/fake" install="$FIXTURE_ROOT/noop/bin" status output
  mkdir -p "$fake" "$install" "$root/home"
  printf '%s\n' '#!/bin/sh' 'echo "dot version 9.9.9"' > "$install/dot"
  chmod 0755 "$install/dot"
  for tool in bash uname tr head grep sed dirname basename mkdir mktemp rm chmod ln readlink tar gzip cp sleep mv; do
    ln -s "$(command -v "$tool")" "$fake/$tool"
  done
  cat > "$fake/curl" <<'EOF'
#!/bin/bash
printf '{"tag_name":"v9.9.9"}\n'
EOF
  chmod 0755 "$fake/curl"
  status=0
  output=$(HOME="$root/home" INSTALL_DIR="$install" PATH="$install:$fake" "$SCRIPT" 2>&1) || status=$?
  if [[ "$status" -eq 0 && -L "$install/dotfiles" && $(readlink "$install/dotfiles") == dot ]]; then
    PASS=$((PASS + 1)); echo "  ✓ no-op install repairs the compatibility symlink"
  else
    FAIL=$((FAIL + 1)); ERRORS+=("FAIL: no-op install should repair compatibility symlink: $output")
  fi
}

echo ""
echo "--- controlled checksum and rollback matrix ---"
assert_case success sha256sum success
assert_case success shasum success
assert_case missingtool none failure
for failure in missing malformed duplicate mismatch curlfail extract invalid-binary promote regular-promote interrupt; do
  assert_case "$failure" sha256sum failure
done
assert_noop_repairs_compatibility_symlink

report
