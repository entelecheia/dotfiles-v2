#!/usr/bin/env bash
# themes.sh — Tmux theme definitions for dotfiles workspace

# Apply a named theme to the current tmux session.
# Uses session-scoped options (not -g) so multiple workspaces can have different themes.
# Usage: apply_theme <theme_name> <session_name>
apply_theme() {
  local theme="${1:-default}"
  local session="${2:?apply_theme requires session name}"

  case "$theme" in
    dracula)
      # Purple-focused dark theme
      _apply_session_theme "$session" \
        "#282a36" "#f8f8f2" \
        "#bd93f9" "#44475a" \
        "#6272a4" "#bd93f9"
      ;;
    nord)
      # Arctic blue-grey theme
      _apply_session_theme "$session" \
        "#2e3440" "#d8dee9" \
        "#88c0d0" "#3b4252" \
        "#4c566a" "#88c0d0"
      ;;
    catppuccin)
      # Warm pastel theme (mocha variant)
      _apply_session_theme "$session" \
        "#1e1e2e" "#cdd6f4" \
        "#cba6f7" "#313244" \
        "#585b70" "#cba6f7"
      ;;
    tokyo-night)
      # Deep indigo theme
      _apply_session_theme "$session" \
        "#1a1b26" "#a9b1d6" \
        "#7aa2f7" "#24283b" \
        "#3b4261" "#7aa2f7"
      ;;
    *) # default — clean blue accent
      _apply_session_theme "$session" \
        "#1c1c1c" "#d0d0d0" \
        "#00afff" "#262626" \
        "#444444" "#00afff"
      ;;
  esac
}

# Internal: apply theme colors to a specific tmux session.
# Uses session-scoped set-option (no -g) so themes don't bleed across sessions.
# Args: session status_bg status_fg accent border_inactive_fg border_active_fg
_apply_session_theme() {
  local session="$1"
  local status_bg="$2" status_fg="$3"
  local accent="$4" status_bg2="$5"
  local border_fg="$6" border_active_fg="$7"

  # Status bar (session-scoped)
  tmux set-option -t "$session" status-style "bg=$status_bg,fg=$status_fg" 2>/dev/null || true
  tmux set-option -t "$session" status-left " #[fg=$accent,bold]#S #[default]" 2>/dev/null || true
  tmux set-option -t "$session" status-right "#[fg=$status_fg]#H #[fg=$border_fg]| #[fg=$status_fg]%H:%M " 2>/dev/null || true
  tmux set-window-option -t "$session" window-status-current-format "#[fg=$accent,bold] #I:#W #[default]" 2>/dev/null || true
  tmux set-window-option -t "$session" window-status-format " #[fg=$status_fg]#I:#W " 2>/dev/null || true

  # Pane borders (session-scoped)
  tmux set-option -t "$session" pane-border-style "fg=$border_fg" 2>/dev/null || true
  tmux set-option -t "$session" pane-active-border-style "fg=$border_active_fg" 2>/dev/null || true

  # Message style
  tmux set-option -t "$session" message-style "bg=$status_bg2,fg=$accent" 2>/dev/null || true
}
