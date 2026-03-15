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
	fontsRefreshPeriod   = 168 * time.Hour // 7 days
	nerdFontsURLTemplate = "https://github.com/ryanoasis/nerd-fonts/releases/latest/download/%s.zip"
)

// FontsModule downloads and installs Nerd Fonts.
type FontsModule struct{}

func (m *FontsModule) Name() string { return "fonts" }

func (m *FontsModule) fontFamily(rc *RunContext) string {
	if rc.Config.Modules.Fonts.Family != "" {
		return rc.Config.Modules.Fonts.Family
	}
	return "FiraCode"
}

func (m *FontsModule) fontDir(rc *RunContext) string {
	if rc.Config.System != nil && rc.Config.System.OS == "linux" {
		return filepath.Join(rc.HomeDir, ".local", "share", "fonts")
	}
	return filepath.Join(rc.HomeDir, "Library", "Fonts")
}

func (m *FontsModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	dir := m.fontDir(rc)
	family := m.fontFamily(rc)

	needsDownload := !rc.Runner.IsDir(dir) || fileutil.NeedsRefresh(dir, fontsRefreshPeriod)
	if needsDownload {
		url := fmt.Sprintf(nerdFontsURLTemplate, family)
		changes = append(changes, Change{
			Description: fmt.Sprintf("download Nerd Font %s to %s", family, dir),
			Command:     fmt.Sprintf("curl -L %s | unzip -d %s", url, dir),
		})
	} else {
		// Verify at least one font file exists by checking refresh marker
		if !rc.Runner.FileExists(filepath.Join(dir, ".dotfiles-refresh")) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("font directory %s exists but appears empty", dir),
				Command:     fmt.Sprintf("download %s fonts", family),
			})
		}
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *FontsModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string

	dir := m.fontDir(rc)
	family := m.fontFamily(rc)

	if !rc.Runner.IsDir(dir) || fileutil.NeedsRefresh(dir, fontsRefreshPeriod) {
		if err := rc.Runner.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("creating font dir %s: %w", dir, err)
		}

		url := fmt.Sprintf(nerdFontsURLTemplate, family)
		if err := fileutil.DownloadAndExtractZip(ctx, rc.Runner, url, dir); err != nil {
			return nil, fmt.Errorf("downloading font %s: %w", family, err)
		}

		if err := fileutil.MarkRefreshed(rc.Runner, dir); err != nil {
			rc.Runner.Logger.Warn("mark refreshed failed", "dir", dir, "err", err)
		}
		messages = append(messages, fmt.Sprintf("installed Nerd Font %s to %s", family, dir))

		// Run fc-cache on Linux
		if rc.Config.System != nil && strings.EqualFold(rc.Config.System.OS, "linux") {
			if _, err := rc.Runner.Run(ctx, "fc-cache", "-f"); err != nil {
				rc.Runner.Logger.Warn("fc-cache failed", "err", err)
			} else {
				messages = append(messages, "ran fc-cache -f")
			}
		}
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
