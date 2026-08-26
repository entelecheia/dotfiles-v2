package syncer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCeilingsDoc(t *testing.T) {
	docPath := filepath.Join("..", "..", "docs", "CEILINGS.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	doc := string(data)

	// Pin paths and marker comments, not line numbers: unrelated edits above a
	// named site must not make this useful drift check noisy.
	requiredCeilings := []string{
		"## Process-wide manifest serialization",
		"## Guard hooks are not a sandbox",
		"## Peer first-contact trust on first use",
		"## Whole-file manifest writes",
		"## Preview cannot prove an openrsync transfer",
	}
	if len(requiredCeilings) != 5 {
		t.Fatalf("required ceiling count = %d, want five", len(requiredCeilings))
	}
	for _, heading := range requiredCeilings {
		if !strings.Contains(doc, heading) {
			t.Errorf("required ceiling %q missing from %s", heading, docPath)
		}
	}
	if !strings.Contains(doc, "## Newer state schema field shapes") {
		t.Error("phase-minted schema ceiling missing from docs/CEILINGS.md")
	}

	for _, path := range []string{
		"internal/syncer/manifest.go",
		"internal/cli/guard_cmd.go",
		"internal/guard/careful.go",
		"internal/syncer/rsyncbin.go",
		"internal/syncer/peer_commands.go",
		"internal/config/state.go",
	} {
		if !strings.Contains(doc, path) {
			t.Errorf("document does not name %q", path)
		}
		if _, err := os.Stat(filepath.Join("..", "..", path)); err != nil {
			t.Errorf("documented path %q is unavailable: %v", path, err)
		}
	}

	for _, want := range []struct {
		path   string
		marker string
	}{
		{"internal/syncer/manifest.go", "ponytail: known ceiling. See docs/CEILINGS.md (manifest serialization)."},
		{"internal/syncer/rsyncbin.go", "ponytail: known ceiling. See docs/CEILINGS.md (preview protocol limit)."},
		{"internal/syncer/rsyncbin.go", "ponytail: known ceiling. See docs/CEILINGS.md (first-contact trust)."},
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", want.path))
		if err != nil {
			t.Errorf("read marker file %q: %v", want.path, err)
			continue
		}
		if !strings.Contains(string(data), want.marker) {
			t.Errorf("documented marker %q missing from %s", want.marker, want.path)
		}
	}
}
