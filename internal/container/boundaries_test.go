package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every path the container module writes must stay under dot's data tree and
// be named in docs/BOUNDARIES.md.
func TestWritePathsAreInBoundaries(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "BOUNDARIES.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	home := "/home/u"
	root := filepath.Join(home, ".local", "share", "dotfiles") + string(filepath.Separator)
	for name, p := range map[string]string{"ShimPath": ShimPath(home), "StatePath": StatePath(home)} {
		if !strings.HasPrefix(p, root) {
			t.Errorf("%s = %q escapes %s", name, p, root)
		}
		if tilde := "~" + strings.TrimPrefix(p, home); !strings.Contains(doc, "`"+tilde+"`") {
			t.Errorf("%s (%s) is not named in docs/BOUNDARIES.md", name, tilde)
		}
	}
	for _, want := range []string{"`~/.config/shell/45-container.sh`", "`sh.brew.container`"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/BOUNDARIES.md does not name %s", want)
		}
	}
}
