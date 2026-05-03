package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// AIModule manages AI CLI/config helper shell configs and Claude settings.
type AIModule struct{}

func (m *AIModule) Name() string { return "ai" }

func (m *AIModule) managedFiles(rc *RunContext) []shellFile {
	return []shellFile{
		{
			templatePath: "shell/30-ai.sh.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".config", "shell", "30-ai.sh"),
			isTemplate:   true,
		},
		{
			templatePath: "claude/settings.json.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".config", "claude", "settings.json"),
			isTemplate:   true,
		},
	}
}

func (m *AIModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change
	data := rc.Config.TemplateData()

	for _, f := range m.managedFiles(rc) {
		content, err := rc.Template.Render(f.templatePath, data)
		if err != nil {
			return nil, fmt.Errorf("rendering %s: %w", f.templatePath, err)
		}
		if fileutil.NeedsUpdate(rc.Runner, f.destPath, content) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("write %s", f.destPath),
				Command:     fmt.Sprintf("render %s -> %s", f.templatePath, f.destPath),
			})
		}
	}
	legacy := filepath.Join(rc.HomeDir, ".config", "shell", "30-ai-tools.sh")
	if rc.Runner.FileExists(legacy) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("remove legacy %s", legacy),
			Command:     fmt.Sprintf("rm %s", legacy),
		})
	}
	if rc.Config.Modules.AI.AgentsSSOT {
		manager := aisettings.NewAgentsManager(rc.Runner, rc.HomeDir)
		statuses, err := manager.Status()
		if err != nil {
			return nil, fmt.Errorf("agents SSOT status: %w", err)
		}
		applySet := make(map[string]bool)
		for _, id := range manager.DefaultApplyTools() {
			applySet[id] = true
		}
		for _, st := range statuses {
			if !applySet[st.Tool.ID] {
				continue
			}
			if st.Drift == "in-sync" {
				continue
			}
			changes = append(changes, Change{
				Description: fmt.Sprintf("reapply agents SSOT to %s (%s)", st.Tool.ID, st.Drift),
				Command:     fmt.Sprintf("dotfiles ai agents apply --tool %s", st.Tool.ID),
			})
		}
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *AIModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string
	data := rc.Config.TemplateData()

	for _, f := range m.managedFiles(rc) {
		content, err := rc.Template.Render(f.templatePath, data)
		if err != nil {
			return nil, fmt.Errorf("rendering %s: %w", f.templatePath, err)
		}
		written, err := fileutil.EnsureFile(rc.Runner, f.destPath, content, 0644)
		if err != nil {
			return nil, fmt.Errorf("writing %s: %w", f.destPath, err)
		}
		if written {
			messages = append(messages, fmt.Sprintf("wrote %s", f.destPath))
		}
	}
	legacy := filepath.Join(rc.HomeDir, ".config", "shell", "30-ai-tools.sh")
	if rc.Runner.FileExists(legacy) {
		if err := rc.Runner.Remove(legacy); err != nil {
			return nil, fmt.Errorf("removing legacy %s: %w", legacy, err)
		}
		messages = append(messages, fmt.Sprintf("removed legacy %s", legacy))
	}
	if rc.Config.Modules.AI.AgentsSSOT {
		manager := aisettings.NewAgentsManager(rc.Runner, rc.HomeDir)
		result, err := manager.Apply(aisettings.ApplyOptions{Tools: manager.DefaultApplyTools(), Yes: rc.Yes})
		if err != nil {
			return nil, fmt.Errorf("applying agents SSOT: %w", err)
		}
		for _, item := range result.Items {
			if item.Changed {
				messages = append(messages, fmt.Sprintf("applied agents SSOT to %s", item.TargetPath))
			}
		}
		for _, warning := range result.Warnings {
			messages = append(messages, warning)
		}
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
