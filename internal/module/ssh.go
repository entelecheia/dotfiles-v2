package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// SSHModule manages SSH configuration files.
type SSHModule struct{}

func (m *SSHModule) Name() string { return "ssh" }

func (m *SSHModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	configDest := filepath.Join(rc.HomeDir, ".ssh", "config")
	configDDir := filepath.Join(rc.HomeDir, ".ssh", "config.d")

	configContent, err := rc.Template.Render("ssh/config.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering ssh/config.tmpl: %w", err)
	}
	if fileutil.NeedsUpdate(rc.Runner, configDest, configContent) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("write %s", configDest),
			Command:     "render ssh/config.tmpl -> ~/.ssh/config",
		})
	}

	if !rc.Runner.IsDir(configDDir) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("create directory %s", configDDir),
			Command:     fmt.Sprintf("mkdir -p %s", configDDir),
		})
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *SSHModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string

	configDest := filepath.Join(rc.HomeDir, ".ssh", "config")
	configDDir := filepath.Join(rc.HomeDir, ".ssh", "config.d")

	configContent, err := rc.Template.Render("ssh/config.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering ssh/config.tmpl: %w", err)
	}
	written, err := fileutil.EnsureFile(rc.Runner, configDest, configContent, 0600)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", configDest, err)
	}
	if written {
		messages = append(messages, fmt.Sprintf("wrote %s", configDest))
	}

	if !rc.Runner.IsDir(configDDir) {
		if err := rc.Runner.MkdirAll(configDDir, 0700); err != nil {
			return nil, fmt.Errorf("creating %s: %w", configDDir, err)
		}
		messages = append(messages, fmt.Sprintf("created %s", configDDir))
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
