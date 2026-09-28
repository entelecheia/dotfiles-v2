package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

// The preview prints each no-match class with its suggestion, and a rescue
// move with its branch (#178).
func TestPrintPeerGitRepos_ClassesAndRescue(t *testing.T) {
	res := &syncer.GitStateResult{Root: "/w", Repos: []*syncer.GitRepoReport{
		{Path: "vault", Status: syncer.GitRepoNoMatch, Class: syncer.GitClassAtTip, Suggestion: "at origin/main; only uncommitted changes differ"},
		{Path: "sites/x", Status: syncer.GitRepoRealignable, Head: "aaaa", Target: "bbbb", Rescue: "rescue/260928-main"},
	}}
	var out bytes.Buffer
	printPeerGitRepos(&Printer{Out: &out}, res, true)
	for _, want := range []string{"at-tip: at origin/main; only uncommitted changes differ", "rescue: rescue/260928-main keeps aaaa"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
