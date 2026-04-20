#!/usr/bin/env bash
# Install the catalog's `defaults` cask set on macOS and verify each app
# actually landed in /Applications.
#
# Runs only on darwin. Expected to be invoked from GitHub Actions
# (macos-latest) after the dotfiles binary has been built at ./bin/dotfiles.
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "skipped: non-darwin host"
  exit 0
fi

CATALOG="${CATALOG:-internal/config/catalog/macos-apps.yaml}"
if [[ ! -f "$CATALOG" ]]; then
  echo "catalog not found: $CATALOG" >&2
  exit 1
fi

# Parse the top-level `defaults:` list. Stops at the next top-level key.
# Built with a while-read loop so the script runs under bash 3.2 (macOS default).
DEFAULTS=()
while IFS= read -r token; do
  DEFAULTS+=("$token")
done < <(awk '
  /^defaults:[[:space:]]*$/ { in_defaults = 1; next }
  in_defaults && /^[a-zA-Z]/  { in_defaults = 0 }
  in_defaults && /^[[:space:]]*-[[:space:]]*/ {
    sub(/^[[:space:]]*-[[:space:]]*/, "")
    sub(/[[:space:]]*(#.*)?$/, "")
    if (length($0)) print
  }
' "$CATALOG")

if [[ ${#DEFAULTS[@]} -eq 0 ]]; then
  echo "no defaults parsed from $CATALOG" >&2
  exit 1
fi

echo "Defaults (${#DEFAULTS[@]}): ${DEFAULTS[*]}"

if ! command -v brew >/dev/null 2>&1; then
  echo "brew not found on PATH" >&2
  exit 1
fi

BIN="${BIN:-./bin/dotfiles}"
if [[ ! -x "$BIN" ]]; then
  echo "dotfiles binary not found at $BIN" >&2
  exit 1
fi

echo "=== Installing default casks via $BIN apps install --defaults --yes ==="
"$BIN" apps install --defaults --yes

echo "=== Verifying each default cask has its .app under /Applications ==="
# Note: verification keys off /Applications/<Name>.app directly (not
# `brew list --cask`) so casks that were skipped because they were already
# present (App Store, manual install) still count as verified.
failed=0
for token in "${DEFAULTS[@]}"; do
  # Extract the primary .app artifact name from the cask metadata.
  # Supports both string entries and [source, {target: ...}] tuples.
  app_name=$(brew info --cask "$token" --json=v2 2>/dev/null \
    | jq -r '[.casks[0].artifacts[]? | objects | .app // empty | .[]? | if type=="array" then (.[1].target // .[0]) else . end | strings] | .[0] // empty' \
    | xargs -I{} basename "{}")

  if [[ -z "$app_name" ]]; then
    echo "  ✗ $token: no .app artifact declared in cask metadata"
    failed=$((failed + 1))
    continue
  fi

  app_path="/Applications/$app_name"
  if [[ ! -d "$app_path" ]]; then
    echo "  ✗ $token: $app_path not found"
    failed=$((failed + 1))
    continue
  fi

  echo "  ✓ $token → $app_path"
done

echo
if (( failed > 0 )); then
  echo "FAIL: $failed of ${#DEFAULTS[@]} default cask(s) did not verify"
  exit 1
fi
echo "OK: all ${#DEFAULTS[@]} default cask(s) verified"
