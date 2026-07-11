package guard

import (
	"encoding/json"
	"io"
)

// HookInput is the subset of the Claude Code PreToolUse hook stdin payload
// that guard consumes.
type HookInput struct {
	HookEventName string         `json:"hook_event_name"`
	ToolName      string         `json:"tool_name"`
	CWD           string         `json:"cwd"`
	ToolInput     map[string]any `json:"tool_input"`
}

// Decide dispatches one hook invocation. Anything guard has no opinion on
// (other events, other tools, parse gaps) returns the zero Decision.
func Decide(in HookInput, careful bool, freezeDir, homeDir string) Decision {
	if in.HookEventName != "PreToolUse" {
		return Decision{}
	}
	switch in.ToolName {
	case "Bash":
		if !careful {
			return Decision{}
		}
		command, _ := in.ToolInput["command"].(string)
		return CheckCommand(command)
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		if freezeDir == "" {
			return Decision{}
		}
		path, _ := in.ToolInput["file_path"].(string)
		if path == "" {
			path, _ = in.ToolInput["notebook_path"].(string)
		}
		return CheckPath(path, in.CWD, freezeDir, homeDir)
	}
	return Decision{}
}

// HookOutput renders the Claude Code PreToolUse decision JSON. A zero
// Decision renders as {} (no opinion; normal permission flow applies).
func HookOutput(d Decision) []byte {
	if d.Permission == "" {
		return []byte("{}\n")
	}
	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       d.Permission,
			"permissionDecisionReason": "[dot guard] " + d.Reason,
		},
	}
	data, err := json.Marshal(out)
	if err != nil {
		return []byte("{}\n")
	}
	return append(data, '\n')
}

// RunHook reads a PreToolUse payload from stdin and writes the decision
// JSON to stdout. Malformed input never hard-fails: hooks must fail open.
func RunHook(stdin io.Reader, stdout io.Writer, careful bool, freezeDir, homeDir string) Decision {
	var in HookInput
	d := Decision{}
	if err := json.NewDecoder(stdin).Decode(&in); err == nil {
		d = Decide(in, careful, freezeDir, homeDir)
	}
	_, _ = stdout.Write(HookOutput(d))
	return d
}
