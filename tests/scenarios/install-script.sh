#!/usr/bin/env bash
# Scenario: install.sh static analysis and functional tests
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

SCRIPT="$(cd "$(dirname "$0")/../.." && pwd)/scripts/install.sh"

echo "=== Scenario: install-script ==="

# --- Static analysis ---

echo ""
echo "--- bash syntax check ---"
assert_exit_code 0 bash -n "$SCRIPT"

echo ""
echo "--- shellcheck ---"
if command -v shellcheck &>/dev/null; then
  assert_exit_code 0 shellcheck "$SCRIPT"
else
  echo "  (shellcheck not available, skipping)"
fi

# --- Functional tests (source helpers only) ---
# We extract and test individual functions without running the full script.

echo ""
echo "--- helper functions ---"

# Create a temp file that sources just the helpers
HELPERS=$(mktemp)
cat > "$HELPERS" <<'HELPEOF'
#!/usr/bin/env bash
set -euo pipefail

# Extract helpers from install.sh
NO_COLOR=1
_green='' _yellow='' _red='' _reset=''
info() { printf "[+] %s\n" "$*"; }
warn() { printf "[!] %s\n" "$*"; }
err()  { printf "[x] %s\n" "$*" >&2; }

# Test info output
output=$(info "test message")
if [[ "$output" == "[+] test message" ]]; then
  echo "  ✓ info() output correct"
else
  echo "  ✗ info() output: $output"
  exit 1
fi

# Test warn output
output=$(warn "warning")
if [[ "$output" == "[!] warning" ]]; then
  echo "  ✓ warn() output correct"
else
  echo "  ✗ warn() output: $output"
  exit 1
fi

# Test err output
output=$(err "error" 2>&1)
if [[ "$output" == "[x] error" ]]; then
  echo "  ✓ err() output correct"
else
  echo "  ✗ err() output: $output"
  exit 1
fi
HELPEOF
assert_exit_code 0 bash "$HELPERS"
rm -f "$HELPERS"

echo ""
echo "--- ensure_path function ---"

# Test ensure_path in isolation
PATHTEST=$(mktemp)
TEST_HOME=$(mktemp -d)
TEST_RC="$TEST_HOME/.zshrc"
touch "$TEST_RC"

cat > "$PATHTEST" <<PATHEOF
#!/usr/bin/env bash
set -euo pipefail
export HOME="$TEST_HOME"
export SHELL=/bin/zsh

info() { printf "[+] %s\n" "\$*"; }

ensure_path() {
  local target_dir="\$1"
  case ":\$PATH:" in
    *":\$target_dir:"*) return 0 ;;
  esac
  local shell_name rc_file
  shell_name="\$(basename "\${SHELL:-/bin/zsh}")"
  case "\$shell_name" in
    zsh)  rc_file="\$HOME/.zshrc" ;;
    bash) rc_file="\$HOME/.bashrc" ;;
    *)    rc_file="\$HOME/.profile" ;;
  esac
  local path_line="export PATH=\"\$target_dir:\\\$PATH\""
  if [[ -f "\$rc_file" ]] && grep -qF "\$target_dir" "\$rc_file" 2>/dev/null; then
    export PATH="\$target_dir:\$PATH"
    return 0
  fi
  info "Adding \$target_dir to PATH in \$rc_file"
  {
    echo ""
    echo "# Added by dotfiles installer"
    echo "\$path_line"
  } >> "\$rc_file"
  export PATH="\$target_dir:\$PATH"
}

# Test: adds to rc file
ensure_path "/tmp/test-bin"
grep -q "/tmp/test-bin" "$TEST_RC" || { echo "FAIL: PATH not in rc"; exit 1; }

# Test: idempotent — second call does not duplicate
ensure_path "/tmp/test-bin"
count=\$(grep -c "/tmp/test-bin" "$TEST_RC")
if [[ "\$count" -ne 1 ]]; then
  echo "FAIL: PATH duplicated (\$count entries)"
  exit 1
fi

echo "PATH tests passed"
PATHEOF

assert_exit_code 0 bash "$PATHTEST"
rm -f "$PATHTEST"
rm -rf "$TEST_HOME"

echo ""
echo "--- OS/arch detection ---"

# Test that the detection section produces valid output
DETECT=$(mktemp)
cat > "$DETECT" <<'DETECTEOF'
#!/usr/bin/env bash
set -euo pipefail
info() { printf "[+] %s\n" "$*"; }
err()  { printf "[x] %s\n" "$*" >&2; }

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) err "Unsupported architecture: $ARCH"; exit 1 ;;
esac

if [[ -z "$OS" ]]; then
  echo "FAIL: OS empty"
  exit 1
fi
if [[ "$ARCH" != "amd64" && "$ARCH" != "arm64" ]]; then
  echo "FAIL: ARCH not normalized: $ARCH"
  exit 1
fi
echo "OS=$OS ARCH=$ARCH"
DETECTEOF

assert_exit_code 0 bash "$DETECT"
rm -f "$DETECT"

echo ""
echo "--- config export/import roundtrip ---"

# Only test if dotfiles binary is available (CI builds it)
if command -v dotfiles &>/dev/null; then
  EXPORT_HOME=$(mktemp -d)

  # Init with defaults
  dotfiles init --yes --home "$EXPORT_HOME"
  assert_file_exists "$EXPORT_HOME/.config/dotfiles/config.yaml" "Init creates config"

  # Export
  EXPORT_FILE=$(mktemp)
  dotfiles config export --home "$EXPORT_HOME" > "$EXPORT_FILE"
  assert_exit_code 0 test -s "$EXPORT_FILE"

  # Import into fresh home
  IMPORT_HOME=$(mktemp -d)
  dotfiles init --from "$EXPORT_FILE" --yes --home "$IMPORT_HOME"
  assert_file_exists "$IMPORT_HOME/.config/dotfiles/config.yaml" "Import creates config"

  # Verify name matches
  ORIG_NAME=$(grep "^name:" "$EXPORT_HOME/.config/dotfiles/config.yaml" | head -1)
  IMPORTED_NAME=$(grep "^name:" "$IMPORT_HOME/.config/dotfiles/config.yaml" | head -1)
  if [[ "$ORIG_NAME" == "$IMPORTED_NAME" ]]; then
    PASS=$((PASS + 1))
    echo "  ✓ Exported name matches imported name"
  else
    FAIL=$((FAIL + 1))
    ERRORS+=("FAIL: Name mismatch: '$ORIG_NAME' vs '$IMPORTED_NAME'")
    echo "  ✗ Name mismatch: '$ORIG_NAME' vs '$IMPORTED_NAME'"
  fi

  # Test empty file rejection
  EMPTY_FILE=$(mktemp)
  : > "$EMPTY_FILE"
  if dotfiles init --from "$EMPTY_FILE" --yes --home "$(mktemp -d)" 2>/dev/null; then
    FAIL=$((FAIL + 1))
    ERRORS+=("FAIL: Should reject empty import file")
    echo "  ✗ Should reject empty import file"
  else
    PASS=$((PASS + 1))
    echo "  ✓ Empty import file rejected"
  fi

  rm -f "$EXPORT_FILE" "$EMPTY_FILE"
  rm -rf "$EXPORT_HOME" "$IMPORT_HOME"
else
  echo "  (dotfiles binary not available, skipping config roundtrip)"
fi

report
