package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// globalNpmPackages are installed globally when npm is available.
var globalNpmPackages = []string{
	"@tobilu/qmd", // local search engine for markdown/docs
}

// NodeModule manages npm/pnpm configuration and global tools.
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

	// Global npm packages (only if npm is available)
	if rc.Runner.CommandExists("npm") {
		for _, pkg := range globalNpmPackages {
			if !isNpmPackageInstalled(ctx, rc, pkg) {
				changes = append(changes, Change{
					Description: fmt.Sprintf("install global npm package: %s", pkg),
					Command:     fmt.Sprintf("npm install -g %s", pkg),
				})
			}
		}
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

// isNpmPackageInstalled checks if a global npm package is installed.
func isNpmPackageInstalled(ctx context.Context, rc *RunContext, pkg string) bool {
	result, err := rc.Runner.Run(ctx, "npm", "list", "-g", "--depth=0", pkg)
	if err != nil {
		return false
	}
	return result.ExitCode == 0
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

	// Install global npm packages
	if rc.Runner.CommandExists("npm") {
		for _, pkg := range globalNpmPackages {
			if isNpmPackageInstalled(ctx, rc, pkg) {
				continue
			}
			if _, err := rc.Runner.Run(ctx, "npm", "install", "-g", pkg); err != nil {
				messages = append(messages, fmt.Sprintf("⚠ failed to install %s: %v", pkg, err))
			} else {
				messages = append(messages, fmt.Sprintf("installed global npm: %s", pkg))
			}
		}
	} else {
		messages = append(messages, "⚠ npm not found — skipping global package install (install node via fnm first)")
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
