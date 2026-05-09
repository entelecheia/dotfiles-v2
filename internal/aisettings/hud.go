package aisettings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	dotexec "github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

var defaultCodexStatusLine = []string{
	"model-with-reasoning",
	"git-branch",
	"context-remaining",
	"total-input-tokens",
	"total-output-tokens",
	"five-hour-limit",
	"weekly-limit",
}

const claudeHUDScript = `#!/usr/bin/env python3
import json
import os
import subprocess
import sys
import time


def pct(value):
    if value is None:
        return "n/a"
    try:
        return f"{int(round(float(value)))}%"
    except (TypeError, ValueError):
        return "n/a"


def duration(seconds):
    try:
        seconds = max(0, int(seconds))
    except (TypeError, ValueError):
        return "n/a"
    days, rem = divmod(seconds, 86400)
    hours, rem = divmod(rem, 3600)
    minutes = rem // 60
    if days:
        return f"{days}d{hours:02d}h"
    if hours:
        return f"{hours}h{minutes:02d}m"
    return f"{minutes}m"


def reset_in(limit):
    if not isinstance(limit, dict):
        return ""
    resets_at = limit.get("resets_at")
    if not resets_at:
        return ""
    return f"({duration(float(resets_at) - time.time())})"


def git_branch(cwd):
    if not cwd:
        return ""
    try:
        out = subprocess.check_output(
            ["git", "-C", cwd, "branch", "--show-current"],
            stderr=subprocess.DEVNULL,
            text=True,
            timeout=0.2,
        ).strip()
        return out
    except Exception:
        return ""


try:
    data = json.load(sys.stdin)
except Exception:
    data = {}

model = data.get("model", {})
name = model.get("display_name") or model.get("id") or "claude"
effort = data.get("effort", {}).get("level") or ""
cwd = data.get("workspace", {}).get("current_dir") or data.get("cwd") or ""
branch = git_branch(cwd)
ctx = data.get("context_window", {})
rate = data.get("rate_limits", {})
five = rate.get("five_hour", {})
week = rate.get("seven_day", {})
session_ms = data.get("cost", {}).get("total_duration_ms") or 0

parts = [name]
if effort:
    parts.append(effort)
if branch:
    parts.append(branch)
parts.append(f"ctx:{pct(ctx.get('used_percentage'))}")
parts.append(f"5h:{pct(five.get('used_percentage'))}{reset_in(five)}")
parts.append(f"wk:{pct(week.get('used_percentage'))}{reset_in(week)}")
parts.append(f"session:{duration(int(session_ms) / 1000)}")

print(" | ".join(parts))
`

// HUDManager manages dot-native status lines for Claude Code and Codex.
type HUDManager struct {
	Runner  *dotexec.Runner
	HomeDir string
}

// HUDOptions controls HUD Apply.
type HUDOptions struct {
	Tools  []string
	DryRun bool
}

// HUDResult summarizes HUD Apply.
type HUDResult struct {
	Items  []HUDItem
	DryRun bool
}

// HUDItem reports one managed HUD target.
type HUDItem struct {
	ToolID     string
	TargetPath string
	Drift      string
	Changed    bool
	Detail     string
}

// NewHUDManager returns a HUD manager rooted at homeDir.
func NewHUDManager(runner *dotexec.Runner, homeDir string) *HUDManager {
	return &HUDManager{Runner: runner, HomeDir: homeDir}
}

