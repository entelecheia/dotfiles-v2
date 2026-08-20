#!/usr/bin/env bash
set -euo pipefail

binary=${1:-./bin/dot}

if [[ ! -x "$binary" ]]; then
  echo "terminal-query check: binary is not executable: $binary" >&2
  exit 1
fi

if ! command -v script >/dev/null 2>&1; then
  echo "terminal-query check: script(1) is required" >&2
  exit 1
fi

run_in_pty() {
  case "$(uname -s)" in
    Darwin)
      script -q /dev/null \
        env -u CI -u NO_COLOR -u CLICOLOR -u CLICOLOR_FORCE \
        TERM=xterm-256color "$binary" "$@" </dev/null
      ;;
    Linux)
      local command
      printf -v command '%q ' \
        env -u CI -u NO_COLOR -u CLICOLOR -u CLICOLOR_FORCE \
        TERM=xterm-256color "$binary" "$@"
      script -q -e -c "$command" /dev/null </dev/null
      ;;
    *)
      echo "terminal-query check: unsupported platform: $(uname -s)" >&2
      return 1
      ;;
  esac
}

osc11=$'\033]11;?'
dsr=$'\033[6n'

for argument in version --help; do
  output=$(run_in_pty "$argument")
  if [[ -z "$output" ]]; then
    echo "terminal-query check: empty output for dot $argument" >&2
    exit 1
  fi
  if [[ "$output" == *"$osc11"* || "$output" == *"$dsr"* ]]; then
    echo "terminal-query check: unexpected terminal query from dot $argument" >&2
    printf '%s\n' "$output" | cat -v >&2
    exit 1
  fi
done

echo "terminal-query check: no OSC 11 or DSR query emitted"
