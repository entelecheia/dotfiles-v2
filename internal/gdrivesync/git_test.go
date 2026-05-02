package gdrivesync

import "testing"

func TestParseGitLsFiles(t *testing.T) {
	got := parseGitLsFiles([]byte("README.md\x00sub/thing.txt\x00\x00"))
	for _, rel := range []string{"README.md", "sub/thing.txt"} {
		if !got[rel] {
			t.Errorf("missing tracked path %q in %v", rel, got)
		}
	}
	if got[""] {
		t.Errorf("empty path should not be tracked: %v", got)
	}
}
