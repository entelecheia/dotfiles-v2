// Package guard implements dot-owned Claude Code PreToolUse safety hooks:
// careful (warn before destructive shell commands) and freeze (deny file
// mutations outside a chosen directory). The semantics are ported from
// gstack's check-careful.sh / check-freeze.sh and preserved verbatim where
// sensible; deviations are commented at the site.
package guard

import (
	"regexp"
	"strings"
)

// Decision is the outcome of a guard check for one tool call.
// An empty Permission means guard has no opinion (normal permission flow).
type Decision struct {
	Permission string // "", "ask", or "deny"
	Reason     string
	Pattern    string
}

// carefulPatterns mirrors gstack check-careful.sh: first match wins, every
// pattern warns with "ask" so the user can still override.
var carefulPatterns = []struct {
	name   string
	reason string
	re     *regexp.Regexp
	lower  bool // match against the lowercased command (SQL patterns)
}{
	{"rm_recursive", "Destructive: recursive delete (rm -r). This permanently removes files.",
		regexp.MustCompile(`rm\s+(-[a-zA-Z]*r|--recursive)`), false},
	{"drop_table", "Destructive: SQL DROP detected. This permanently deletes database objects.",
		regexp.MustCompile(`drop\s+(table|database)`), true},
	{"truncate", "Destructive: SQL TRUNCATE detected. This deletes all rows from a table.",
		regexp.MustCompile(`\btruncate\b`), true},
	{"git_force_push", "Destructive: git force-push rewrites remote history. Other contributors may lose work.",
		regexp.MustCompile(`git\s+push\s+.*(-f\b|--force)`), false},
	{"git_reset_hard", "Destructive: git reset --hard discards all uncommitted changes.",
		regexp.MustCompile(`git\s+reset\s+--hard`), false},
	{"git_discard", "Destructive: discards all uncommitted changes in the working tree.",
		regexp.MustCompile(`git\s+(checkout|restore)\s+\.`), false},
	{"kubectl_delete", "Destructive: kubectl delete removes Kubernetes resources. May impact production.",
		regexp.MustCompile(`kubectl\s+delete`), false},
	{"docker_destructive", "Destructive: Docker force-remove or prune. May delete running containers or cached images.",
		regexp.MustCompile(`docker\s+(rm\s+-f|system\s+prune)`), false},
}

// rmRecursiveTrigger gates the build-artifact allowlist check.
var rmRecursiveTrigger = regexp.MustCompile(`rm\s+(-[a-zA-Z]*r[a-zA-Z]*\s+|--recursive\s+)`)

// rmArgsPrefix strips everything up to and including the (last) rm
// invocation and its flags, mirroring gstack's greedy
// `sed -E 's/.*rm\s+(-[a-zA-Z]+\s+)*//'`.
var rmArgsPrefix = regexp.MustCompile(`(?s)^.*rm\s+(-[a-zA-Z]+\s+)*`)

// rmSafeTargets are build artifacts whose recursive removal is routine.
var rmSafeTargets = map[string]bool{
	"node_modules": true,
	".next":        true,
	"dist":         true,
	"__pycache__":  true,
	".cache":       true,
	"build":        true,
	".turbo":       true,
	"coverage":     true,
}

// shellSeparators split a compound command so the rm allowlist inspects
// every rm invocation, not just the last one (gstack's greedy sed inspected
// only the last rm, which let `kubectl delete x; rm -rf dist` pass silently).
var shellSeparators = regexp.MustCompile(`\|\||&&|[;|&]`)

// CheckCommand evaluates a Bash tool command against the careful rules.
// Unparseable or empty commands allow (fail open, matching gstack).
func CheckCommand(command string) Decision {
	if strings.TrimSpace(command) == "" {
		return Decision{}
	}
	lower := strings.ToLower(command)
	for _, p := range carefulPatterns {
		subject := command
		if p.lower {
			subject = lower
		}
		if !p.re.MatchString(subject) {
			continue
		}
		// The build-artifact allowlist suppresses only the rm_recursive
		// warning itself; every other destructive pattern in the same
		// command still warns.
		if p.name == "rm_recursive" && rmTargetsAllSafe(command) {
			continue
		}
		return Decision{Permission: "ask", Reason: p.reason, Pattern: p.name}
	}
	return Decision{}
}

// rmTargetsAllSafe reports whether every recursive rm in the command targets
// only known build artifacts (bare name or any path ending in /<name>).
// ponytail: separator split is a heuristic, not a shell parser; a quoted
// separator inside an argument over-splits and fails safe (warns).
func rmTargetsAllSafe(command string) bool {
	for _, segment := range shellSeparators.Split(command, -1) {
		if !rmRecursiveTrigger.MatchString(segment) {
			continue
		}
		rest := rmArgsPrefix.ReplaceAllString(segment, "")
		rest = strings.Replace(rest, "--recursive", "", 1)
		for _, target := range strings.Fields(rest) {
			if strings.HasPrefix(target, "-") {
				continue // flag, skip
			}
			base := target
			if i := strings.LastIndex(target, "/"); i >= 0 {
				base = target[i+1:]
			}
			if !rmSafeTargets[base] {
				return false
			}
		}
	}
	return true
}
