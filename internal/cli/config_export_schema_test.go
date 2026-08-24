package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedForwardState writes a state file claiming a schema version this binary
// does not know, carrying a key this binary's UserState has no field for.
// yaml.v3 drops that key on load, which is the whole point: after the load the
// in-memory state is v1 data wearing a v2 label.
func seedForwardState(t *testing.T) (home, statePath string) {
	t.Helper()
	home = t.TempDir()
	statePath = filepath.Join(home, ".config", "dotfiles", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "schema_version: 99\nname: Test\nemail: t@example.com\nprofile: full\n" +
		"a_key_this_binary_does_not_know: matters\n"
	if err := os.WriteFile(statePath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, statePath
}

// TestConfigExport_DoesNotForgeAForwardVersion is the first half of the defect
// Codex found on PR #88. `dot config export` marshals the loaded UserState
// directly, so before the fix it wrote `schema_version: 99` into a document
// whose version-99 payload yaml.v3 had already discarded. A later v99 binary
// would read that file, trust the label, and find the data gone.
func TestConfigExport_DoesNotForgeAForwardVersion(t *testing.T) {
	home, _ := seedForwardState(t)
	dest := filepath.Join(t.TempDir(), "exported.yaml")

	_, _, err := runDotForTest("--home", home, "config", "export", dest)
	if err == nil {
		data, readErr := os.ReadFile(dest)
		if readErr != nil {
			t.Fatalf("export reported success but wrote nothing: %v", readErr)
		}
		if strings.Contains(string(data), "schema_version: 99") {
			t.Fatalf("export forged a version whose payload was dropped on load:\n%s", data)
		}
		if strings.Contains(string(data), "a_key_this_binary_does_not_know") {
			t.Fatalf("unexpected: the unknown key survived, so this test proves nothing:\n%s", data)
		}
		return
	}
	if !strings.Contains(err.Error(), "99") || !strings.Contains(err.Error(), "dot update") {
		t.Fatalf("refusal must name the version and the remedy, got: %v", err)
	}
}

// TestConfigExport_DoesNotBypassTheDowngradeRefusal is the second half. Export
// takes an arbitrary destination and writes it with os.WriteFile, so pointing
// it at a newer state file overwrote it without ever reaching saveStateAt's
// refusal.
func TestConfigExport_DoesNotBypassTheDowngradeRefusal(t *testing.T) {
	home, statePath := seedForwardState(t)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	_, _, _ = runDotForTest("--home", home, "config", "export", statePath)

	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("export destroyed the state file: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("export overwrote a newer state file, bypassing the refusal:\nbefore:\n%s\nafter:\n%s",
			before, after)
	}
}
