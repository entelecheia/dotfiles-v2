package watchdog

import (
	"os"
	"testing"
)

func testSettings() ReaperSettings {
	return ReaperSettings{
		Mode:         ModeDryRun,
		CPUThreshold: 50,
		OrphanOnly:   true,
		Paths:        []string{"$TMPDIR/**", "**/target/debug/**"},
		Args:         []string{"ahub-recovery-cli-"},
		Allow:        []string{"cloudflared", "beszel-agent", "JumpConnect"},
	}
}

func expandWithTMPDIR(t *testing.T) func(string) string {
	t.Helper()
	t.Setenv("TMPDIR", "/var/folders/ab/xyz/T/")
	return os.ExpandEnv
}

func TestMatchCandidate_OrphanVsChild(t *testing.T) {
	s := testSettings()
	expand := expandWithTMPDIR(t)
	orphan := Process{PID: 1, PPID: 1, Args: "/var/folders/ab/xyz/T/ahub-recovery-cli-x/bun"}
	child := Process{PID: 2, PPID: 88123, Args: "/var/folders/ab/xyz/T/ahub-recovery-cli-x/bun"}

	if ok, _ := MatchCandidate(orphan, s, expand); !ok {
		t.Fatal("orphan under $TMPDIR must match")
	}
	if ok, reason := MatchCandidate(child, s, expand); ok {
		t.Fatalf("child process must not match orphan_only config (reason: %s)", reason)
	}

	s.OrphanOnly = false
	if ok, _ := MatchCandidate(child, s, expand); !ok {
		t.Fatal("with orphan_only off the child must match")
	}
}

func TestMatchCandidate_PathGlobs(t *testing.T) {
	s := testSettings()
	expand := expandWithTMPDIR(t)
	cases := []struct {
		name  string
		exe   string
		match bool
	}{
		{"under TMPDIR", "/var/folders/ab/xyz/T/ahub-recovery-cli-x/bun", true},
		{"outside TMPDIR", "/opt/homebrew/bin/bun", false},
		{"cargo debug build", "/Users/x/proj/target/debug/app", true},
		{"cargo release build", "/Users/x/proj/target/release/app", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Process{PID: 1, PPID: 1, Args: tc.exe}
			ok, _ := MatchCandidate(p, s, expand)
			if ok != tc.match {
				t.Errorf("MatchCandidate(%q) = %v, want %v", tc.exe, ok, tc.match)
			}
		})
	}
}

func TestMatchCandidate_ArgsSubstring(t *testing.T) {
	s := testSettings()
	s.Paths = nil // isolate the args rule
	expand := expandWithTMPDIR(t)
	p := Process{PID: 1, PPID: 1, Args: "/opt/homebrew/bin/bun run ahub-recovery-cli-worker"}
	if ok, reason := MatchCandidate(p, s, expand); !ok {
		t.Fatalf("args substring must match: %s", reason)
	}
}

func TestMatchCandidate_AllowlistWins(t *testing.T) {
	s := testSettings()
	expand := expandWithTMPDIR(t)
	p := Process{PID: 1, PPID: 1, Args: "/var/folders/ab/xyz/T/cloudflared tunnel run ahub-recovery-cli-"}
	if ok, reason := MatchCandidate(p, s, expand); ok {
		t.Fatalf("allowlisted process must never be a candidate (reason: %s)", reason)
	}
}

func TestMatchCandidate_DeniesByDefault(t *testing.T) {
	s := testSettings()
	s.Paths = nil
	s.Args = nil
	expand := expandWithTMPDIR(t)
	p := Process{PID: 1, PPID: 1, Args: "/var/folders/ab/xyz/T/ahub-recovery-cli-x/bun"}
	if ok, _ := MatchCandidate(p, s, expand); ok {
		t.Fatal("a config with no match rules must match nothing")
	}
}

func TestGlobMatch_DoubleStar(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"/a/**", "/a/b/c", true},
		{"/a/**", "/a", true},
		{"**/target/debug/**", "/x/target/debug/app", true},
		{"**/target/debug/**", "/x/target/debug/sub/dir/app", true},
		{"**/target/debug/**", "/x/target/release/app", false},
		{"/a/*/c", "/a/b/c", true},
		{"/a/*/c", "/a/b/d/c", false},
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/bc", false},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.name); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}
