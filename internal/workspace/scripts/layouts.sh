#!/usr/bin/env bash
# layouts.sh — Tmux workspace layout definitions for dotfiles workspace

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/tools.sh"
source "$SCRIPT_DIR/themes.sh"

# ── Layout: dev (default, 5-pane, laptop-friendly) ──────────────────────────
# ┌──────────────┬──────────┐
# │              │  MONITOR │
# │   CLAUDE     ├──────────┤
# │              │  FILES   │
# ├──────────────┼──────────┤
# │  LAZYGIT     │   SHELL  │
# └──────────────┴──────────┘
layout_dev() {
  local session="$1" project="$2" theme="$3"

  # Create session — first pane becomes CLAUDE
  tmux new-session -d -s "$session" -c "$project" -x 220 -y 60
  local CLAUDE
  CLAUDE=$(tmux list-panes -t "$session" -F '#{pane_id}' | head -1)

  # Right: MONITOR (40% width)
  PANE=$(tmux split-window -h -t "$CLAUDE" -c "$project" -l 40% -PF '#{pane_id}')
  local MONITOR="$PANE"
  run_monitor

  # Below MONITOR: FILES (50% height of right column)
  PANE=$(tmux split-window -v -t "$MONITOR" -c "$project" -l 50% -PF '#{pane_id}')
  run_filetree "$project"

  # Below CLAUDE: LAZYGIT (35% height of left column)
  PANE=$(tmux split-window -v -t "$CLAUDE" -c "$project" -l 35% -PF '#{pane_id}')
  local LAZYGIT="$PANE"
  run_lazygit "$project"

  # Right of LAZYGIT: SHELL (45% width of bottom)
  PANE=$(tmux split-window -h -t "$LAZYGIT" -c "$project" -l 45% -PF '#{pane_id}')
  run_shell "$project"

  # Launch Claude in the first pane
  PANE="$CLAUDE"
  run_claude "$project"

  apply_theme "$theme" "$session"
  tmux select-pane -t "$CLAUDE"
}

# ── Layout: claude (7-pane, Claude-focused) ──────────────────────────────────
# ┌──────────────┬──────────┐
# │              │  MONITOR │
# │   CLAUDE     ├──────────┤
# │              │  FILES   │
# │              ├──────────┤
# │              │  REMOTE  │
# ├──────────────┼─────┬────┤
# │   LAZYGIT    │SHELL│LOG │
# └──────────────┴─────┴────┘
layout_claude() {
  local session="$1" project="$2" theme="$3"

  tmux new-session -d -s "$session" -c "$project" -x 220 -y 60
  local CLAUDE
  CLAUDE=$(tmux list-panes -t "$session" -F '#{pane_id}' | head -1)

  # Right column: MONITOR (40% width)
  PANE=$(tmux split-window -h -t "$CLAUDE" -c "$project" -l 40% -PF '#{pane_id}')
  local MONITOR="$PANE"
  run_monitor

  # Below MONITOR: FILES (60% of right column)
  PANE=$(tmux split-window -v -t "$MONITOR" -c "$project" -l 60% -PF '#{pane_id}')
  local FILES="$PANE"
  run_filetree "$project"

  # Below FILES: REMOTE (50% of remaining right)
  PANE=$(tmux split-window -v -t "$FILES" -c "$project" -l 50% -PF '#{pane_id}')
  run_remote "$project"

  # Below CLAUDE: LAZYGIT (30% height)
  PANE=$(tmux split-window -v -t "$CLAUDE" -c "$project" -l 30% -PF '#{pane_id}')
  local LAZYGIT="$PANE"
  run_lazygit "$project"

  # Right of LAZYGIT: SHELL (60% of bottom)
  PANE=$(tmux split-window -h -t "$LAZYGIT" -c "$project" -l 60% -PF '#{pane_id}')
  local SHELL_PANE="$PANE"
  run_shell "$project"

  # Right of SHELL: LOG (40% of remaining bottom)
  PANE=$(tmux split-window -h -t "$SHELL_PANE" -c "$project" -l 40% -PF '#{pane_id}')
  run_logs "$project"

  # Claude in first pane
  PANE="$CLAUDE"
  run_claude "$project"

  apply_theme "$theme" "$session"
  tmux select-pane -t "$CLAUDE"
}

# ── Layout: monitor (4-pane, server monitoring) ──────────────────────────────
# ┌──────────────┬──────────┐
# │   MONITOR    │  SHELL   │
# ├──────────────┼──────────┤
# │   LAZYGIT    │  LOGS    │
# └──────────────┴──────────┘
layout_monitor() {
  local session="$1" project="$2" theme="$3"

  tmux new-session -d -s "$session" -c "$project" -x 220 -y 60
  local FIRST
  FIRST=$(tmux list-panes -t "$session" -F '#{pane_id}' | head -1)

  # Right: SHELL (45% width)
  PANE=$(tmux split-window -h -t "$FIRST" -c "$project" -l 45% -PF '#{pane_id}')
  local SHELL_PANE="$PANE"
  run_shell "$project"

  # Below FIRST: LAZYGIT (50% height)
  PANE=$(tmux split-window -v -t "$FIRST" -c "$project" -l 50% -PF '#{pane_id}')
  run_lazygit "$project"

  # Below SHELL: LOGS (50% height)
  PANE=$(tmux split-window -v -t "$SHELL_PANE" -c "$project" -l 50% -PF '#{pane_id}')
  run_logs "$project"

  # MONITOR in first pane
  PANE="$FIRST"
  run_monitor

  apply_theme "$theme" "$session"
  tmux select-pane -t "$FIRST"
}

# ── Layout dispatcher ────────────────────────────────────────────────────────

load_layout() {
  local layout="$1" session="$2" project="$3" theme="${4:-default}"

  case "$layout" in
    dev)     layout_dev "$session" "$project" "$theme" ;;
    claude)  layout_claude "$session" "$project" "$theme" ;;
    monitor) layout_monitor "$session" "$project" "$theme" ;;
    *)
      echo "Unknown layout: $layout (using dev)"
      layout_dev "$session" "$project" "$theme"
      ;;
  esac
}

list_layouts() {
  echo "Available layouts:"
  echo "  dev      Default 5-pane layout (Claude + monitor + files + git + shell)"
  echo "  claude   7-pane Claude-focused (Claude + monitor + files + remote + git + shell + logs)"
  echo "  monitor  4-pane server monitoring (monitor + git + shell + logs)"
}
