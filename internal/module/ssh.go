package module

import (
	"context"
	"fmt"
	"path/filepath"
)

// SSHModule manages SSH configuration files.
type SSHModule struct{}

func (m *SSHModule) Name() string { return "ssh" }

func (m *SSHModule) files(rc *RunContext) []templatedFile {
	return []templatedFile{
		{
			templatePath: "ssh/config.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".ssh", "config"),
			isTemplate:   true,
			perm:         0600,
		},
	}
}

func (m *SSHModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	changes, err := checkTemplatedFiles(rc, m.files(rc))
	if err != nil {
		return nil, err
	}
	configDDir := filepath.Join(rc.HomeDir, ".ssh", "config.d")

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

	configDDir := filepath.Join(rc.HomeDir, ".ssh", "config.d")

	fileMessages, err := applyTemplatedFiles(rc, m.files(rc))
	if err != nil {
		return nil, err
	}
	messages = append(messages, fileMessages...)

	if !rc.Runner.IsDir(configDDir) {
		if err := rc.Runner.MkdirAll(configDDir, 0700); err != nil {
			return nil, fmt.Errorf("creating %s: %w", configDDir, err)
		}
		messages = append(messages, fmt.Sprintf("created %s", configDDir))
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
