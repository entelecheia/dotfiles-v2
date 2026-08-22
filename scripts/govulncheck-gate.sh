#!/usr/bin/env bash
# Fail the build on a called govulncheck finding that no live allowlist entry covers.
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
allow_file="$script_dir/../.govulncheck-allow.json"
input_file=${1:-}
today=$(date -u +%Y-%m-%d)

if ! command -v jq >/dev/null 2>&1; then
  echo "govulncheck gate: jq is required" >&2
  exit 1
fi

if [[ ! -f "$allow_file" ]]; then
  echo "govulncheck gate: allowlist not found: $allow_file" >&2
  exit 1
fi

if ! jq -e '.allow | type == "array"' "$allow_file" >/dev/null 2>&1; then
  echo "govulncheck gate: malformed allowlist: $allow_file has no .allow array" >&2
  exit 1
fi

# D-08: stdlib is closed by raising the go directive, never by suppression.
stdlib_entries=$(jq -r '.allow[] | select(.module == "stdlib") | .id // "(no id)"' "$allow_file")
if [[ -n "$stdlib_entries" ]]; then
  echo "govulncheck gate: malformed allowlist: stdlib entries are never allowlistable" >&2
  while IFS= read -r entry_id; do
    echo "govulncheck gate:   $entry_id names module stdlib; raise the go directive in go.mod instead" >&2
  done <<<"$stdlib_entries"
  exit 1
fi

scan_file=""
cleanup() {
  if [[ -n "$scan_file" ]]; then
    rm -f "$scan_file"
  fi
}
trap cleanup EXIT

if [[ -z "$input_file" ]]; then
  if ! command -v govulncheck >/dev/null 2>&1; then
    echo "govulncheck gate: govulncheck is required (go install golang.org/x/vuln/cmd/govulncheck@v1.7.0)" >&2
    exit 1
  fi
  # Not `mktemp -t PREFIX`: BSD mktemp accepts a bare prefix, GNU mktemp treats
  # -t's argument as a template and rejects it with "too few X's". An explicit
  # path template with trailing X's is the form both accept.
  scan_file=$(mktemp "${TMPDIR:-/tmp}/govulncheck-gate-XXXXXX")
  # -format json always exits 0, so this script's exit code is the gate.
  govulncheck -format json ./... >"$scan_file"
  input_file="$scan_file"
elif [[ ! -f "$input_file" ]]; then
  echo "govulncheck gate: scan output not found: $input_file" >&2
  exit 1
fi

total=$(jq -s 'map(select(has("finding"))) | length' "$input_file")

# D-07: a finding is CALLED when some trace frame carries a function key.
# Module-level and package-level findings for the same id have no such frame.
called=$(jq -r -s '
  map(select(has("finding")) | .finding)
  | map(select((.trace // []) | any(has("function"))))
  | .[]
  | [ (.osv // "unknown"), ((.trace[0].module) // "unknown"), (.fixed_version // "none") ]
  | @tsv
' "$input_file")

fatal=0
suppressed=0
called_count=0

if [[ -n "$called" ]]; then
  while IFS=$'\t' read -r osv module fixed_version; do
    [[ -z "$osv" ]] && continue
    called_count=$((called_count + 1))

    if [[ "$module" == "stdlib" ]]; then
      echo "govulncheck gate: $osv is a called standard-library finding (fixed in $fixed_version)" >&2
      echo "govulncheck gate:   stdlib findings are never allowlistable; raise the go directive in go.mod" >&2
      fatal=$((fatal + 1))
      continue
    fi

    expires=$(jq -r --arg id "$osv" 'first(.allow[] | select(.id == $id) | .expires // "") // ""' "$allow_file")
    if [[ -z "$expires" ]]; then
      echo "govulncheck gate: $osv is called in $module and is not allowlisted (fixed in $fixed_version)" >&2
      fatal=$((fatal + 1))
      continue
    fi

    # ISO YYYY-MM-DD compares correctly as a string, which avoids the GNU/BSD date split.
    if [[ "$expires" < "$today" ]]; then
      echo "govulncheck gate: $osv allowlist entry expired on $expires (today is $today)" >&2
      echo "govulncheck gate:   renew it with a fresh reason or upgrade $module (fixed in $fixed_version)" >&2
      fatal=$((fatal + 1))
      continue
    fi

    suppressed=$((suppressed + 1))
  done <<<"$called"
fi

if [[ "$fatal" -gt 0 ]]; then
  echo "govulncheck gate: $fatal of $called_count called finding(s) are not covered by a live allowlist entry" >&2
  exit 1
fi

echo "govulncheck gate: $total finding(s) seen, $called_count called, $suppressed suppressed by allowlist"
