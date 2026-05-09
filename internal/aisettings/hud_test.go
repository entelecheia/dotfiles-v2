package aisettings

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func TestPatchCodexStatusLineAddsTUI(t *testing.T) {
	got := patchCodexStatusLine("model = \"gpt\"\n")
	if !strings.Contains(got, "[tui]\nstatus_line = [") {
		t.Fatalf("missing tui status_line:\n%s", got)
	}
	if !strings.Contains(got, `"weekly-limit"`) {
		t.Fatalf("missing weekly limit segment:\n%s", got)
	}
}

func TestPatchCodexStatusLineReplacesExisting(t *testing.T) {
	got := patchCodexStatusLine("[tui]\nstatus_line = [\"old\"]\nnotification_condition = \"always\"\n")
	if strings.Contains(got, `"old"`) {
		t.Fatalf("old status_line remained:\n%s", got)
	}
	if !strings.Contains(got, "notification_condition = \"always\"") {
		t.Fatalf("unrelated tui key removed:\n%s", got)
	}
}

func TestHUDApplyPreservesClaudeSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := filepath.Join(home, ".claude", "settings.json")
	mustWrite(t, settings, []byte(`{"model":"opus","enabledPlugins":{"x":true}}`+"\n"))
	mgr := NewHUDManager(exec.NewRunner(false, slog.Default()), home)

	res, err := mgr.Apply(HUDOptions{Tools: []string{"claude"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Items) != 1 || !res.Items[0].Changed {
		t.Fatalf("expected changed claude item, got %#v", res.Items)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{`"model": "opus"`, `"enabledPlugins"`, `"statusLine"`, "statusline-dot.py"} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings missing %q:\n%s", want, body)
		}
	}
	script, err := os.Stat(filepath.Join(home, ".claude", "statusline-dot.py"))
	if err != nil {
		t.Fatal(err)
	}
	if script.Mode().Perm()&0o111 == 0 {
		t.Fatalf("statusline script is not executable: %v", script.Mode())
	}
}
