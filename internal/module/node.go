package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// NodeModule manages npm/pnpm configuration to relocate stores outside Google Drive.
type NodeModule struct{}

func (m *NodeModule) Name() string { return "node" }

func (m *NodeModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	npmrcDest := filepath.Join(rc.HomeDir, ".npmrc")

	content, err := rc.Template.Render("node/npmrc.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering node/npmrc.tmpl: %w", err)
	}
	if fileutil.NeedsUpdate(rc.Runner, npmrcDest, content) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("write %s", npmrcDest),
			Command:     "render node/npmrc.tmpl -> ~/.npmrc",
		})
	}

	storeDirs := []string{
		filepath.Join(rc.HomeDir, ".local", "share", "pnpm", "virtual-store"),
		filepath.Join(rc.HomeDir, ".local", "share", "pnpm", "store"),
		filepath.Join(rc.HomeDir, ".cache", "pnpm"),
	}
	for _, dir := range storeDirs {
		if !rc.Runner.IsDir(dir) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("create directory %s", dir),
				Command:     fmt.Sprintf("mkdir -p %s", dir),
			})
		}
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *NodeModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string

	// Ensure target directories exist
	storeDirs := []string{
		filepath.Join(rc.HomeDir, ".local", "share", "pnpm", "virtual-store"),
		filepath.Join(rc.HomeDir, ".local", "share", "pnpm", "store"),
		filepath.Join(rc.HomeDir, ".cache", "pnpm"),
	}
	for _, dir := range storeDirs {
		if !rc.Runner.IsDir(dir) {
			if err := rc.Runner.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("creating directory %s: %w", dir, err)
			}
			messages = append(messages, fmt.Sprintf("created %s", dir))
		}
	}

	// Render and write .npmrc
	npmrcDest := filepath.Join(rc.HomeDir, ".npmrc")
	content, err := rc.Template.Render("node/npmrc.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering node/npmrc.tmpl: %w", err)
	}
	written, err := fileutil.EnsureFile(rc.Runner, npmrcDest, content, 0644)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", npmrcDest, err)
	}
	if written {
		messages = append(messages, fmt.Sprintf("wrote %s", npmrcDest))
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
