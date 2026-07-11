package guard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// HookMarker tags every settings.json hook entry dot guard installs. Hook
// commands run via `sh -c`, so a trailing comment is inert and makes
// removal independent of where the dot binary lives.
const HookMarker = "# dot-guard"

// hookTimeoutSeconds caps hook runtime so a wedged binary cannot stall
// every tool call (Claude Code's default is 60s).
const hookTimeoutSeconds = 10

// guardMatchers are the PreToolUse matchers guard registers: careful
// inspects Bash commands, freeze inspects file mutations.
var guardMatchers = []string{"Bash", "Edit|Write|MultiEdit|NotebookEdit"}

// HookCommand composes the settings.json command string for a dot binary.
// The path is single-quoted so homes containing spaces survive `sh -c`.
func HookCommand(dotPath string) string {
	return fmt.Sprintf("'%s' guard hook %s", dotPath, HookMarker)
}

// HookBinary extracts the binary path from a registered hook command
// (inverse of HookCommand; tolerates unquoted legacy commands).
func HookBinary(command string) string {
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, "'") {
		if end := strings.Index(command[1:], "'"); end >= 0 {
			return command[1 : 1+end]
		}
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// ClaudeSettingsPath returns ~/.claude/settings.json for the given home.
func ClaudeSettingsPath(homeDir string) string {
	return filepath.Join(homeDir, ".claude", "settings.json")
}

// EnsureHookEntries registers guard's PreToolUse hook entries in
// ~/.claude/settings.json, replacing any existing marker-tagged entries.
// Entries owned by other tools are preserved untouched. Returns whether the
// file changed.
func EnsureHookEntries(runner *exec.Runner, homeDir, hookCommand string) (bool, error) {
	path := ClaudeSettingsPath(homeDir)
	settings, err := readSettings(path)
	if err != nil {
		return false, err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	pre, _ := hooks["PreToolUse"].([]any)
	kept, _ := stripGuardHooks(pre)
	for _, matcher := range guardMatchers {
		kept = append(kept, map[string]any{
			"matcher": matcher,
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": hookCommand,
				"timeout": hookTimeoutSeconds,
			}},
		})
	}
	hooks["PreToolUse"] = kept
	settings["hooks"] = hooks
	return writeSettings(runner, path, settings)
}

// RemoveHookEntries deletes guard's marker-tagged hook entries. Empty
// containers left behind by the removal are dropped so a file that only
// ever held guard entries returns to its prior shape.
func RemoveHookEntries(runner *exec.Runner, homeDir string) (int, error) {
	path := ClaudeSettingsPath(homeDir)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return 0, nil
	}
	settings, err := readSettings(path)
	if err != nil {
		return 0, err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	kept, removed := stripGuardHooks(pre)
	if removed == 0 {
		return 0, nil
	}
	if len(kept) == 0 {
		delete(hooks, "PreToolUse")
	} else {
		hooks["PreToolUse"] = kept
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	if _, err := writeSettings(runner, path, settings); err != nil {
		return 0, err
	}
	return removed, nil
}

// InspectHookEntries returns the marker-tagged hook commands currently
// registered (read-only; missing file means none).
func InspectHookEntries(homeDir string) ([]string, error) {
	path := ClaudeSettingsPath(homeDir)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	settings, err := readSettings(path)
	if err != nil {
		return nil, err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	var commands []string
	for _, raw := range pre {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entryHooks, _ := entry["hooks"].([]any)
		for _, h := range entryHooks {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if command, _ := hm["command"].(string); strings.Contains(command, HookMarker) {
				commands = append(commands, command)
			}
		}
	}
	return commands, nil
}

// stripGuardHooks removes marker-tagged hooks from a PreToolUse entry list.
// An entry whose hooks were all ours is dropped entirely; entries with any
// foreign hook survive with only our hook removed.
func stripGuardHooks(pre []any) ([]any, int) {
	var kept []any
	removed := 0
	for _, raw := range pre {
		entry, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		entryHooks, _ := entry["hooks"].([]any)
		var keptHooks []any
		for _, h := range entryHooks {
			hm, ok := h.(map[string]any)
			if ok {
				if command, _ := hm["command"].(string); strings.Contains(command, HookMarker) {
					removed++
					continue
				}
			}
			keptHooks = append(keptHooks, h)
		}
		if len(keptHooks) == 0 && len(entryHooks) > 0 {
			continue // every hook in this entry was ours; drop the entry
		}
		if len(entryHooks) > 0 {
			entry["hooks"] = keptHooks
		}
		kept = append(kept, entry)
	}
	return kept, removed
}

// readSettings parses settings.json into a generic map. A missing or empty
// file yields an empty map; invalid JSON is a hard error so guard never
// clobbers a file it cannot faithfully rewrite.
func readSettings(path string) (map[string]any, error) {
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return settings, nil
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w (fix the JSON manually; dot will not overwrite it)", path, err)
	}
	return settings, nil
}

// writeSettings persists via fileutil.EnsureFile (hash-skip when unchanged,
// backup-before-overwrite, dry-run aware through the runner).
func writeSettings(runner *exec.Runner, path string, settings map[string]any) (bool, error) {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	return fileutil.EnsureFile(runner, path, append(data, '\n'), 0o644)
}