// Status reports whether selected HUD targets match dot-native defaults.
func (m *HUDManager) Status(tools []string) ([]HUDItem, error) {
	ids, err := resolveHUDToolIDs(tools)
	if err != nil {
		return nil, err
	}
	var out []HUDItem
	for _, id := range ids {
		switch id {
		case "codex":
			out = append(out, m.codexStatus())
		case "claude":
			item, err := m.claudeStatus()
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
	}
	return out, nil
}

// Apply writes selected HUD targets.
func (m *HUDManager) Apply(opts HUDOptions) (*HUDResult, error) {
	ids, err := resolveHUDToolIDs(opts.Tools)
	if err != nil {
		return nil, err
	}
	effectiveDryRun := opts.DryRun || m.runner().DryRun
	result := &HUDResult{DryRun: effectiveDryRun}
	for _, id := range ids {
		var item HUDItem
		switch id {
		case "codex":
			item, err = m.applyCodexHUD(effectiveDryRun)
		case "claude":
			item, err = m.applyClaudeHUD(effectiveDryRun)
		}
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func (m *HUDManager) codexStatus() HUDItem {
	path := m.codexConfigPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return HUDItem{ToolID: "codex", TargetPath: path, Drift: "missing", Detail: "config missing"}
	}
	if err != nil {
		return HUDItem{ToolID: "codex", TargetPath: path, Drift: "error", Detail: err.Error()}
	}
	next := patchCodexStatusLine(string(data))
	if next == string(data) {
		return HUDItem{ToolID: "codex", TargetPath: path, Drift: "in-sync"}
	}
	return HUDItem{ToolID: "codex", TargetPath: path, Drift: "out-of-sync", Detail: "tui.status_line differs"}
}

func (m *HUDManager) claudeStatus() (HUDItem, error) {
	settingsPath := m.claudeSettingsPath()
	scriptPath := m.claudeScriptPath()
	scriptOK := false
	if data, err := os.ReadFile(scriptPath); err == nil && string(data) == claudeHUDScript {
		scriptOK = true
	}
	settingsOK, detail, err := m.claudeSettingsStatus()
	if err != nil {
		return HUDItem{}, err
	}
	item := HUDItem{ToolID: "claude", TargetPath: settingsPath, Drift: "in-sync"}
	switch {
	case scriptOK && settingsOK:
		return item, nil
	case detail != "":
		item.Drift = "out-of-sync"
		item.Detail = detail
	default:
		item.Drift = "out-of-sync"
		item.Detail = "statusline script differs"
	}
	return item, nil
}

func (m *HUDManager) applyCodexHUD(dryRun bool) (HUDItem, error) {
	path := m.codexConfigPath()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return HUDItem{}, fmt.Errorf("read %s: %w", path, err)
	}
	next := patchCodexStatusLine(string(data))
	changed := string(data) != next
	item := HUDItem{ToolID: "codex", TargetPath: path, Changed: changed, Drift: "in-sync"}
	if changed {
		item.Drift = "out-of-sync"
		item.Detail = "write tui.status_line"
	}
	if changed && !dryRun {
		if _, err := fileutil.EnsureFile(m.runner(), path, []byte(next), 0o600); err != nil {
			return HUDItem{}, err
		}
	}
	return item, nil
}

func (m *HUDManager) applyClaudeHUD(dryRun bool) (HUDItem, error) {
	settingsPath := m.claudeSettingsPath()
	scriptPath := m.claudeScriptPath()
	settings, err := m.mergedClaudeSettings()
	if err != nil {
		return HUDItem{}, err
	}
	settingsChanged := true
	if current, err := os.ReadFile(settingsPath); err == nil {
		settingsChanged = !jsonBytesEqual(current, settings)
	} else if !os.IsNotExist(err) {
		return HUDItem{}, fmt.Errorf("read %s: %w", settingsPath, err)
	}
	scriptChanged := true
	if current, err := os.ReadFile(scriptPath); err == nil {
		scriptChanged = string(current) != claudeHUDScript
	} else if !os.IsNotExist(err) {
		return HUDItem{}, fmt.Errorf("read %s: %w", scriptPath, err)
	}
	changed := settingsChanged || scriptChanged
	item := HUDItem{ToolID: "claude", TargetPath: settingsPath, Changed: changed, Drift: "in-sync"}
	if changed {
		item.Drift = "out-of-sync"
		item.Detail = "write statusLine and statusline-dot.py"
	}
	if changed && !dryRun {
		if _, err := fileutil.EnsureFile(m.runner(), scriptPath, []byte(claudeHUDScript), 0o755); err != nil {
			return HUDItem{}, err
		}
		if err := os.Chmod(scriptPath, 0o755); err != nil {
			return HUDItem{}, fmt.Errorf("chmod %s: %w", scriptPath, err)
		}
		if _, err := fileutil.EnsureFile(m.runner(), settingsPath, settings, 0o644); err != nil {
			return HUDItem{}, err
		}
	}
	return item, nil
}

func (m *HUDManager) claudeSettingsStatus() (bool, string, error) {
	path := m.claudeSettingsPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, "settings missing", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("read %s: %w", path, err)
	}
	var settings map[string]any
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return false, "settings json invalid", nil
		}
	}
	if settings == nil {
		settings = map[string]any{}
	}
	sl, ok := settings["statusLine"].(map[string]any)
	if !ok {
		return false, "statusLine missing", nil
	}
	if sl["type"] != "command" || sl["command"] != "~/.claude/statusline-dot.py" {
		return false, "statusLine command differs", nil
	}
	return true, "", nil
}

