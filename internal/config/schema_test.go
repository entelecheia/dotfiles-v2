package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns
// everything written to it. The state loaders warn straight to os.Stderr and
// take no injectable writer, so the pipe is the only way to assert on the
// wording the differential harness compares byte for byte.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	os.Stderr = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func writeStateFile(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSaveState_StampsSchemaVersion pins DEBT-01's whole claim: a state that
// never touched the field is still written with a real version key, and that
// key is first in the file. Without this a file written by v1.0 would be
// indistinguishable from one written before the field existed.
func TestSaveState_StampsSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := saveStateAt(path, &UserState{Name: "Test", Profile: "full"}); err != nil {
		t.Fatalf("saveStateAt: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "schema_version:") {
		t.Fatalf("schema_version is not the first key:\n%s", data)
	}
	if strings.Contains(string(data), "schema_version: 0") {
		t.Fatalf("an unstamped save wrote version 0:\n%s", data)
	}
	loaded, err := loadStateAt(path)
	if err != nil {
		t.Fatalf("loadStateAt: %v", err)
	}
	if loaded.SchemaVersion != currentSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", loaded.SchemaVersion, currentSchemaVersion)
	}
}

// TestSaveState_StampsOverAnUnversionedFile proves the stamp is applied to a
// state loaded from a pre-version file, not only to a fresh struct.
func TestSaveState_StampsOverAnUnversionedFile(t *testing.T) {
	path := writeStateFile(t, t.TempDir(), "name: Test\nprofile: full\n")
	loaded, err := loadStateAt(path)
	if err != nil {
		t.Fatalf("loadStateAt: %v", err)
	}
	if loaded.SchemaVersion != 0 {
		t.Fatalf("unversioned file loaded as version %d, want 0", loaded.SchemaVersion)
	}
	if err := saveStateAt(path, loaded); err != nil {
		t.Fatalf("saveStateAt: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "schema_version: 1\n") {
		t.Fatalf("rewrite did not stamp the current version:\n%s", data)
	}
}

// TestLoadState_UnversionedFileIsSilent pins version 0 as a normal state:
// "written before the field existed", not an error and not a warning.
func TestLoadState_UnversionedFileIsSilent(t *testing.T) {
	path := writeStateFile(t, t.TempDir(), "name: Test\nprofile: full\n")
	var loaded *UserState
	var err error
	out := captureStderr(t, func() { loaded, err = loadStateAt(path) })
	if err != nil {
		t.Fatalf("loadStateAt: %v", err)
	}
	if loaded.SchemaVersion != 0 {
		t.Fatalf("SchemaVersion = %d, want 0", loaded.SchemaVersion)
	}
	if out != "" {
		t.Fatalf("unversioned load wrote to stderr: %q", out)
	}
}

// TestLoadState_ForwardVersionWarnsAndStillWorks is DEBT-02's read half on the
// loadStateAt entry point: an additively-forward file (every field this binary
// knows still decodes) warns and returns state the caller can use.
func TestLoadState_ForwardVersionWarnsAndStillWorks(t *testing.T) {
	path := writeStateFile(t, t.TempDir(),
		"schema_version: 99\nname: Test\nprofile: full\nunknown_future_key: whatever\n")
	var loaded *UserState
	var err error
	out := captureStderr(t, func() { loaded, err = loadStateAt(path) })
	if err != nil {
		t.Fatalf("loadStateAt: %v", err)
	}
	if loaded.Name != "Test" || loaded.Profile != "full" {
		t.Fatalf("forward-version state is not usable: %#v", loaded)
	}
	if !strings.Contains(out, "warning:") {
		t.Fatalf("no warning for a forward-version file: %q", out)
	}
	if !strings.Contains(out, "99") || !strings.Contains(out, "dot update") {
		t.Fatalf("warning names neither the version nor the remedy: %q", out)
	}
}

// TestLoadState_IncompatiblyForwardWarnsBeforeTheDecodeError covers the case a
// struct field alone cannot: the full decode FAILS, so the field that would
// carry the version is never populated. The peek still recovers it, and the
// warning must be emitted before the decode error is returned so the user
// learns the file came from a newer dot instead of getting a bare yaml error.
func TestLoadState_IncompatiblyForwardWarnsBeforeTheDecodeError(t *testing.T) {
	// profile: arrives as a mapping where UserState.Profile is a string.
	path := writeStateFile(t, t.TempDir(),
		"schema_version: 99\nname: Test\nprofile:\n  name: full\n  variant: server\n")
	var loaded *UserState
	var err error
	out := captureStderr(t, func() { loaded, err = loadStateAt(path) })
	if err == nil {
		t.Fatalf("expected a decode error, got state %#v", loaded)
	}
	if !strings.HasPrefix(err.Error(), "parsing state: ") {
		t.Fatalf("decode error wording changed: %v", err)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "99") {
		t.Fatalf("no forward-version warning before the decode error: %q", out)
	}
	if !strings.Contains(out, "dot update") {
		t.Fatalf("warning does not point at the remedy: %q", out)
	}
}

// TestLoadStateFrom_ForwardVersionWarnsAndStillWorks is the same claim on the
// entry point D-03's original wording missed. dot init --from, onestop and
// profilesnap all read through here.
func TestLoadStateFrom_ForwardVersionWarnsAndStillWorks(t *testing.T) {
	path := writeStateFile(t, t.TempDir(),
		"schema_version: 99\nname: Test\nprofile: full\n")
	var loaded *UserState
	var err error
	out := captureStderr(t, func() { loaded, err = LoadStateFrom(path) })
	if err != nil {
		t.Fatalf("LoadStateFrom: %v", err)
	}
	if loaded.Name != "Test" || loaded.Profile != "full" {
		t.Fatalf("forward-version import is not usable: %#v", loaded)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "99") || !strings.Contains(out, "dot update") {
		t.Fatalf("LoadStateFrom did not warn about a forward-version file: %q", out)
	}
}

// TestLoadStateFrom_KeepsItsThreeBehaviours pins what the merge into a shared
// read body must not change: a missing file is a hard error here, the two
// error wordings say "config" and not "state", and an import with neither a
// name nor a profile is refused.
func TestLoadStateFrom_KeepsItsThreeBehaviours(t *testing.T) {
	dir := t.TempDir()

	_, err := LoadStateFrom(filepath.Join(dir, "absent.yaml"))
	if err == nil {
		t.Fatal("missing file must be an error for LoadStateFrom")
	}
	if !strings.HasPrefix(err.Error(), "reading config: ") {
		t.Fatalf("read error wording = %q, want prefix %q", err.Error(), "reading config: ")
	}

	broken := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(broken, []byte("{{ broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadStateFrom(broken)
	if err == nil {
		t.Fatal("unparseable import must be an error")
	}
	if !strings.HasPrefix(err.Error(), "parsing config: ") {
		t.Fatalf("parse error wording = %q, want prefix %q", err.Error(), "parsing config: ")
	}

	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, []byte("email: nobody@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadStateFrom(empty)
	if err == nil || err.Error() != "imported config is empty (no name or profile set)" {
		t.Fatalf("emptiness assertion changed: %v", err)
	}
}

// TestLoadStateFrom_ValidationWarningWording pins the one-line form. loadStateAt
// prints a second remedy line and LoadStateFrom does not; the differential
// harness compares stderr byte for byte, so the difference is load-bearing.
func TestLoadStateFrom_ValidationWarningWording(t *testing.T) {
	path := writeStateFile(t, t.TempDir(), "name: Test\nprofile: not-a-profile\n")
	out := captureStderr(t, func() { _, _ = LoadStateFrom(path) })
	if !strings.HasPrefix(out, "warning: imported config has invalid values: ") {
		t.Fatalf("import warning wording changed: %q", out)
	}
	if strings.Contains(out, "dot reconfigure") {
		t.Fatalf("LoadStateFrom must not print loadStateAt's remedy line: %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("import warning must be one line, got %q", out)
	}
}

// TestLoadState_ValidationWarningWording pins loadStateAt's two-line form.
func TestLoadState_ValidationWarningWording(t *testing.T) {
	path := writeStateFile(t, t.TempDir(), "name: Test\nprofile: not-a-profile\n")
	out := captureStderr(t, func() { _, _ = loadStateAt(path) })
	if !strings.HasPrefix(out, "warning: state file has invalid values: ") {
		t.Fatalf("state warning wording changed: %q", out)
	}
	if !strings.HasSuffix(out, "  Run 'dot reconfigure' to fix.\n") {
		t.Fatalf("state warning lost its remedy line: %q", out)
	}
}

// TestLoadState_MissingFileIsEmptyState pins loadStateAt's own fork, the one
// LoadStateFrom must NOT inherit from the shared body.
func TestLoadState_MissingFileIsEmptyState(t *testing.T) {
	state, err := loadStateAt(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("missing state file must not error: %v", err)
	}
	if state == nil || state.Name != "" || state.Profile != "" || state.SchemaVersion != 0 {
		t.Fatalf("missing state file = %#v, want the zero state", state)
	}
}

// TestPeekSchemaVersion_SwallowsItsOwnErrors: every malformed input peeks as 0
// so the real decode error surfaces instead of a complaint about a one-field
// struct. Measured in 07-RESEARCH.md Q3b.
func TestPeekSchemaVersion_SwallowsItsOwnErrors(t *testing.T) {
	cases := map[string]string{
		"sequence document": "- a\n- b\n",
		"scalar document":   "just a scalar\n",
		"empty document":    "",
		"broken document":   "{{ broken\n",
		"non-integer":       "schema_version: notanint\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := peekSchemaVersion([]byte(doc)); got != 0 {
				t.Fatalf("peek(%q) = %d, want 0", doc, got)
			}
		})
	}
}

// TestPeekSchemaVersion_TopLevelOnly rules out the two implementations a future
// reader might reach for. A line scanner or a regex over the raw bytes gets the
// nested case wrong.
func TestPeekSchemaVersion_TopLevelOnly(t *testing.T) {
	nested := "name: Test\nmodules:\n  schema_version: 42\n"
	if got := peekSchemaVersion([]byte(nested)); got != 0 {
		t.Fatalf("nested schema_version peeked as %d, want 0", got)
	}
	anchored := "defaults: &d\n  name: Test\nschema_version: 5\nmodules:\n  <<: *d\n"
	if got := peekSchemaVersion([]byte(anchored)); got != 5 {
		t.Fatalf("anchored document peeked as %d, want 5", got)
	}
}
