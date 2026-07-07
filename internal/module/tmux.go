package module

import (
	"context"
	"path/filepath"
)

// TmuxModule manages tmux configuration.
type TmuxModule struct{}

func (m *TmuxModule) Name() string { return "tmux" }

func (m *TmuxModule) files(rc *RunContext) []templatedFile {
	return []templatedFile{
		{
			templatePath: "tmux/tmux.conf.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".tmux.conf"),
			isTemplate:   true,
			perm:         0644,
		},
	}
}

func (m *TmuxModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	changes, err := checkTemplatedFiles(rc, m.files(rc))
	if err != nil {
		return nil, err
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *TmuxModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	messages, err := applyTemplatedFiles(rc, m.files(rc))
	if err != nil {
		return nil, err
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
