package guard

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecideDispatch(t *testing.T) {
	boundary := t.TempDir()

	cases := []struct {
		name      string
		in        HookInput
		careful   bool
		freezeDir string
		wantPerm  string
	}{
		{
			name:     "careful warns on destructive bash",
			in:       HookInput{HookEventName: "PreToolUse", ToolName: "Bash", ToolInput: map[string]any{"command": "rm -rf /etc"}},
			careful:  true,
			wantPerm: "ask",
		},
		{
			name:     "careful off allows destructive bash",
			in:       HookInput{HookEventName: "PreToolUse", ToolName: "Bash", ToolInput: map[string]any{"command": "rm -rf /etc"}},
			careful:  false,
			wantPerm: "",
		},
		{
			name:      "freeze denies edit outside boundary",
			in:        HookInput{HookEventName: "PreToolUse", ToolName: "Edit", ToolInput: map[string]any{"file_path": "/etc/hosts"}},
			freezeDir: boundary,
			wantPerm:  "deny",
		},
		{
			name:      "freeze allows edit inside boundary",
			in:        HookInput{HookEventName: "PreToolUse", ToolName: "Write", ToolInput: map[string]any{"file_path": filepath.Join(boundary, "a.go")}},
			freezeDir: boundary,
			wantPerm:  "",
		},
		{
			name:      "notebook path is checked",
			in:        HookInput{HookEventName: "PreToolUse", ToolName: "NotebookEdit", ToolInput: map[string]any{"notebook_path": "/etc/nb.ipynb"}},
			freezeDir: boundary,
			wantPerm:  "deny",
		},
		{
			name:     "other tools are ignored",
			in:       HookInput{HookEventName: "PreToolUse", ToolName: "Read", ToolInput: map[string]any{"file_path": "/etc/hosts"}},
			careful:  true,
			wantPerm: "",
		},
		{
			name:     "other events are ignored",
			in:       HookInput{HookEventName: "PostToolUse", ToolName: "Bash", ToolInput: map[string]any{"command": "rm -rf /etc"}},
			careful:  true,
			wantPerm: "",
		},
		{
			name:     "missing tool_input fails open",
			in:       HookInput{HookEventName: "PreToolUse", ToolName: "Bash"},
			careful:  true,
			wantPerm: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.in, tc.careful, tc.freezeDir, "")
			if d.Permission != tc.wantPerm {
				t.Fatalf("Decide() permission = %q, want %q (%+v)", d.Permission, tc.wantPerm, d)
			}
		})
	}
}

func TestHookOutput(t *testing.T) {
	if got := string(HookOutput(Decision{})); got != "{}\n" {
		t.Fatalf("zero decision output = %q, want {}", got)
	}

	out := HookOutput(Decision{Permission: "ask", Reason: "Destructive: something.", Pattern: "x"})
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	hso, ok := parsed["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("missing hookSpecificOutput: %s", out)
	}
	if hso["hookEventName"] != "PreToolUse" || hso["permissionDecision"] != "ask" {
		t.Fatalf("unexpected fields: %s", out)
	}
	reason, _ := hso["permissionDecisionReason"].(string)
	if !strings.HasPrefix(reason, "[dot guard] ") {
		t.Fatalf("reason missing prefix: %q", reason)
	}
}

func TestRunHook(t *testing.T) {
	t.Run("full round trip ask", func(t *testing.T) {
		in := `{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"/","tool_input":{"command":"git push --force"}}`
		var out bytes.Buffer
		d := RunHook(strings.NewReader(in), &out, true, "", "")
		if d.Permission != "ask" {
			t.Fatalf("decision = %+v, want ask", d)
		}
		if !strings.Contains(out.String(), `"permissionDecision":"ask"`) {
			t.Fatalf("stdout = %s", out.String())
		}
	})
	t.Run("malformed stdin fails open", func(t *testing.T) {
		var out bytes.Buffer
		d := RunHook(strings.NewReader("not json"), &out, true, "/some/dir", "")
		if d.Permission != "" || out.String() != "{}\n" {
			t.Fatalf("malformed input must allow: d=%+v out=%q", d, out.String())
		}
	})
	t.Run("empty stdin fails open", func(t *testing.T) {
		var out bytes.Buffer
		RunHook(strings.NewReader(""), &out, true, "", "")
		if out.String() != "{}\n" {
			t.Fatalf("empty input must allow: %q", out.String())
		}
	})
}
