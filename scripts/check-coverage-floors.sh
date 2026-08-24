#!/usr/bin/env bash
# Fail the build when a package carries no coverage floor, or a floor names no
# package.
#
# The floor table in .testcoverage.yml is hand-maintained; the package set it
# describes is not. That asymmetry is the whole reason this script exists. A
# package added later arrives with no floor and is measured against nothing, and
# a floor left behind after a rename sits there looking like enforcement
# forever. Both are the same set comparison read in opposite directions.
#
# The parsing below is grep- and awk-shaped rather than a real YAML parse, which
# makes it brittle by construction: a reformat of the config can stop it seeing
# entries it used to see. That is a deliberate trade, and the emptiness guard is
# what pays for it. Every gate in this repository that quietly broke, broke by
# having nothing to check and reporting success -- the govulncheck gate's own
# scenario header records that history. So this script refuses to reach a
# verdict on empty inputs, and prints what it examined before it reports
# anything, which converts brittleness into a loud failure instead of a silent
# pass.
#
# It reads `go list` and the config, and nothing else. It deliberately reads no
# coverage profile: whether a package is MEASURABLE is the threshold checker's
# question and it already answers it (a package with no tests reports 0.0% and
# fails its floor, it does not vanish). This script only asks whether the table
# and the tree still describe the same set of packages.
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)
config_file=${1:-$repo_root/.testcoverage.yml}

if [[ ! -f "$config_file" ]]; then
  echo "check-coverage-floors: config not found: $config_file" >&2
  exit 1
fi

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/check-coverage-floors-XXXXXX")
cleanup() { rm -rf "$work_dir"; }
trap cleanup EXIT

# --- normalisation -----------------------------------------------------------
#
# The two sides spell the same package differently and a literal comparison
# mismatches in BOTH directions, reporting every package missing and every entry
# stale on the same run. Each side gets exactly one normalisation:
#
#   `go list ./...`  emits `<module>/internal/config`  -> strip the module prefix
#   override.path    reads `^internal/config$`         -> strip both anchors
#   exclude.paths    reads `^cmd/dot/`                  -> strip `^` and the
#                                                          trailing separator
#
# The module prefix is READ from `go list -m`, never hard-coded: a module rename
# against a hard-coded string would make every package look missing at once.

module=$(cd -- "$repo_root" && go list -m)
if [[ -z "$module" ]]; then
  echo "check-coverage-floors: 'go list -m' reported no module path, so the package prefix cannot be stripped" >&2
  exit 1
fi

(cd -- "$repo_root" && go list ./...) | sed "s|^${module}/||; s|^${module}\$|.|" | sort -u >"$work_dir/packages"

