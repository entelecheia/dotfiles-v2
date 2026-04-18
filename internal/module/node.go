package module

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// globalNpmPackages are installed globally when npm is available.
var globalNpmPackages = []string{
	"@tobilu/qmd", // local search engine for markdown/docs
}

// legacyPnpmKeys are pnpm-only keys that used to be written to ~/.npmrc.
// npm 11+ warns about them, so we scrub them from any legacy ~/.npmrc.
var legacyPnpmKeys = []string{
	"virtual-store-dir",
	"store-dir",
	"cache-dir",
}

// NodeModule manages npm/pnpm configuration and global tools.
type NodeModule struct{}

func (m *NodeModule) Name() string { return "node" }

// pnpmNpmrcPath returns ~/.config/pnpm/npmrc — the pnpm-dedicated config path.
func pnpmNpmrcPath(rc *RunContext) string {
	return filepath.Join(rc.HomeDir, ".config", "pnpm", "npmrc")
}

// legacyNpmrcPath returns ~/.npmrc — the path we no longer manage but still clean.
func legacyNpmrcPath(rc *RunContext) string {
	return filepath.Join(rc.HomeDir, ".npmrc")
}

// pnpmDirs returns the directories pnpm expects for its virtual-store/store/cache.
func pnpmDirs(rc *RunContext) []string {
	return []string{
		filepath.Join(rc.HomeDir, ".config", "pnpm"),
		filepath.Join(rc.HomeDir, ".local", "share", "pnpm", "virtual-store"),
		filepath.Join(rc.HomeDir, ".local", "share", "pnpm", "store"),
		filepath.Join(rc.HomeDir, ".cache", "pnpm"),
	}
}

func (m *NodeModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	npmrcDest := pnpmNpmrcPath(rc)

	content, err := rc.Template.Render("node/pnpm-npmrc.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering node/pnpm-npmrc.tmpl: %w", err)
	}
	if fileutil.NeedsUpdate(rc.Runner, npmrcDest, content) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("write %s", npmrcDest),
			Command:     "render node/pnpm-npmrc.tmpl -> ~/.config/pnpm/npmrc",
		})
	}

	for _, dir := range pnpmDirs(rc) {
		if !rc.Runner.IsDir(dir) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("create directory %s", dir),
				Command:     fmt.Sprintf("mkdir -p %s", dir),
			})
		}
	}

	// Legacy ~/.npmrc cleanup — scrub pnpm-only keys so npm stops warning.
	legacyPath := legacyNpmrcPath(rc)
	if legacyNpmrcNeedsCleanup(rc.Runner, legacyPath) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("scrub pnpm-only keys from legacy %s", legacyPath),
			Command:     "strip virtual-store-dir/store-dir/cache-dir from ~/.npmrc",
		})
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
	for _, dir := range pnpmDirs(rc) {
		if !rc.Runner.IsDir(dir) {
			if err := rc.Runner.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("creating directory %s: %w", dir, err)
			}
			messages = append(messages, fmt.Sprintf("created %s", dir))
		}
	}

	// Render and write pnpm npmrc
	npmrcDest := pnpmNpmrcPath(rc)
	content, err := rc.Template.Render("node/pnpm-npmrc.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering node/pnpm-npmrc.tmpl: %w", err)
	}
	written, err := fileutil.EnsureFile(rc.Runner, npmrcDest, content, 0644)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", npmrcDest, err)
	}
	if written {
		messages = append(messages, fmt.Sprintf("wrote %s", npmrcDest))
	}

	// Scrub pnpm-only keys from legacy ~/.npmrc (removes the file if it becomes empty).
	legacyPath := legacyNpmrcPath(rc)
	cleaned, removed, err := cleanLegacyNpmrc(rc.Runner, legacyPath)
	if err != nil {
		return nil, fmt.Errorf("cleaning legacy %s: %w", legacyPath, err)
	}
	if removed {
		messages = append(messages, fmt.Sprintf("removed stale %s (pnpm keys migrated)", legacyPath))
	} else if cleaned {
		messages = append(messages, fmt.Sprintf("scrubbed pnpm-only keys from %s", legacyPath))
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

// legacyNpmrcNeedsCleanup returns true when ~/.npmrc exists and contains any pnpm-only key.
func legacyNpmrcNeedsCleanup(runner *exec.Runner, path string) bool {
	if !runner.FileExists(path) {
		return false
	}
	data, err := runner.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if lineHasLegacyPnpmKey(line) {
			return true
		}
	}
	return false
}

// cleanLegacyNpmrc strips pnpm-only keys from ~/.npmrc. Returns (cleaned, removed, err):
//   - cleaned=true when at least one line was removed
//   - removed=true when the file ended up empty and was deleted
func cleanLegacyNpmrc(runner *exec.Runner, path string) (bool, bool, error) {
	if !runner.FileExists(path) {
		return false, false, nil
	}
	data, err := runner.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}

	lines := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(lines))
	cleaned := false
	for _, line := range lines {
		if lineHasLegacyPnpmKey(line) {
			cleaned = true
			continue
		}
		kept = append(kept, line)
	}
	if !cleaned {
		return false, false, nil
	}

	// Also drop the legacy managed-by header so a fully-migrated file is considered empty.
	trimmedKept := dropLegacyHeaderComments(kept)
	if allBlank(trimmedKept) {
		if err := runner.Remove(path); err != nil {
			return false, false, err
		}
		return true, true, nil
	}

	out := strings.Join(trimmedKept, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if err := runner.WriteFile(path, []byte(out), 0644); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func lineHasLegacyPnpmKey(line string) bool {
	trimmed := strings.TrimSpace(line)
	for _, key := range legacyPnpmKeys {
		if strings.HasPrefix(trimmed, key+"=") {
			return true
		}
	}
	return false
}

// dropLegacyHeaderComments removes the two comment lines we used to write above the pnpm keys
// so that a fully-migrated file is considered empty.
func dropLegacyHeaderComments(lines []string) []string {
	legacyHeaders := []string{
		"# Managed by dotfiles-v2 — do not edit manually",
		"# Relocate pnpm virtual store outside Google Drive",
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		matched := false
		for _, h := range legacyHeaders {
			if trimmed == h {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, line)
		}
	}
	return out
}

func allBlank(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	return true
}
