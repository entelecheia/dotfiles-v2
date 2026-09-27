package watchdog

import (
	"path"
	"strings"
)

// MatchCandidate reports whether a process is one the reaper may act on, and
// a short reason for the decision (used in log records). A process is a
// candidate only when the match config selects it AND the allowlist does
// not. With no path glob and no args substring configured, nothing matches:
// the reaper denies by default so an empty profile can never kill anything.
func MatchCandidate(p Process, s ReaperSettings, expandEnv func(string) string) (bool, string) {
	if s.OrphanOnly && p.PPID != 1 {
		return false, "not an orphan"
	}
	for _, allow := range s.Allow {
		if allow != "" && strings.Contains(p.Args, allow) {
			return false, "allowlisted: " + allow
		}
	}
	exe := p.Executable()
	for _, pattern := range s.Paths {
		if pattern == "" {
			continue
		}
		if globMatch(expandEnv(pattern), exe) {
			return true, "path matches " + pattern
		}
	}
	for _, sub := range s.Args {
		if sub != "" && strings.Contains(p.Args, sub) {
			return true, "args contain " + sub
		}
	}
	return false, "no match rule selected it"
}

// globMatch matches slash-separated path patterns where "*" and "?" work
// within one segment (path.Match semantics) and "**" spans zero or more
// whole segments. The pattern must be fully expanded already.
func globMatch(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for {
				if matchSegments(pat[1:], name) {
					return true
				}
				if len(name) == 0 {
					return false
				}
				name = name[1:]
			}
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat = pat[1:]
		name = name[1:]
	}
	return len(name) == 0
}
