package module

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// GPGModule manages gpg-agent configuration and git signing setup.
type GPGModule struct{}

func (m *GPGModule) Name() string { return "gpg" }

func (m *GPGModule) files(rc *RunContext) []templatedFile {
	return []templatedFile{
		{
			templatePath: "gpg/gpg-agent.conf.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".gnupg", "gpg-agent.conf"),
			isTemplate:   true,
			perm:         0600,
		},
	}
}

func (m *GPGModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	changes, err := checkTemplatedFiles(rc, m.files(rc))
	if err != nil {
		return nil, err
	}

	if rc.Config.Modules.Git.Signing {
		if gitGlobalConfigValue(ctx, rc, "commit.gpgsign") != "true" {
			changes = append(changes, Change{
				Description: "configure git commit signing",
				Command:     "git config --global commit.gpgsign true",
			})
		}
		if gitGlobalConfigValue(ctx, rc, "gpg.format") != "openpgp" {
			changes = append(changes, Change{
				Description: "configure git gpg format",
				Command:     "git config --global gpg.format openpgp",
			})
		}
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *GPGModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string

	gnupgDir := filepath.Join(rc.HomeDir, ".gnupg")
	if err := rc.Runner.MkdirAll(gnupgDir, 0700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", gnupgDir, err)
	}

	fileMessages, err := applyTemplatedFiles(rc, m.files(rc))
	if err != nil {
		return nil, err
	}
	messages = append(messages, fileMessages...)

	if rc.Config.Modules.Git.Signing {
		changed := false
		if gitGlobalConfigValue(ctx, rc, "commit.gpgsign") != "true" {
			if _, err := rc.Runner.Run(ctx, "git", "config", "--global", "commit.gpgsign", "true"); err != nil {
				return nil, fmt.Errorf("setting commit.gpgsign: %w", err)
			}
			changed = true
		}
		if gitGlobalConfigValue(ctx, rc, "gpg.format") != "openpgp" {
			if _, err := rc.Runner.Run(ctx, "git", "config", "--global", "gpg.format", "openpgp"); err != nil {
				return nil, fmt.Errorf("setting gpg.format: %w", err)
			}
			changed = true
		}
		if changed {
			messages = append(messages, "configured git commit signing")
		}
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}

func gitGlobalConfigValue(ctx context.Context, rc *RunContext, key string) string {
	result, err := rc.Runner.RunQuery(ctx, "git", "config", "--global", "--get", key)
	if err != nil || result == nil || result.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}
