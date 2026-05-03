package cli

import (
	"context"
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
	cmd.AddCommand(newAIAgentsCmd())
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
		if aiEntryManagedByAgents(entry.Path) {
			label += "  (agents SSOT)"
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
		label := st.Entry.Path
		if aiEntryManagedByAgents(st.Entry.Path) {
			label += "  (agents SSOT)"
		}
		p.Bullet(marker, fmt.Sprintf("%-8s live:%s backup:%s  %s",
			ui.StyleValue.Render(st.Entry.Tool), live, backup, label))
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
	c.Flags().Bool("reapply-agents", false, "After restore, reapply the agents SSOT to tool targets")
	return c
}

func runAIRestore(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	includeAuth, _ := cmd.Flags().GetBool("include-auth")
	version, _ := cmd.Flags().GetString("version")
	reapplyAgents, _ := cmd.Flags().GetBool("reapply-agents")
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
	if reapplyAgents {
		mgr := newAgentsManagerFromCmd(cmd)
		result, err := mgr.Apply(aisettings.ApplyOptions{Tools: mgr.DefaultApplyTools(), Yes: yes})
		if err != nil {
			return err
		}
		p.Section("Agents SSOT Reapply")
		for _, item := range result.Items {
			state := "in-sync"
			if item.Changed {
				state = "wrote"
			}
			p.Bullet(ui.StyleValue.Render(ui.MarkPartial), fmt.Sprintf("%-8s %-8s %s", item.ToolID, state, item.TargetPath))
		}
	}
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

func newAIAgentsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agents",
		Short: "Manage the shared AI agents instruction SSOT",
		Long:  "Manage ~/.config/dotfiles/agents/AGENTS.md and copy-render it to Claude, Codex, Cursor, and optional AI coding tool targets.",
	}
	c.AddCommand(newAIAgentsListCmd(false))
	c.AddCommand(newAIAgentsListCmd(true))
	c.AddCommand(newAIAgentsInitCmd())
	c.AddCommand(newAIAgentsAuthorCmd())
	c.AddCommand(newAIAgentsShowCmd())
	c.AddCommand(newAIAgentsEditCmd())
	c.AddCommand(newAIAgentsApplyCmd())
	c.AddCommand(newAIAgentsPullCmd())
	c.AddCommand(newAIAgentsDiffCmd())
	c.AddCommand(newAIAgentsPathCmd())
	return c
}

func newAIAgentsListCmd(verbose bool) *cobra.Command {
	use := "list"
	short := "List registered agents targets and drift"
	if verbose {
		use = "status"
		short = "Show detailed agents SSOT drift status"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr := newAgentsManagerFromCmd(cmd)
			statuses, err := mgr.Status()
			if err != nil {
				return err
			}
			p := printerFrom(cmd)
			p.Header("AI Agents SSOT")
			p.KV("SSOT", mgr.SSOTPath())
			p.Section("Targets")
			for _, st := range statuses {
				marker, style := agentDriftMarker(st.Drift)
				opt := ""
				if st.Tool.Optional {
					opt = " optional"
				}
				overlay := ""
				if st.OverlayExists {
					overlay = " overlay"
				}
				p.Bullet(style.Render(marker), fmt.Sprintf("%-8s %-14s %s%s%s",
					ui.StyleValue.Render(st.Tool.ID), st.Drift, st.TargetPath, opt, overlay))
				if verbose {
					p.Line("      rendered:%s target:%s", shortHash(st.RenderedHash), shortHash(st.TargetHash))
				}
			}
			return nil
		},
	}
}

func newAIAgentsInitCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "init",
		Short: "Create the shared agents SSOT",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, _ := cmd.Flags().GetString("from-current")
			yes, _ := cmd.Flags().GetBool("yes")
			force, _ := cmd.Flags().GetBool("force")
			mgr := newAgentsManagerFromCmd(cmd)
			res, err := mgr.Init(aisettings.InitOptions{FromCurrent: from, Yes: yes, Force: force})
			if err != nil {
				return err
			}
			p := printerFrom(cmd)
			p.Header("AI Agents Init")
			p.KV("SSOT", res.Path)
			if res.FromTool != "" {
				p.KV("From", res.FromTool)
			}
			if res.BackupPath != "" {
				p.KV("Backup", res.BackupPath)
			}
			if res.Created {
				p.Success("created")
			} else {
				p.Line("already exists")
			}
			return nil
		},
	}
	c.Flags().String("from-current", "", "Seed AGENTS.md from an existing tool target")
	c.Flags().Bool("force", false, "Overwrite an existing SSOT after backing it up")
	return c
}

func newAIAgentsAuthorCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "author",
		Short: "Interactively or programmatically edit SSOT sections",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, _ := cmd.Flags().GetString("from-current")
			nonInteractive, _ := cmd.Flags().GetBool("non-interactive")
			section, _ := cmd.Flags().GetString("section")
			value, _ := cmd.Flags().GetString("value")
			yes, _ := cmd.Flags().GetBool("yes")
			mgr := newAgentsManagerFromCmd(cmd)
			res, err := mgr.Author(aisettings.AuthorOptions{
				FromCurrent:    from,
				NonInteractive: nonInteractive,
				Section:        section,
				Value:          value,
				Yes:            yes,
			})
			if err != nil {
				return err
			}
			p := printerFrom(cmd)
			p.Header("AI Agents Author")
			p.KV("SSOT", res.Path)
			if len(res.Sections) > 0 {
				p.KV("Sections", strings.Join(res.Sections, ", "))
			}
			if res.Changed {
				p.Success("updated")
			} else {
				p.Line("no changes")
			}
			return nil
		},
	}
	c.Flags().String("from-current", "", "Pull from a live tool target before authoring")
	c.Flags().Bool("non-interactive", false, "Update one section without the wizard")
	c.Flags().String("section", "", "Section name for --non-interactive")
	c.Flags().String("value", "", "Section value for --non-interactive")
	return c
}

func newAIAgentsShowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "show",
		Short: "Print the raw or rendered agents SSOT",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rendered, _ := cmd.Flags().GetString("rendered")
			withLineNumbers, _ := cmd.Flags().GetBool("with-line-numbers")
			mgr := newAgentsManagerFromCmd(cmd)
			out, err := mgr.Show(aisettings.ShowOptions{RenderedTool: rendered, WithLineNumbers: withLineNumbers})
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			if !strings.HasSuffix(out, "\n") {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}
	c.Flags().String("rendered", "", "Print SSOT rendered for one tool")
	c.Flags().Bool("with-line-numbers", false, "Prefix output with line numbers")
	return c
}

func newAIAgentsEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open $EDITOR on the shared AGENTS.md",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr := newAgentsManagerFromCmd(cmd)
			if _, err := os.Stat(mgr.SSOTPath()); os.IsNotExist(err) {
				if _, err := mgr.Init(aisettings.InitOptions{}); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			editorCmd := exec.CommandContext(context.Background(), editor, mgr.SSOTPath())
			editorCmd.Stdin = os.Stdin
			editorCmd.Stdout = os.Stdout
			editorCmd.Stderr = os.Stderr
			return editorCmd.Run()
		},
	}
}

func newAIAgentsApplyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "apply",
		Short: "Copy-render the SSOT to agent tool targets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			toolFlag, _ := cmd.Flags().GetString("tool")
			force, _ := cmd.Flags().GetBool("force")
			yes, _ := cmd.Flags().GetBool("yes")
			mgr := newAgentsManagerFromCmd(cmd)
			ids := parseAgentToolIDs(toolFlag)
			if len(ids) == 0 {
				ids = mgr.DefaultApplyTools()
			}
			result, err := mgr.Apply(aisettings.ApplyOptions{Tools: ids, Force: force, Yes: yes})
			if err != nil {
				return err
			}
			p := printerFrom(cmd)
			p.Header("AI Agents Apply")
			for _, warning := range result.Warnings {
				p.Warn(warning)
			}
			changed := 0
			for _, item := range result.Items {
				marker := ui.StyleSuccess.Render(ui.MarkPresent)
				state := "in-sync"
				if item.Changed {
					changed++
					marker = ui.StyleHint.Render(ui.MarkPending)
					state = "would write"
					if !result.DryRun {
						state = "wrote"
					}
				}
				p.Bullet(marker, fmt.Sprintf("%-8s %-10s %s", ui.StyleValue.Render(item.ToolID), state, item.TargetPath))
				if result.DryRun && item.Diff != "" {
					p.Line("%s", item.Diff)
				}
			}
			if changed == 0 {
				p.Success("all selected targets already match")
			}
			return nil
		},
	}
	c.Flags().String("tool", "", "Comma-separated tool IDs to apply")
	c.Flags().Bool("force", false, "Reserved for hand-edit conflict workflows")
	return c
}

func newAIAgentsPullCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "pull",
		Short: "Copy one live tool target back into the SSOT",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, _ := cmd.Flags().GetString("from")
			yes, _ := cmd.Flags().GetBool("yes")
			mgr := newAgentsManagerFromCmd(cmd)
			res, err := mgr.Pull(aisettings.PullOptions{FromTool: from, Yes: yes})
			if err != nil {
				return err
			}
			p := printerFrom(cmd)
			p.Header("AI Agents Pull")
			p.KV("From", res.SourcePath)
			p.KV("SSOT", res.SSOTPath)
			if res.BackupPath != "" {
				p.KV("Backup", res.BackupPath)
			}
			if res.Changed {
				p.Success("updated")
			} else {
				p.Line("already matches")
			}
			return nil
		},
	}
	c.Flags().String("from", "", "Tool ID to pull from")
	return c
}

func newAIAgentsDiffCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "diff",
		Short: "Show rendered-vs-live diff for agents targets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			toolFlag, _ := cmd.Flags().GetString("tool")
			mgr := newAgentsManagerFromCmd(cmd)
			ids := parseAgentToolIDs(toolFlag)
			if len(ids) == 0 {
				ids = mgr.DefaultApplyTools()
			}
			for _, id := range ids {
				diff, err := mgr.Diff(id)
				if err != nil {
					return err
				}
				if diff == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "%s: in-sync\n", id)
					continue
				}
				fmt.Fprint(cmd.OutOrStdout(), diff)
			}
			return nil
		},
	}
	c.Flags().String("tool", "", "Comma-separated tool IDs to diff")
	return c
}

func newAIAgentsPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the absolute agents SSOT directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr := newAgentsManagerFromCmd(cmd)
			fmt.Fprintln(cmd.OutOrStdout(), mgr.SSOTDirPath())
			return nil
		},
	}
}

func newAgentsManagerFromCmd(cmd *cobra.Command) *aisettings.AgentsManager {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	home, _ := os.UserHomeDir()
	if over, _ := cmd.Flags().GetString("home"); over != "" {
		home = over
	}
	logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	return aisettings.NewAgentsManager(execrun.NewRunner(dryRun, logger), home)
}

func parseAgentToolIDs(value string) []string {
	var ids []string
	for _, part := range strings.Split(value, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			ids = append(ids, part)
		}
	}
	return ids
}

func agentDriftMarker(drift string) (string, interface{ Render(...string) string }) {
	switch drift {
	case "in-sync":
		return ui.MarkPresent, ui.StyleSuccess
	case "out-of-sync":
		return ui.MarkWarn, ui.StyleWarning
	case "target-missing", "ssot-missing":
		return ui.MarkAbsent, ui.StyleHint
	default:
		return ui.MarkPartial, ui.StyleHint
	}
}

func shortHash(hash string) string {
	if hash == "" {
		return "-"
	}
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func aiEntryManagedByAgents(path string) bool {
	if path == aisettings.AgentsSSOTRelPath {
		return true
	}
	for _, tool := range aisettings.RegisteredAgentTools() {
		target := strings.TrimPrefix(tool.TargetPath, "~/")
		if path == target {
			return true
		}
	}
	return false
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
