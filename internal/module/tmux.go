package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// TmuxModule manages tmux configuration.
type TmuxModule struct{}

func (m *TmuxModule) Name() string { return "tmux" }

func (m *TmuxModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	dest := filepath.Join(rc.HomeDir, ".tmux.conf")
	content, err := rc.Template.Render("tmux/tmux.conf.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering tmux/tmux.conf.tmpl: %w", err)
	}
	if fileutil.NeedsUpdate(rc.Runner, dest, content) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("write %s", dest),
			Command:     "render tmux/tmux.conf.tmpl -> ~/.tmux.conf",
		})
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *TmuxModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string

	dest := filepath.Join(rc.HomeDir, ".tmux.conf")
	content, err := rc.Template.Render("tmux/tmux.conf.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering tmux/tmux.conf.tmpl: %w", err)
	}
	written, err := fileutil.EnsureFile(rc.Runner, dest, content, 0644)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", dest, err)
	}
	if written {
		messages = append(messages, fmt.Sprintf("wrote %s", dest))
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
