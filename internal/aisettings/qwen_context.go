package aisettings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// ensureQwenContext adds AGENTS.md without replacing existing context discovery.
func (m *AgentsManager) ensureQwenContext(dry bool) error {
	path := filepath.Join(m.homeDir(), ".qwen", "settings.json")
	original, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	settings := map[string]any{}
	if len(original) > 0 {
		if err := json.Unmarshal(original, &settings); err != nil {
			return fmt.Errorf("read Qwen settings: %w", err)
		}
	}
	if settings == nil {
		return fmt.Errorf("Qwen settings must be an object")
	}
	context := map[string]any{}
	if v, exists := settings["context"]; exists {
		var ok bool
		context, ok = v.(map[string]any)
		if !ok || context == nil {
			return fmt.Errorf("Qwen context must be an object; preserving settings")
		}
	}
	var names []string
	switch v := context["fileName"].(type) {
	case nil:
		names = []string{"QWEN.md"}
	case string:
		names = []string{v}
	case []any:
		for _, name := range v {
			text, ok := name.(string)
			if !ok {
				return fmt.Errorf("Qwen context.fileName must contain strings")
			}
			names = append(names, text)
		}
	default:
		return fmt.Errorf("unsupported Qwen context.fileName; preserving settings")
	}
	if slices.Contains(names, "AGENTS.md") {
		return nil
	}
	names = append(names, "AGENTS.md")
	context["fileName"] = names
	settings["context"] = context
	if dry {
		return nil
	}
	if len(original) > 0 {
		if _, err := m.backupTarget("qwen-settings", path); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := m.runner().MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return m.runner().WriteFile(path, append(data, '\n'), 0600)
}

func (m *AgentsManager) qwenContextReady() (bool, error) {
	path := filepath.Join(m.homeDir(), ".qwen", "settings.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var settings struct {
		Context struct {
			FileName any `json:"fileName"`
		} `json:"context"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, err
	}
	switch v := settings.Context.FileName.(type) {
	case string:
		return v == "AGENTS.md", nil
	case []any:
		for _, name := range v {
			if name == "AGENTS.md" {
				return true, nil
			}
		}
	}
	return false, nil
}
