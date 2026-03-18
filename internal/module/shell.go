package module

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

const (
	omzURL           = "https://github.com/ohmyzsh/ohmyzsh/archive/refs/heads/master.tar.gz"
	omzRefreshPeriod = 168 * time.Hour // 7 days

	zshAutosuggURL    = "https://github.com/zsh-users/zsh-autosuggestions/archive/refs/heads/master.tar.gz"
	zshSyntaxURL      = "https://github.com/zsh-users/zsh-syntax-highlighting/archive/refs/heads/master.tar.gz"
	zshCompletionsURL = "https://github.com/zsh-users/zsh-completions/archive/refs/heads/master.tar.gz"
)

// shellFile describes a file managed by ShellModule.
type shellFile struct {
	templatePath string // path relative to templates/
	destPath     string // absolute destination path
	isTemplate   bool   // true = Render(), false = ReadStatic()
}

// ShellModule manages Oh My Zsh, plugins, and shell config files.
type ShellModule struct{}

func (m *ShellModule) Name() string { return "shell" }

func (m *ShellModule) files(homeDir string) []shellFile {
	cfg := filepath.Join(homeDir, ".config", "shell")
	return []shellFile{
		{"shell/zshrc.tmpl", filepath.Join(homeDir, ".zshrc"), true},
		{"shell/bashrc.tmpl", filepath.Join(homeDir, ".bashrc"), true},
		{"shell/00-exports.sh.tmpl", filepath.Join(cfg, "00-exports.sh"), true},
		{"shell/05-completion.sh", filepath.Join(cfg, "05-completion.sh"), false},
		{"shell/10-aliases.sh.tmpl", filepath.Join(cfg, "10-aliases.sh"), true},
		{"shell/20-functions.sh", filepath.Join(cfg, "20-functions.sh"), false},
		{"shell/50-tools-init.sh.tmpl", filepath.Join(cfg, "50-tools-init.sh"), true},
	}
}

func (m *ShellModule) omzDir(homeDir string) string {
	return filepath.Join(homeDir, ".oh-my-zsh")
}

func (m *ShellModule) pluginDir(homeDir, name string) string {
	return filepath.Join(homeDir, ".oh-my-zsh", "custom", "plugins", name)
}

