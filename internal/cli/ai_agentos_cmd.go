package cli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
	"github.com/entelecheia/dotfiles-v2/internal/config"
	execrun "github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newAISkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Inventory and validate AI Markdown skills",
	}
	cmd.AddCommand(newAISkillsListCmd())
	cmd.AddCommand(newAISkillsValidateCmd())
	cmd.AddCommand(newAISkillsStatusCmd())
	cmd.AddCommand(newAISkillsApplyCmd())
	cmd.AddCommand(newAISkillsPathCmd())
	return cmd
}

func newAISkillsListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List SKILL.md files and metadata status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, asJSON, _, err := scanSkillsFromCmd(cmd)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, report)
			}
			printSkillReport(printerFrom(cmd), report, false)
			return nil
		},
	}
	addSkillScanFlags(c)
	return c
}

func newAISkillsValidateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "validate",
		Short: "Validate SKILL.md frontmatter and duplicate names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, asJSON, strict, err := scanSkillsFromCmd(cmd)
			if err != nil {
				return err
			}
			errs := report.ValidationErrors(strict)
			if asJSON {
				if err := printJSON(cmd, report); err != nil {
					return err
				}
			} else {
				printSkillReport(printerFrom(cmd), report, strict)
			}
			if len(errs) > 0 {
				return fmt.Errorf("skill validation failed: %s", strings.Join(errs, "; "))
			}
			return nil
		},
	}
	addSkillScanFlags(c)
	return c
}

func addSkillScanFlags(c *cobra.Command) {
	c.Flags().String("tool", "", "Comma-separated default skill roots to scan (codex,claude,agents,antigravity)")
	c.Flags().StringArray("root", nil, "Explicit skill root to scan; may be repeated and replaces default roots")
	c.Flags().Bool("json", false, "Print JSON")
	c.Flags().Bool("strict", false, "Treat legacy skills without schema_version/frontmatter as validation failures")
}

func scanSkillsFromCmd(cmd *cobra.Command) (*aisettings.SkillScanReport, bool, bool, error) {
	toolFlag, _ := cmd.Flags().GetString("tool")
	roots, _ := cmd.Flags().GetStringArray("root")
	asJSON, _ := cmd.Flags().GetBool("json")
	strict, _ := cmd.Flags().GetBool("strict")
	home := homeFromCmd(cmd)
	report, err := aisettings.ScanSkills(aisettings.SkillScanOptions{
		HomeDir: home,
		Tools:   parseAgentToolIDs(toolFlag),
		Roots:   roots,
	})
	return report, asJSON, strict, err
}

func newAISkillsStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "Show configured skills SSOT symlink status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := skillsOptionsFromCmd(cmd)
			if err != nil {
				return err
			}
			report, err := newSkillsManagerFromCmd(cmd).Status(opts)
			if err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON {
				return printJSON(cmd, report)
			}
			printSkillsStatus(printerFrom(cmd), report)
			return nil
		},
	}
	addSkillManageFlags(c)
	c.Flags().Bool("json", false, "Print JSON")
	return c
}

func newAISkillsApplyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "apply",
		Short: "Apply configured skills SSOT symlinks to selected tools",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := skillsOptionsFromCmd(cmd)
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			persist, _ := cmd.Flags().GetBool("persist")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			opts.Force = force
			opts.DryRun = dryRun
			result, err := newSkillsManagerFromCmd(cmd).Apply(opts)
			if err != nil {
				return err
			}
			if persist && !dryRun {
				if err := persistAISkills(cmd, result.Status); err != nil {
					return err
				}
			}
			if err := auditAIEvent(cmd, "ai.skills.apply", skillsApplyPayload(result, persist)); err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON {
				return printJSON(cmd, result)
			}
			p := printerFrom(cmd)
			p.Header("AI Skills Apply")
			printSkillsStatusSummary(p, result.Status)
			printSkillsApplyResult(p, result)
			if persist {
				p.KV("Persist", fmt.Sprintf("%v", !dryRun))
			}
			return nil
		},
	}
	addSkillManageFlags(c)
	c.Flags().Bool("force", false, "Back up and replace conflicting target skill entries")
	c.Flags().Bool("persist", false, "Persist modules.ai.skills for future dot apply runs")
	c.Flags().Bool("json", false, "Print JSON")
	return c
}

func newAISkillsPathCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "path",
		Short: "Show skills SSOT and target skill roots",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := skillsOptionsFromCmd(cmd)
			if err != nil {
				return err
			}
			mgr := newSkillsManagerFromCmd(cmd)
			report, err := mgr.Status(opts)
			if err != nil {
				return err
			}
			p := printerFrom(cmd)
			p.Header("AI Skills Paths")
			printSkillsStatusSummary(p, report)
			p.Section("Target Roots")
			for _, tool := range report.Tools {
				root, err := mgr.TargetRoot(tool)
				if err != nil {
					return err
				}
				p.Bullet(ui.StyleHint.Render(ui.MarkPartial), fmt.Sprintf("%-12s %s", ui.StyleValue.Render(tool), root))
			}
			return nil
		},
	}
	addSkillManageFlags(c)
	return c
}

func addSkillManageFlags(c *cobra.Command) {
	c.Flags().String("provider", "", "Skills SSOT provider: anchor or path")
	c.Flags().String("ssot", "", "Skills SSOT root path (defaults to ~/.anchor/skills for provider=anchor)")
	c.Flags().String("tool", "", "Comma-separated explicit target tools (claude,codex,agents,gemini,antigravity)")
}

func skillsOptionsFromCmd(cmd *cobra.Command) (aisettings.SkillsOptions, error) {
	provider, _ := cmd.Flags().GetString("provider")
	ssot, _ := cmd.Flags().GetString("ssot")
	toolFlag, _ := cmd.Flags().GetString("tool")

	state, err := loadStateForCmd(cmd)
	if err == nil {
		cfg := state.Modules.AI.Skills
		if provider == "" {
			provider = cfg.Provider
		}
		if ssot == "" {
			ssot = cfg.SSOTPath
		}
		if toolFlag == "" && len(cfg.Tools) > 0 {
			return aisettings.SkillsOptions{Provider: provider, SSOTPath: ssot, Tools: append([]string(nil), cfg.Tools...)}, nil
		}
	}
	return aisettings.SkillsOptions{Provider: provider, SSOTPath: ssot, Tools: parseAgentToolIDs(toolFlag)}, nil
}

func newSkillsManagerFromCmd(cmd *cobra.Command) *aisettings.SkillsManager {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	return aisettings.NewSkillsManager(execrun.NewRunner(dryRun, slog.Default()), homeFromCmd(cmd))
}

func persistAISkills(cmd *cobra.Command, report *aisettings.SkillsStatusReport) error {
	if report == nil {
		return fmt.Errorf("skills status report is nil")
	}
	state, err := loadStateForCmd(cmd)
	if err != nil {
		return err
	}
	state.Modules.AI.Enabled = true
	state.Modules.AI.Skills = config.AISkillsConfig{
		Enabled:  true,
		Provider: report.Provider,
		SSOTPath: report.SSOTPath,
		Tools:    append([]string(nil), report.Tools...),
	}
	return saveStateForCmd(cmd, state)
}

func printSkillsStatus(p *Printer, report *aisettings.SkillsStatusReport) {
	p.Header("AI Skills Status")
	printSkillsStatusSummary(p, report)
	if len(report.Items) > 0 {
		p.Section("Targets")
		for _, item := range report.Items {
			marker, style := skillLinkStatusMarker(item.Status)
			if item.ToolID == "" {
				p.Bullet(style.Render(marker), item.Message)
				continue
			}
			line := fmt.Sprintf("%-12s %-24s %-12s %s", ui.StyleValue.Render(item.ToolID), item.SkillName, item.Status, item.TargetPath)
			p.Bullet(style.Render(marker), line)
			if item.Message != "" {
				p.Line("      %s", item.Message)
			}
		}
	}
	for _, warning := range report.Warnings {
		p.Warn(warning)
	}
}

func printSkillsStatusSummary(p *Printer, report *aisettings.SkillsStatusReport) {
	if report == nil {
		return
	}
	p.KV("Provider", report.Provider)
	p.KV("SSOT", report.SSOTPath)
	p.KV("Tools", strings.Join(report.Tools, ","))
	p.KV("Sources", fmt.Sprintf("%d", len(report.Sources)))
}

