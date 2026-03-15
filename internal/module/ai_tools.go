package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// AIToolsModule manages AI tool shell configs and Claude settings.
type AIToolsModule struct{}

func (m *AIToolsModule) Name() string { return "ai-tools" }

func (m *AIToolsModule) managedFiles(rc *RunContext) []shellFile {
	return []shellFile{
		{
			templatePath: "shell/30-ai-tools.sh.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".config", "shell", "30-ai-tools.sh"),
			isTemplate:   true,
		},
		{
			templatePath: "claude/settings.json.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".config", "claude", "settings.json"),
			isTemplate:   true,
		},
	}
}

func (m *AIToolsModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
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

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *AIToolsModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
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

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