func (m *ShellModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change
	cfg := rc.Config
	data := cfg.TemplateData()

	omzDir := m.omzDir(rc.HomeDir)
	if !rc.Runner.IsDir(omzDir) || fileutil.NeedsRefresh(omzDir, omzRefreshPeriod) {
		changes = append(changes, Change{
			Description: "download/refresh oh-my-zsh",
			Command:     fmt.Sprintf("curl -L %s | tar xz --strip-components=1 -C %s", omzURL, omzDir),
		})
	}

	plugins := []struct {
		name string
		url  string
	}{
		{"zsh-autosuggestions", zshAutosuggURL},
		{"zsh-syntax-highlighting", zshSyntaxURL},
		{"zsh-completions", zshCompletionsURL},
	}
	for _, p := range plugins {
		dir := m.pluginDir(rc.HomeDir, p.name)
		if !rc.Runner.IsDir(dir) || fileutil.NeedsRefresh(dir, omzRefreshPeriod) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("download/refresh plugin %s", p.name),
				Command:     fmt.Sprintf("curl -L %s | tar xz --strip-components=1 -C %s", p.url, dir),
			})
		}
	}

	for _, f := range m.files(rc.HomeDir) {
		content, err := m.renderFile(rc, f, data)
		if err != nil {
			return nil, fmt.Errorf("reading template %s: %w", f.templatePath, err)
		}
		if fileutil.NeedsUpdate(rc.Runner, f.destPath, content) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("write %s", f.destPath),
				Command:     fmt.Sprintf("render %s -> %s", f.templatePath, f.destPath),
			})
		}
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *ShellModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string
	cfg := rc.Config
	data := cfg.TemplateData()

	// Oh My Zsh
	omzDir := m.omzDir(rc.HomeDir)
	if !rc.Runner.IsDir(omzDir) || fileutil.NeedsRefresh(omzDir, omzRefreshPeriod) {
		if err := rc.Runner.MkdirAll(omzDir, 0755); err != nil {
			return nil, fmt.Errorf("creating oh-my-zsh dir: %w", err)
		}
		if err := fileutil.DownloadAndExtractTarGz(ctx, rc.Runner, omzURL, omzDir, 1); err != nil {
			return nil, fmt.Errorf("downloading oh-my-zsh: %w", err)
		}
		if err := fileutil.MarkRefreshed(rc.Runner, omzDir); err != nil {
			rc.Runner.Logger.Warn("mark refreshed failed", "dir", omzDir, "err", err)
		}
		messages = append(messages, "oh-my-zsh downloaded/refreshed")
	}

	// Plugins
	plugins := []struct {
		name string
		url  string
	}{
		{"zsh-autosuggestions", zshAutosuggURL},
		{"zsh-syntax-highlighting", zshSyntaxURL},
		{"zsh-completions", zshCompletionsURL},
	}
	for _, p := range plugins {
		dir := m.pluginDir(rc.HomeDir, p.name)
		if !rc.Runner.IsDir(dir) || fileutil.NeedsRefresh(dir, omzRefreshPeriod) {
			if err := rc.Runner.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("creating plugin dir %s: %w", p.name, err)
			}
			if err := fileutil.DownloadAndExtractTarGz(ctx, rc.Runner, p.url, dir, 1); err != nil {
				return nil, fmt.Errorf("downloading plugin %s: %w", p.name, err)
			}
			if err := fileutil.MarkRefreshed(rc.Runner, dir); err != nil {
				rc.Runner.Logger.Warn("mark refreshed failed", "dir", dir, "err", err)
			}
			messages = append(messages, fmt.Sprintf("plugin %s downloaded/refreshed", p.name))
		}
	}

	// Config files
	for _, f := range m.files(rc.HomeDir) {
		content, err := m.renderFile(rc, f, data)
		if err != nil {
			return nil, fmt.Errorf("rendering %s: %w", f.templatePath, err)
		}
		written, err := fileutil.EnsureFile(rc.Runner, f.destPath, content, 0644)
		if err != nil {
			return nil, fmt.Errorf("writing %s: %w", f.destPath, err)
		}
		if written {
			messages = append(messages, fmt.Sprintf("wrote %s", f.destPath))
		}
	}

	// Set default shell to zsh
	if err := m.ensureZshDefault(ctx, rc); err != nil {
		rc.Runner.Logger.Warn("setting default shell failed", "err", err)
	} else {
		messages = append(messages, "default shell set to zsh")
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}

func (m *ShellModule) renderFile(rc *RunContext, f shellFile, data map[string]any) ([]byte, error) {
	if f.isTemplate {
		return rc.Template.Render(f.templatePath, data)
	}
	return rc.Template.ReadStatic(f.templatePath)
}

func (m *ShellModule) ensureZshDefault(ctx context.Context, rc *RunContext) error {
	// Find zsh path
	zshPath := ""
	for _, candidate := range []string{"/usr/local/bin/zsh", "/opt/homebrew/bin/zsh", "/bin/zsh", "/usr/bin/zsh"} {
		if rc.Runner.FileExists(candidate) {
			zshPath = candidate
			break
		}
	}
	if zshPath == "" {
		return fmt.Errorf("zsh not found")
	}

	isLinux := rc.Config.System != nil && rc.Config.System.OS == "linux"

	if isLinux {
		// On Linux, check /etc/passwd for current shell
		result, err := rc.Runner.Run(ctx, "getent", "passwd", currentUser(rc))
		if err == nil && strings.Contains(result.Stdout, zshPath) {
			return nil // already zsh
		}
		_, err = rc.Runner.Run(ctx, "chsh", "-s", zshPath)
		return err
	}

	// macOS: use dscl
	result, err := rc.Runner.Run(ctx, "dscl", ".", "-read", fmt.Sprintf("/Users/%s", currentUser(rc)), "UserShell")
	if err == nil && strings.Contains(result.Stdout, zshPath) {
		return nil // already zsh
	}

	_, err = rc.Runner.Run(ctx, "chsh", "-s", zshPath)
	return err
}

func currentUser(rc *RunContext) string {
	// HomeDir is like /Users/foo or /home/foo
	return filepath.Base(rc.HomeDir)
}