func (m *HUDManager) mergedClaudeSettings() ([]byte, error) {
	path := m.claudeSettingsPath()
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	settings["statusLine"] = map[string]any{
		"type":            "command",
		"command":         "~/.claude/statusline-dot.py",
		"refreshInterval": float64(5),
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func patchCodexStatusLine(content string) string {
	desired := codexStatusLineTOML()
	content = strings.ReplaceAll(content, "\r\n", "\n")
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return "[tui]\n" + desired + "\n"
	}
	lines := strings.Split(trimmed, "\n")
	start, end := findTOMLTable(lines, "tui")
	if start < 0 {
		return trimmed + "\n\n[tui]\n" + desired + "\n"
	}
	statusStart, statusEnd := findTOMLKey(lines, start+1, end, "status_line")
	if statusStart >= 0 {
		next := append([]string{}, lines[:statusStart]...)
		next = append(next, desired)
		next = append(next, lines[statusEnd:]...)
		return strings.Join(next, "\n") + "\n"
	}
	next := append([]string{}, lines[:end]...)
	next = append(next, desired)
	next = append(next, lines[end:]...)
	return strings.Join(next, "\n") + "\n"
}

func codexStatusLineTOML() string {
	quoted := make([]string, 0, len(defaultCodexStatusLine))
	for _, part := range defaultCodexStatusLine {
		quoted = append(quoted, fmt.Sprintf("%q", part))
	}
	return "status_line = [" + strings.Join(quoted, ", ") + "]"
}

func findTOMLTable(lines []string, name string) (int, int) {
	start := -1
	tablePattern := regexp.MustCompile(`^\s*\[` + regexp.QuoteMeta(name) + `\]\s*(#.*)?$`)
	anyTablePattern := regexp.MustCompile(`^\s*\[[^\]]+\]\s*(#.*)?$`)
	for i, line := range lines {
		if tablePattern.MatchString(line) {
			start = i
			break
		}
	}
	if start < 0 {
		return -1, -1
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if anyTablePattern.MatchString(lines[i]) {
			end = i
			break
		}
	}
	return start, end
}

func findTOMLKey(lines []string, start, end int, key string) (int, int) {
	keyPattern := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)
	for i := start; i < end; i++ {
		if !keyPattern.MatchString(lines[i]) {
			continue
		}
		keyEnd := i + 1
		balance := strings.Count(lines[i], "[") - strings.Count(lines[i], "]")
		for keyEnd < end && balance > 0 {
			balance += strings.Count(lines[keyEnd], "[") - strings.Count(lines[keyEnd], "]")
			keyEnd++
		}
		return i, keyEnd
	}
	return -1, -1
}

func jsonBytesEqual(a, b []byte) bool {
	var am, bm any
	if json.Unmarshal(a, &am) != nil || json.Unmarshal(b, &bm) != nil {
		return bytes.Equal(a, b)
	}
	aj, _ := json.Marshal(am)
	bj, _ := json.Marshal(bm)
	return bytes.Equal(aj, bj)
}

func resolveHUDToolIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return []string{"claude", "codex"}, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || seen[id] {
			continue
		}
		if id != "claude" && id != "codex" {
			return nil, fmt.Errorf("unknown HUD tool %q", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func (m *HUDManager) runner() *dotexec.Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return dotexec.NewRunner(false, slog.Default())
}

func (m *HUDManager) homeDir() string {
	if m.HomeDir != "" {
		return m.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

func (m *HUDManager) codexConfigPath() string {
	return filepath.Join(m.homeDir(), ".codex", "config.toml")
}

func (m *HUDManager) claudeSettingsPath() string {
	return filepath.Join(m.homeDir(), ".claude", "settings.json")
}

func (m *HUDManager) claudeScriptPath() string {
	return filepath.Join(m.homeDir(), ".claude", "statusline-dot.py")
}
