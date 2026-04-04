#!/usr/bin/env bash
# tools.sh — Tool launchers with fallback chains for dotfiles workspace
# NOTE: Do NOT set -e here — layout functions need to tolerate tmux errors

# ── Helpers ──────────────────────────────────────────────────────────────────

has_cmd() { command -v "$1" &>/dev/null; }

label_pane() {
  local pane="${1:-}" name="${2:-}"
  [ -n "$pane" ] && [ -n "$name" ] && tmux select-pane -t "$pane" -T "$name" 2>/dev/null || true
}

# ── Tool Launchers ───────────────────────────────────────────────────────────
# Each function expects $PANE to be set to the target pane ID.

run_claude() {
  local project_dir="${1:-.}"
  label_pane "$PANE" "CLAUDE"
  if has_cmd claude; then
    tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir") && claude" Enter
  else
    tmux send-keys -t "$PANE" "echo '⚠ Claude Code not installed. Run: npm install -g @anthropic-ai/claude-code'" Enter
  fi
}

run_remote() {
  local project_dir="${1:-.}"
  label_pane "$PANE" "REMOTE"
  if has_cmd claude; then
    tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir") && claude remote-control" Enter
  else
    tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir")" Enter
  fi
}

run_monitor() {
  label_pane "$PANE" "MONITOR"
  if has_cmd btop; then
    tmux send-keys -t "$PANE" "btop" Enter
  elif has_cmd htop; then
    tmux send-keys -t "$PANE" "htop" Enter
  else
    tmux send-keys -t "$PANE" "top" Enter
  fi
}

run_lazygit() {
  local project_dir="${1:-.}"
  label_pane "$PANE" "GIT"
  if has_cmd lazygit; then
    tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir") && lazygit" Enter
  else
    tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir") && git status" Enter
  fi
}

run_filetree() {
  local project_dir="${1:-.}"
  label_pane "$PANE" "FILES"
  if has_cmd yazi; then
    tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir") && yazi" Enter
  elif has_cmd eza; then
    tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir") && eza --tree --level=3 --icons" Enter
  elif has_cmd tree; then
    tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir") && tree -L 3" Enter
  else
    tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir") && ls -la" Enter
  fi
}

run_shell() {
  local project_dir="${1:-.}"
  label_pane "$PANE" "SHELL"
  tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir")" Enter
}

run_logs() {
  local project_dir="${1:-.}"
  label_pane "$PANE" "LOGS"
  tmux send-keys -t "$PANE" "cd $(printf '%q' "$project_dir") && echo '── logs pane ── (tail -f your.log)'" Enter
}
