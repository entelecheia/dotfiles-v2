#!/usr/bin/env bash
# Scenario: dot guard lifecycle (enable, hook decisions, freeze, disable)
set -euo pipefail
source "$(dirname "$0")/../assert.sh"

# Local helpers on top of assert.sh counters (no generic pass/fail there).
pass() {
  PASS=$((PASS + 1))
  echo "  ✓ $1"
}
fail() {
  FAIL=$((FAIL + 1))
  ERRORS+=("FAIL: $1")
  echo "  ✗ $1"
}

echo "=== Scenario: guard ==="

CUSTOM_HOME=$(mktemp -d /tmp/dotfiles-guard-XXXX)
SETTINGS="$CUSTOM_HOME/.claude/settings.json"
mkdir -p "$CUSTOM_HOME/.claude"

# Pre-existing foreign hook must survive the guard lifecycle untouched.
cat > "$SETTINGS" <<'EOF'
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "/usr/local/bin/other-tool check" }
        ]
      }
    ]
  }
}
EOF

echo ""
echo "--- guard enable ---"
dot guard enable --home "$CUSTOM_HOME"
assert_file_contains "$SETTINGS" "# dot-guard" "Guard hook entries registered"
assert_file_contains "$SETTINGS" "other-tool check" "Foreign hook preserved after enable"
assert_file_contains "$CUSTOM_HOME/.config/dotfiles/config.yaml" "careful: true" "Careful persisted in state"

echo ""
echo "--- hook decisions ---"
ASK_OUT=$(echo '{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git push --force"}}' | dot guard hook --home "$CUSTOM_HOME")
if echo "$ASK_OUT" | grep -q '"permissionDecision":"ask"'; then
  pass "Destructive Bash command returns ask"
else
  fail "Expected ask decision, got: $ASK_OUT"
fi

ALLOW_OUT=$(echo '{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}' | dot guard hook --home "$CUSTOM_HOME")
if [ "$(echo "$ALLOW_OUT" | tr -d '[:space:]')" = "{}" ]; then
  pass "Harmless Bash command returns {}"
else
  fail "Expected {}, got: $ALLOW_OUT"
fi

echo ""
echo "--- freeze boundary ---"
mkdir -p "$CUSTOM_HOME/project"
dot guard freeze "$CUSTOM_HOME/project" --home "$CUSTOM_HOME"

DENY_OUT=$(echo '{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/etc/hosts"}}' | dot guard hook --home "$CUSTOM_HOME")
if echo "$DENY_OUT" | grep -q '"permissionDecision":"deny"'; then
  pass "Out-of-boundary edit returns deny"
else
  fail "Expected deny decision, got: $DENY_OUT"
fi

INSIDE_OUT=$(echo "{\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"$CUSTOM_HOME/project/a.go\"}}" | dot guard hook --home "$CUSTOM_HOME")
if [ "$(echo "$INSIDE_OUT" | tr -d '[:space:]')" = "{}" ]; then
  pass "In-boundary edit returns {}"
else
  fail "Expected {}, got: $INSIDE_OUT"
fi

dot guard unfreeze --home "$CUSTOM_HOME"

echo ""
echo "--- guard status ---"
dot guard status --home "$CUSTOM_HOME"

echo ""
echo "--- guard disable restores foreign hooks ---"
dot guard disable --home "$CUSTOM_HOME"
if grep -q "# dot-guard" "$SETTINGS"; then
  fail "Guard entries still present after disable"
else
  pass "Guard entries removed"
fi
assert_file_contains "$SETTINGS" "other-tool check" "Foreign hook preserved after disable"

echo ""
echo "--- dry-run makes no changes ---"
BEFORE=$(cat "$SETTINGS")
dot guard enable --home "$CUSTOM_HOME" --dry-run
AFTER=$(cat "$SETTINGS")
if [ "$BEFORE" = "$AFTER" ]; then
  pass "Dry-run enable left settings.json untouched"
else
  fail "Dry-run enable modified settings.json"
fi

# Cleanup
rm -rf "$CUSTOM_HOME"

report
