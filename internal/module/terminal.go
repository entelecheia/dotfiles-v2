package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// TerminalModule manages terminal configuration files (starship, warp).
type TerminalModule struct{}

func (m *TerminalModule) Name() string { return "terminal" }

func (m *TerminalModule) managedFiles(rc *RunContext) []shellFile {
	files := []shellFile{
		{
			templatePath: "starship/starship.toml",
			destPath:     filepath.Join(rc.HomeDir, ".config", "starship.toml"),
			isTemplate:   false,
		},
	}

	cfg := rc.Config
	isDarwin := cfg.System != nil && cfg.System.OS == "darwin"
	if cfg.Modules.Terminal.Warp && isDarwin {
		files = append(files, shellFile{
			templatePath: "warp/dotfiles-v2.yaml",
			destPath:     filepath.Join(rc.HomeDir, ".warp", "themes", "dotfiles-v2.yaml"),
			isTemplate:   false,
		})
	}

	return files
}

func (m *TerminalModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	for _, f := range m.managedFiles(rc) {
		content, err := rc.Template.ReadStatic(f.templatePath)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f.templatePath, err)
		}
		if fileutil.NeedsUpdate(rc.Runner, f.destPath, content) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("write %s", f.destPath),
				Command:     fmt.Sprintf("copy %s -> %s", f.templatePath, f.destPath),
			})
		}
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *TerminalModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string

	for _, f := range m.managedFiles(rc) {
		content, err := rc.Template.ReadStatic(f.templatePath)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f.templatePath, err)
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