func printSkillsApplyResult(p *Printer, result *aisettings.SkillsApplyResult) {
	if result == nil {
		return
	}
	p.Section("Changes")
	changed := 0
	for _, item := range result.Items {
		if item.Changed {
			changed++
		}
		marker := ui.StyleHint.Render(ui.MarkPartial)
		if item.Changed {
			marker = ui.StyleSuccess.Render(ui.MarkPresent)
		}
		if item.Conflict && !item.Changed {
			marker = ui.StyleWarning.Render(ui.MarkWarn)
		}
		p.Bullet(marker, fmt.Sprintf("%-12s %-24s %s", ui.StyleValue.Render(item.ToolID), item.SkillName, item.Message))
		if item.BackupPath != "" {
			p.Line("      backup: %s", item.BackupPath)
		}
	}
	p.KV("Changed", fmt.Sprintf("%d", changed))
	for _, warning := range result.Warnings {
		p.Warn(warning)
	}
}

func skillsApplyPayload(result *aisettings.SkillsApplyResult, persist bool) map[string]any {
	payload := map[string]any{"persist": persist}
	if result == nil || result.Status == nil {
		return payload
	}
	payload["provider"] = result.Status.Provider
	payload["ssot_path"] = result.Status.SSOTPath
	payload["tools"] = result.Status.Tools
	payload["items"] = len(result.Items)
	payload["dry_run"] = result.DryRun
	return payload
}

func skillLinkStatusMarker(status string) (string, interface{ Render(...string) string }) {
	switch status {
	case aisettings.SkillLinkStatusInSync:
		return ui.MarkPresent, ui.StyleSuccess
	case aisettings.SkillLinkStatusConflict:
		return ui.MarkWarn, ui.StyleWarning
	case aisettings.SkillLinkStatusMissing, aisettings.SkillLinkStatusSourceMissing:
		return ui.MarkAbsent, ui.StyleHint
	default:
		return ui.MarkPartial, ui.StyleHint
	}
}

func printSkillReport(p *Printer, report *aisettings.SkillScanReport, strict bool) {
	p.Header("AI Skills")
	p.KV("Roots", fmt.Sprintf("%d", len(report.Roots)))
	p.KV("Skills", fmt.Sprintf("%d", len(report.Items)))
	p.KV("Valid", fmt.Sprintf("%d", report.Counts[aisettings.SkillStatusValid]))
	p.KV("Legacy", fmt.Sprintf("%d", report.Counts[aisettings.SkillStatusLegacy]))
	p.KV("Invalid", fmt.Sprintf("%d", report.Counts[aisettings.SkillStatusInvalid]))
	if strict {
		p.KV("Strict", "true")
	}
	p.Section("Roots")
	for _, root := range report.Roots {
		p.Bullet(ui.StyleHint.Render(ui.MarkPartial), fmt.Sprintf("%-8s %s", ui.StyleValue.Render(root.Tool), root.Path))
	}
	if len(report.Items) > 0 {
		p.Section("Skills")
		for _, item := range report.Items {
			marker, style := skillStatusMarker(item.Status)
			name := item.Frontmatter.Name
			if name == "" {
				name = "(unnamed)"
			}
			p.Bullet(style.Render(marker), fmt.Sprintf("%-8s %-8s %-20s %s", ui.StyleValue.Render(item.Tool), item.Status, name, item.Path))
			if len(item.Errors) > 0 {
				p.Line("      %s", strings.Join(item.Errors, "; "))
			}
		}
	}
	if len(report.Duplicates) > 0 {
		p.Section("Duplicates")
		for _, dup := range report.Duplicates {
			p.Bullet(ui.StyleWarning.Render(ui.MarkWarn), fmt.Sprintf("%s: %s", dup.Name, strings.Join(dup.Paths, ", ")))
		}
	}
	for _, err := range report.Errors {
		p.Warn(err)
	}
}

func skillStatusMarker(status string) (string, interface{ Render(...string) string }) {
	switch status {
	case aisettings.SkillStatusValid:
		return ui.MarkPresent, ui.StyleSuccess
	case aisettings.SkillStatusInvalid:
		return ui.MarkAbsent, ui.StyleError
	default:
		return ui.MarkPartial, ui.StyleHint
	}
}

func newAIAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect append-only dot ai audit events",
	}
	cmd.AddCommand(newAIAuditTailCmd())
	cmd.AddCommand(newAIAuditSummaryCmd())
	return cmd
}

func newAIAuditTailCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tail [N]",
		Short: "Print the last N dot ai audit events as JSONL",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n := 20
			if len(args) == 1 {
				parsed, err := strconv.Atoi(args[0])
				if err != nil || parsed < 1 {
					return fmt.Errorf("N must be a positive integer")
				}
				n = parsed
			}
			events, malformed, err := aisettings.TailAIEvents(homeFromCmd(cmd), n)
			if err != nil {
				return err
			}
			for _, event := range events {
				line, err := json.Marshal(event)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(line))
			}
			if malformed > 0 {
				printerFrom(cmd).Warn("%d malformed audit line(s) skipped", malformed)
			}
			return nil
		},
	}
}

func newAIAuditSummaryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "summary",
		Short: "Summarize dot ai audit events by type",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sum, err := aisettings.SummarizeAIEvents(homeFromCmd(cmd))
			if err != nil {
				return err
			}
			p := printerFrom(cmd)
			p.Header("AI Audit")
			p.KV("Path", sum.Path)
			p.KV("Events", fmt.Sprintf("%d", sum.Total))
			if sum.LastEvent != nil {
				p.KV("Last", fmt.Sprintf("%s %s", sum.LastEvent.Timestamp, sum.LastEvent.Type))
			}
			if sum.Malformed > 0 {
				p.KV("Malformed", fmt.Sprintf("%d", sum.Malformed))
			}
			if len(sum.ByType) > 0 {
				p.Section("By Type")
				for _, typ := range aisettings.SortedAIEventTypes(sum) {
					p.Bullet(ui.StyleHint.Render(ui.MarkPartial), fmt.Sprintf("%-24s %d", typ, sum.ByType[typ]))
				}
			}
			return nil
		},
	}
}

func auditAIEvent(cmd *cobra.Command, typ string, payload map[string]any) error {
	if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
		return nil
	}
	_, err := aisettings.AppendAIEvent(homeFromCmd(cmd), typ, payload)
	return err
}

func homeFromCmd(cmd *cobra.Command) string {
	home, _ := os.UserHomeDir()
	if cmd != nil {
		if over, _ := cmd.Flags().GetString("home"); over != "" {
			home = over
		}
	}
	return home
}

func aiSummaryPayload(sum *aisettings.Summary) map[string]any {
	if sum == nil {
		return map[string]any{}
	}
	return map[string]any{
		"version":       sum.Version,
		"path":          sum.Path,
		"entries_count": len(sum.Entries),
		"files":         sum.Files,
		"bytes":         sum.Bytes,
	}
}

func agentsApplyPayload(result *aisettings.ApplyResult) map[string]any {
	payload := map[string]any{
		"items_count": 0,
		"changed":     0,
		"backed_up":   0,
	}
	if result == nil {
		return payload
	}
	payload["items_count"] = len(result.Items)
	items := make([]map[string]any, 0, len(result.Items))
	changed := 0
	backedUp := 0
	for _, item := range result.Items {
		if item.Changed {
			changed++
		}
		if item.BackedUp {
			backedUp++
		}
		items = append(items, map[string]any{
			"tool":          item.ToolID,
			"path":          item.TargetPath,
			"changed":       item.Changed,
			"backed_up":     item.BackedUp,
			"backup_path":   item.BackupPath,
			"conflict":      item.Conflict,
			"expected_hash": item.ExpectedHash,
			"actual_hash":   item.ActualHash,
		})
	}
	payload["changed"] = changed
	payload["backed_up"] = backedUp
	payload["items"] = items
	return payload
}

func hudApplyPayload(result *aisettings.HUDResult, persist bool) map[string]any {
	payload := map[string]any{
		"persist":     persist,
		"items_count": 0,
		"changed":     0,
	}
	if result == nil {
		return payload
	}
	payload["items_count"] = len(result.Items)
	items := make([]map[string]any, 0, len(result.Items))
	changed := 0
	for _, item := range result.Items {
		if item.Changed {
			changed++
		}
		items = append(items, map[string]any{
			"tool":    item.ToolID,
			"path":    item.TargetPath,
			"changed": item.Changed,
			"drift":   item.Drift,
		})
	}
	payload["changed"] = changed
	payload["items"] = items
	return payload
}

func coauthorGuardPayload(result *aisettings.CoauthorGuardResult, persist bool) map[string]any {
	payload := map[string]any{"persist": persist}
	if result == nil {
		return payload
	}
	payload["mode"] = result.Status.Mode
	payload["hook_path"] = result.Status.HookPath
	payload["git_config_path"] = result.Status.GitConfigPath
	payload["agents_path"] = result.Status.AgentsPath
	payload["hook_changed"] = result.HookChanged
	payload["config_changed"] = result.ConfigChanged
	payload["agents_changed"] = result.AgentsChanged
	payload["agents_applied"] = result.AgentsApplied
	return payload
}

func printJSON(cmd *cobra.Command, value any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
