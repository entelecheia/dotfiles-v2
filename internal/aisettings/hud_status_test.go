package aisettings

import (
	"os"
	"path/filepath"
	"testing"
)

// TestClaudeSettingsStatus_MalformedJSONIsDriftNotError pins the read-only
// contract of the status path against the claudecfg conversion.
//
// claudecfg.Read fails hard on unparseable JSON so a writer never clobbers a
// file it cannot faithfully rewrite. The status path must not inherit that:
// before claudecfg existed, `dot ai hud status` reported the Claude target as
// out-of-sync with "settings json invalid" and still printed the Codex target.
// Propagating the error instead aborts the whole command and hides every
// other tool's status behind one malformed file.
//
// Deleting the errors.Is(err, claudecfg.ErrInvalidJSON) branch from
// claudeSettingsStatus must turn this test red.
func TestClaudeSettingsStatus_MalformedJSONIsDriftNotError(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(settings, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &HUDManager{HomeDir: home}
	inSync, reason, err := m.claudeSettingsStatus()
	if err != nil {
		t.Fatalf("claudeSettingsStatus() err = %v, want nil (malformed JSON is drift, not a hard error)", err)
	}
	if inSync {
		t.Error("claudeSettingsStatus() inSync = true, want false")
	}
	if reason != "settings json invalid" {
		t.Errorf("claudeSettingsStatus() reason = %q, want %q", reason, "settings json invalid")
	}
}

// TestClaudeSettingsStatus_ReadErrorStillPropagates is the other half of the
// pair: only the parse failure is softened. An unreadable file is a real
// error and must not be reported as ordinary drift, or a permission problem
// would masquerade as a fixable settings mismatch.
func TestClaudeSettingsStatus_ReadErrorStillPropagates(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the unreadable-file mode")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"statusLine":{}}`), 0o000); err != nil {
		t.Fatal(err)
	}

	m := &HUDManager{HomeDir: home}
	if _, _, err := m.claudeSettingsStatus(); err == nil {
		t.Fatal("claudeSettingsStatus() err = nil, want a read error for an unreadable settings file")
	}
}
