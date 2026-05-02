package cli

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/config/catalog"
	execrun "github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newAICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ai",
		Short: "AI CLI/config helpers and settings backup/restore",
		Long: `Manage portable AI assistant configuration.

The ai module writes shell/config helper files. It does not install Claude,
Codex, or ChatGPT apps; use 'dotfiles apps install' for Homebrew casks.`,
	}
	cmd.AddCommand(newAIListCmd())
	cmd.AddCommand(newAIStatusCmd())
	cmd.AddCommand(newAIBackupCmd())
	cmd.AddCommand(newAIRestoreCmd())
	cmd.AddCommand(newAIExportCmd())
	cmd.AddCommand(newAIImportCmd())
	return cmd
}

func newAIListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List AI helpers, detected CLIs, and managed paths",
		Args:  cobra.NoArgs,
		RunE:  runAIList,
	}
	c.Flags().Bool("include-auth", false, "Show auth/local-secret paths that are excluded by default")
	return c
}

func runAIList(cmd *cobra.Command, _ []string) error {
	includeAuth, _ := cmd.Flags().GetBool("include-auth")
	p := printerFrom(cmd)
	p.Header("AI Helpers")
	p.Section("Module")
	p.KV("Name", "ai")
	p.KV("Writes", "~/.config/shell/30-ai.sh, ~/.config/claude/settings.json")
	p.KV("Installs apps", "no — use `dotfiles apps install`")

	p.Section("Detected CLI tools")
	for _, name := range []string{"claude", "codex", "gh", "fabric"} {
		path, err := exec.LookPath(name)
		marker := ui.StyleHint.Render(ui.MarkAbsent)
		value := "(not found)"
		if err == nil {
			marker = ui.StyleSuccess.Render(ui.MarkPresent)
			value = path
		}
		p.Bullet(marker, fmt.Sprintf("%-8s %s", ui.StyleValue.Render(name), ui.StyleHint.Render(value)))
	}

	p.Section("Portable settings")
	for _, entry := range aisettings.Entries(includeAuth) {
		marker := ui.StyleHint.Render(ui.MarkPartial)
		if entry.Auth {
			marker = ui.StyleWarning.Render(ui.MarkWarn)
		}
		label := entry.Path
		if entry.Auth {
			label += "  (auth)"
		}
		p.Bullet(marker, fmt.Sprintf("%-8s %s", ui.StyleValue.Render(entry.Tool), label))
	}

	if apps, err := aiCaskTokens(); err == nil && len(apps) > 0 {
		p.Section("AI app casks")
		p.Line("  %s", ui.StyleHint.Render(strings.Join(apps, ", ")))
	}
	return nil
}

func newAIStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "Show AI settings live/backup status",
		Args:  cobra.NoArgs,
		RunE:  runAIStatus,
	}
	c.Flags().String("from", "", "Backup root to inspect")
	c.Flags().Bool("include-auth", false, "Include auth/local-secret paths in status")
	return c
}

func runAIStatus(cmd *cobra.Command, _ []string) error {
	includeAuth, _ := cmd.Flags().GetBool("include-auth")
	eng, err := newAIEngine(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	p.Header("AI Config Status")
	p.KV("Host", eng.Hostname)
	p.KV("Backup", eng.HostRoot())
	if latest, err := eng.ResolveLatest(); err == nil {
		p.KV("Latest", latest)
	} else {
		p.KV("Latest", "(none)")
	}
	p.Section("Paths")
	for _, st := range eng.Status(includeAuth) {
		live := "·"
		backup := "·"
		if st.PresentLive {
			live = "✓"
		}
		if st.PresentBackup {
			backup = "✓"
		}
		marker := ui.StyleHint.Render(ui.MarkPartial)
		if st.PresentLive && st.PresentBackup {
			marker = ui.StyleSuccess.Render(ui.MarkPresent)
		}
		if st.Entry.Auth {
			marker = ui.StyleWarning.Render(ui.MarkWarn)
		}
		p.Bullet(marker, fmt.Sprintf("%-8s live:%s backup:%s  %s",
			ui.StyleValue.Render(st.Entry.Tool), live, backup, st.Entry.Path))
	}
	return nil
}

func newAIBackupCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "backup",
		Short: "Create a versioned AI settings snapshot",
		Args:  cobra.NoArgs,
		RunE:  runAIBackup,
	}
	c.Flags().String("to", "", "Backup root (overrides configured BackupRoot)")
	c.Flags().String("tag", "", "Human-friendly label stored in meta.yaml")
	c.Flags().Bool("include-auth", false, "Include auth/local-secret files")
	return c
}

func runAIBackup(cmd *cobra.Command, _ []string) error {
	includeAuth, _ := cmd.Flags().GetBool("include-auth")
	tag, _ := cmd.Flags().GetString("tag")
	eng, err := newAIEngine(cmd)
	if err != nil {
		return err
	}
	sum, err := eng.Backup(aisettings.BackupOptions{Tag: tag, IncludeAuth: includeAuth})
	if err != nil {
		return err
	}
	printAISummary(printerFrom(cmd), "AI Backup", sum)
	return nil
}

func newAIRestoreCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "restore",
		Short: "Restore AI settings from a versioned snapshot",
		Args:  cobra.NoArgs,
		RunE:  runAIRestore,
	}
	c.Flags().String("from", "", "Backup root (overrides configured BackupRoot)")
	c.Flags().String("version", "", `Specific version to restore, or "latest" (default: latest)`)
	c.Flags().Bool("include-auth", false, "Restore auth/local-secret files from the snapshot")
	return c
}

func runAIRestore(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	includeAuth, _ := cmd.Flags().GetBool("include-auth")
	version, _ := cmd.Flags().GetString("version")
	eng, err := newAIEngine(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if version == "" || version == "latest" {
		v, err := eng.ResolveLatest()
		if err != nil {
			return err
		}
		version = v
	}
	if !yes {
		p.Line("About to restore AI settings from snapshot %s.", version)
		ok, err := ui.ConfirmBool("Continue?", false, false)
		if err != nil {
			return err
		}
		if !ok {
			p.Line("aborted")
			return nil
		}
	}
	sum, err := eng.Restore(aisettings.RestoreOptions{Version: version, IncludeAuth: includeAuth})
	if err != nil {
		return err
	}
	printAISummary(p, "AI Restore", sum)
	return nil
}

func newAIExportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "export <file.tar.gz>",
		Short: "Export AI settings to a portable tar.gz archive",
		Args:  cobra.ExactArgs(1),
		RunE:  runAIExport,
	}
	c.Flags().String("tag", "", "Human-friendly label stored in meta.yaml")
	c.Flags().Bool("include-auth", false, "Include auth/local-secret files")
	return c
}

func runAIExport(cmd *cobra.Command, args []string) error {
	includeAuth, _ := cmd.Flags().GetBool("include-auth")
	tag, _ := cmd.Flags().GetString("tag")
	eng, err := newAIEngine(cmd)
	if err != nil {
		return err
	}
	sum, err := eng.Export(args[0], aisettings.BackupOptions{Tag: tag, IncludeAuth: includeAuth})
	if err != nil {
		return err
	}
	printAISummary(printerFrom(cmd), "AI Export", sum)
	return nil
}

func newAIImportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "import <file.tar.gz>",
		Short: "Import AI settings from a portable tar.gz archive",
		Args:  cobra.ExactArgs(1),
		RunE:  runAIImport,
	}
	c.Flags().Bool("include-auth", false, "Import auth/local-secret files from the archive")
	return c
}

func runAIImport(cmd *cobra.Command, args []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	includeAuth, _ := cmd.Flags().GetBool("include-auth")
	eng, err := newAIEngine(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if !yes {
		p.Line("About to import AI settings from %s.", args[0])
		ok, err := ui.ConfirmBool("Continue?", false, false)
		if err != nil {
			return err
		}
		if !ok {
			p.Line("aborted")
			return nil
		}
	}
	sum, err := eng.Import(args[0], aisettings.RestoreOptions{IncludeAuth: includeAuth})
	if err != nil {
		return err
	}
	printAISummary(p, "AI Import", sum)
	return nil
}

func newAIEngine(cmd *cobra.Command) (*aisettings.Engine, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	homeOverride, _ := cmd.Flags().GetString("home")
	var state *config.UserState
	var err error
	if homeOverride != "" {
		state, err = config.LoadStateForHome(homeOverride)
	} else {
		state, err = config.LoadState()
	}
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	home, _ := os.UserHomeDir()
	if homeOverride != "" {
		home = homeOverride
	}
	root := resolveBackupRoot(cmd, state, home)
	hostname, _ := os.Hostname()
	if idx := strings.Index(hostname, "."); idx > 0 {
		hostname = hostname[:idx]
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return &aisettings.Engine{
		Runner:   execrun.NewRunner(dryRun, logger),
		HomeDir:  home,
		Root:     root,
		Hostname: hostname,
		User:     os.Getenv("USER"),
	}, nil
}

func printAISummary(p *Printer, title string, sum *aisettings.Summary) {
	p.Header(title + " Summary")
	if sum.Version != "" {
		p.KV("Version", sum.Version)
	}
	if sum.Path != "" {
		p.KV("Path", sum.Path)
	}
	p.Section("Entries")
	for _, entry := range sum.Entries {
		marker := ui.StyleHint.Render(ui.MarkPartial)
		if entry.Copied > 0 {
			marker = ui.StyleSuccess.Render(ui.MarkPresent)
		}
		if entry.Auth {
			marker = ui.StyleWarning.Render(ui.MarkWarn)
		}
		p.Bullet(marker, fmt.Sprintf("%-8s paths:%d copied / %d missing  files:%d  bytes:%d  %s",
			ui.StyleValue.Render(entry.Tool), entry.Copied, entry.Missing, entry.Files, entry.Bytes, entry.Path))
	}
	p.Blank()
	p.Line("  Total: %d file(s), %d byte(s)", sum.Files, sum.Bytes)
}

func aiCaskTokens() ([]string, error) {
	cat, err := catalog.LoadMacApps()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, group := range cat.Groups {
		if group.Name != "AI" {
			continue
		}
		for _, app := range group.Apps {
			out = append(out, app.Token)
		}
	}
	return out, nil
}
