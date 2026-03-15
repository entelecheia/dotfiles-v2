package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// GPGModule manages gpg-agent configuration and git signing setup.
type GPGModule struct{}

func (m *GPGModule) Name() string { return "gpg" }

func (m *GPGModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	dest := filepath.Join(rc.HomeDir, ".gnupg", "gpg-agent.conf")
	content, err := rc.Template.Render("gpg/gpg-agent.conf.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering gpg/gpg-agent.conf.tmpl: %w", err)
	}
	if fileutil.NeedsUpdate(rc.Runner, dest, content) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("write %s", dest),
			Command:     "render gpg/gpg-agent.conf.tmpl -> ~/.gnupg/gpg-agent.conf",
		})
	}

	if rc.Config.Modules.Git.Signing {
		changes = append(changes, Change{
			Description: "configure git commit signing",
			Command:     "git config --global commit.gpgsign true",
		})
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *GPGModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string

	gnupgDir := filepath.Join(rc.HomeDir, ".gnupg")
	if err := rc.Runner.MkdirAll(gnupgDir, 0700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", gnupgDir, err)
	}

	dest := filepath.Join(gnupgDir, "gpg-agent.conf")
	content, err := rc.Template.Render("gpg/gpg-agent.conf.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering gpg/gpg-agent.conf.tmpl: %w", err)
	}
	written, err := fileutil.EnsureFile(rc.Runner, dest, content, 0600)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", dest, err)
	}
	if written {
		messages = append(messages, fmt.Sprintf("wrote %s", dest))
	}

	if rc.Config.Modules.Git.Signing {
		if _, err := rc.Runner.Run(ctx, "git", "config", "--global", "commit.gpgsign", "true"); err != nil {
			return nil, fmt.Errorf("setting commit.gpgsign: %w", err)
		}
		if _, err := rc.Runner.Run(ctx, "git", "config", "--global", "gpg.format", "openpgp"); err != nil {
			return nil, fmt.Errorf("setting gpg.format: %w", err)
		}
		messages = append(messages, "configured git commit signing")
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