# One awk pass over the config for both lists. Sections are tracked rather than
# grepped globally so an `override` entry cannot be mistaken for an exclusion.
# `has_reason` is 1 when the entry carries a comment on its own line or on the
# line directly above it, which is what D-04 requires of every exclusion.
awk -v ov="$work_dir/overrides.raw" -v ex="$work_dir/exclusions.raw" '
  {
    line = $0
    if (line ~ /^[^[:space:]#]/) section = ""
    if (line ~ /^override:[[:space:]]*$/) section = "override"
    else if (line ~ /^exclude:[[:space:]]*$/) section = "exclude"
    else if (section == "exclude" && line ~ /^[[:space:]]+paths:[[:space:]]*$/) section = "exclude_paths"
    else if (section == "override" && line ~ /^[[:space:]]*-[[:space:]]+path:/) {
      # A new entry begins, so the previous one can no longer acquire a
      # threshold. Flush it with the verdict it ended up with.
      if (pending != "") print pending_thr "\t" pending >ov
      v = line
      sub(/^[[:space:]]*-[[:space:]]+path:[[:space:]]*/, "", v)
      sub(/[[:space:]]*#.*$/, "", v)
      sub(/[[:space:]]+$/, "", v)
      pending = v
      pending_thr = 0
    }
    # A `threshold:` belonging to the entry above it. Anchored to the list-item
    # indentation so a `threshold:` under `threshold:` at the top of the file
    # cannot be mistaken for one.
    else if (section == "override" && pending != "" && line ~ /^[[:space:]]+threshold:[[:space:]]*-?[0-9]+/) {
      pending_thr = 1
    }
    else if (section == "exclude_paths" && line ~ /^[[:space:]]*-[[:space:]]/) {
      v = line
      sub(/^[[:space:]]*-[[:space:]]*/, "", v)
      reason = (v ~ /#/) || (prev ~ /^[[:space:]]*#/) ? 1 : 0
      sub(/[[:space:]]*#.*$/, "", v)
      sub(/[[:space:]]+$/, "", v)
      if (v != "") print reason "\t" v >ex
    }
    prev = line
  }
  END { if (pending != "") print pending_thr "\t" pending >ov }
' "$config_file"

touch "$work_dir/overrides.raw" "$work_dir/exclusions.raw"

cut -f2 "$work_dir/overrides.raw" | sed 's/^\^//; s/\$$//' | sort -u >"$work_dir/overrides"
cut -f2 "$work_dir/exclusions.raw" | sed 's/^\^//; s|/$||' | sort -u >"$work_dir/exclusions"

package_count=$(wc -l <"$work_dir/packages" | tr -d '[:space:]')
override_count=$(wc -l <"$work_dir/overrides" | tr -d '[:space:]')
exclusion_count=$(wc -l <"$work_dir/exclusions" | tr -d '[:space:]')

# --- fail closed on emptiness ------------------------------------------------
#
# FIRST, before any comparison. A comparison against an empty side succeeds
# vacuously in one direction and reports the whole world missing in the other;
# neither is a verdict worth printing.

if ! grep -q '^override:' "$config_file"; then
  echo "check-coverage-floors: $config_file has no 'override:' key, so no floor table was found" >&2
  exit 1
fi
if ! grep -q '^exclude:' "$config_file"; then
  echo "check-coverage-floors: $config_file has no 'exclude:' key, so no exclusion list was found" >&2
  exit 1
fi
if [[ "$package_count" -eq 0 ]]; then
  echo "check-coverage-floors: 'go list ./...' reported no packages; refusing to report success having compared nothing" >&2
  exit 1
fi
if [[ "$override_count" -eq 0 ]]; then
  echo "check-coverage-floors: parsed zero override entries from $config_file; refusing to report success having compared nothing" >&2
  exit 1
fi
if [[ "$exclusion_count" -eq 0 ]]; then
  echo "check-coverage-floors: parsed zero exclusion entries from $config_file; refusing to report success having compared nothing" >&2
  exit 1
fi

echo "check-coverage-floors: $package_count package(s), $override_count override(s), $exclusion_count exclusion(s)"

# --- override entries must be complete and anchored --------------------------
#
# Both checks run on the RAW entries, before normalisation erases the very thing
# they inspect. Each closes a way for this table to look seeded while enforcing
# nothing, and both were found by review on PR #87 rather than by design:
#
#   - An entry with a `path` but no `threshold` is not an error to the coverage
#     tool. It falls back to `threshold.package`, which this config sets to 0
#     deliberately, so the package is measured against nothing while every gate
#     stays green. Reproduced: dropping `internal/guard`'s threshold left a
#     92.3% package enforced at 0 with both CI steps passing.
#   - An override path missing its trailing `$` still normalises to a live
#     package name here, so this script blesses it, while the coverage tool's
#     FIRST matching override wins and shadows the sibling's own entry.
#     Reproduced: unanchoring `^internal/config` judged `internal/config/catalog`
#     at config's floor instead of its own.
#
# Neither is caught by the set comparison below, which sees only the normalised
# name and cannot tell a complete entry from a hollow one.

incomplete=0
while IFS=$'\t' read -r has_threshold path; do
  [[ -z "$path" ]] && continue
  if [[ "$has_threshold" != "1" ]]; then
    echo "check-coverage-floors: override '$path' has no 'threshold:' line, so it falls back to threshold.package (0) and enforces nothing" >&2
    incomplete=$((incomplete + 1))
  fi
  if [[ "$path" != "^"* || "$path" != *'$' ]]; then
    echo "check-coverage-floors: override '$path' is not anchored at both ends (^...\$), so it can also match a sibling package and shadow that package's own floor" >&2
    incomplete=$((incomplete + 1))
  fi
done <"$work_dir/overrides.raw"

if [[ "$incomplete" -gt 0 ]]; then
  echo "check-coverage-floors: $incomplete malformed override entr(ies); a floor that matches loosely or carries no threshold is not enforcement" >&2
  exit 1
fi

# --- the comparison, read in both directions ---------------------------------

findings=0

# Completeness: a package in neither list is measured against nothing.
while IFS= read -r pkg; do
  [[ -z "$pkg" ]] && continue
  echo "check-coverage-floors: package $pkg has no floor; add an 'override' entry (^$pkg\$) or an 'exclude.paths' entry with a reason" >&2
  findings=$((findings + 1))
done < <(cat "$work_dir/overrides" "$work_dir/exclusions" | sort -u | comm -23 "$work_dir/packages" -)

# Staleness: an entry naming no package outlived what it described. Exclusions
# are compared as whole packages, so a directory-prefix exclusion spanning
# several packages fails here rather than passing unnoticed -- the loud
# direction, and the one that keeps the two lists a partition of the tree.
while IFS= read -r entry; do
  [[ -z "$entry" ]] && continue
  echo "check-coverage-floors: override '^$entry\$' matches no package reported by 'go list ./...'" >&2
  findings=$((findings + 1))
done < <(comm -23 "$work_dir/overrides" "$work_dir/packages")

while IFS= read -r entry; do
  [[ -z "$entry" ]] && continue
  echo "check-coverage-floors: exclusion '^$entry/' matches no package reported by 'go list ./...'" >&2
  findings=$((findings + 1))
done < <(comm -23 "$work_dir/exclusions" "$work_dir/packages")

# No double listing: the two lists disagree about whether the package is
# measured, and nothing in the config says which one wins.
while IFS= read -r entry; do
  [[ -z "$entry" ]] && continue
  echo "check-coverage-floors: package $entry is in BOTH 'override' and 'exclude.paths'; it is either measured or it is not" >&2
  findings=$((findings + 1))
done < <(comm -12 "$work_dir/overrides" "$work_dir/exclusions")

# Reasons: D-04 requires every exclusion to name what covers it instead. An
# exclusion without a reason is indistinguishable from an oversight, so the
# requirement is enforced here rather than left to review.
while IFS=$'\t' read -r reason entry; do
  [[ -z "$entry" ]] && continue
  if [[ "$reason" != "1" ]]; then
    echo "check-coverage-floors: exclusion '$entry' carries no comment; every exclusion states what covers that package instead (D-04)" >&2
    findings=$((findings + 1))
  fi
done <"$work_dir/exclusions.raw"

if [[ "$findings" -gt 0 ]]; then
  echo "check-coverage-floors: $findings finding(s) across $package_count package(s)" >&2
  exit 1
fi

echo "check-coverage-floors: every package is in exactly one list, every entry names a live package, every exclusion carries a reason"
