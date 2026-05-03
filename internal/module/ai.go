package module

import (
	"context"
	"fmt"
	"path/filepath"

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

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
