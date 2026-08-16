package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
	execrun "github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

// updateTools is the fixed phase order of `dot ai update`.
var updateTools = []string{"claude", "codex", "copilot", "gemini", "kimi", "kiro", "cursor", "skills"}

// updateToolBinary maps a phase id to its CLI binary where the two differ.
// Every other phase id is the binary name.
var updateToolBinary = map[string]string{
	"kiro":   "kiro-cli",
	"cursor": "cursor-agent",
}

// toolBinary resolves the executable to probe and run for a phase id.
func toolBinary(tool string) string {
	if bin, ok := updateToolBinary[tool]; ok {
		return bin
	}
	return tool
}

// updateStep records the outcome of one step within a tool phase. Status is
// one of updated, up-to-date, failed, or skipped.
type updateStep struct {
	Tool   string `json:"tool"`
	Step   string `json:"step"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

const (
	stepUpdated = "updated"
	stepRan     = "ran"
	stepCurrent = "up-to-date"
	stepFailed  = "failed"
	stepSkipped = "skipped"
)

func newAIUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update",
		Short: "Update AI CLIs, plugins, marketplaces, and skills",
		Long: `Bring managed AI tooling current in one pass.

Phases run in a fixed order (claude, codex, copilot, gemini, kimi, kiro,
cursor, skills) and are partial-failure tolerant: one tool failing never aborts
the rest. Missing binaries are skipped, not errors.

Skills are delegated to 'maru skills update/sync' — dot never writes under a
tool skill root (see docs/BOUNDARIES.md). Plugin updates take effect after a
Claude Code restart.`,
		Args: cobra.NoArgs,
		RunE: runAIUpdate,
	}
	c.Flags().Bool("check", false, "Report available updates without changing anything")
	c.Flags().StringSlice("tool", nil, "Limit to specific tools ("+strings.Join(updateTools, ",")+")")
	c.Flags().Bool("json", false, "Emit machine-readable JSON")
	return c
}

func runAIUpdate(cmd *cobra.Command, _ []string) error {
	selected, err := resolveUpdateTools(cmd)
	if err != nil {
		return err
	}
	if checkOnly, _ := cmd.Flags().GetBool("check"); checkOnly {
		return runAIUpdateCheck(cmd, selected)
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	u := newAIUpdaterFromCmd(cmd)
	p := printerFrom(cmd)
	asJSON, _ := cmd.Flags().GetBool("json")
	if !asJSON {
		p.Header("AI Update")
	}

	var steps []updateStep
	for _, tool := range selected {
		switch tool {
		case "claude":
			steps = append(steps, u.updateClaude(ctx)...)
		case "codex":
			steps = append(steps, u.updateCodex(ctx)...)
		case "copilot":
			steps = append(steps, u.selfUpdate(ctx, "copilot", "update", " (also auto-updates on startup)")...)
		case "gemini":
			steps = append(steps, u.updateGemini(ctx)...)
		case "kimi":
			steps = append(steps, u.selfUpdate(ctx, "kimi", "upgrade", "")...)
		case "kiro":
			steps = append(steps, u.selfUpdate(ctx, "kiro", "update", "")...)
		case "cursor":
			steps = append(steps, u.selfUpdate(ctx, "cursor", "update", "")...)
		case "skills":
			steps = append(steps, u.updateSkills(ctx)...)
		}
	}

	failed := 0
	for _, s := range steps {
		if s.Status == stepFailed {
			failed++
		}
	}
	auditAIEventBestEffort(cmd, "ai.update", map[string]any{
		"tools":       selected,
		"steps_count": len(steps),
		"failed":      failed,
		"steps":       steps,
	})

	if asJSON {
		if err := printJSON(cmd, map[string]any{"tools": selected, "steps": steps, "failed": failed}); err != nil {
			return err
		}
	} else {
		printUpdateSteps(p, steps)
		p.Section("Notes")
		p.Bullet(ui.StyleHint.Render(ui.MarkPartial), "Claude plugin updates take effect after a Claude Code restart.")
		p.Bullet(ui.StyleHint.Render(ui.MarkPartial), "~/.agents/skills (npm 'skills' CLI) is multi-owner and not managed by dot.")
		if failed == 0 {
			p.Success("all update steps completed")
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d update step(s) failed", failed)
	}
	return nil
}

func runAIUpdateCheck(cmd *cobra.Command, selected []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	u := newAIUpdaterFromCmd(cmd)
	p := printerFrom(cmd)
	asJSON, _ := cmd.Flags().GetBool("json")

	report := map[string]any{"tools": selected}
	versions := map[string]string{}
	for _, tool := range selected {
		if tool == "skills" {
			continue
		}
		versions[tool] = u.toolVersion(ctx, tool)
	}
	report["versions"] = versions

	var installed []installedPlugin
	claudeSelected := containsString(selected, "claude")
	if claudeSelected {
		installed = u.installedPlugins()
		report["plugins"] = installed
		if outcome, status, ok := u.claudeLastUpdate(); ok && outcome == "failed" {
			report["claude_last_update"] = map[string]string{"outcome": outcome, "status": status}
		}
	}
	if containsString(selected, "skills") {
		skills := u.skillsCheck(ctx)
		report["skills"] = skills
	}

	if asJSON {
		return printJSON(cmd, report)
	}

	p.Header("AI Update Check")
	p.Section("CLI versions")
	for _, tool := range selected {
		if tool == "skills" {
			continue
		}
		p.KV(tool, versions[tool])
	}
	if outcome, status, ok := u.claudeLastUpdate(); ok && outcome == "failed" {
		p.Warn("  claude native self-update last failed (%s) — a manual reinstall may be required", status)
	}
	if claudeSelected {
		p.Section("Claude plugins")
		if len(installed) == 0 {
			p.Bullet(ui.StyleHint.Render(ui.MarkAbsent), "no installed plugins found")
		}
		for _, pl := range installed {
			version := pl.Version
			if version == "" || version == "unknown" {
				version = "-"
			}
			p.Bullet(ui.StyleHint.Render(ui.MarkPartial),
				fmt.Sprintf("%-44s %-10s %-8s %s", ui.StyleValue.Render(pl.ID), version, pl.Scope, shortHash(pl.SHA)))
		}
	}
	if containsString(selected, "skills") {
		p.Section("Skills (maru)")
		for _, line := range u.skillsCheckLines(ctx) {
			p.Bullet(ui.StyleHint.Render(ui.MarkPartial), line)
		}
	}
	p.Section("Notes")
	p.Bullet(ui.StyleHint.Render(ui.MarkPartial), "Plugin update availability is resolved by claude itself during 'dot ai update'.")
	p.Bullet(ui.StyleHint.Render(ui.MarkPartial), "Chrome extensions update via the Chrome Web Store — no CLI path.")
	p.Line("\nRun 'dot ai update' to apply.")
	return nil
}

// aiUpdater carries the runner and home used by every phase.
type aiUpdater struct {
	runner *execrun.Runner
	home   string
	p      *Printer
	json   bool
}

func newAIUpdaterFromCmd(cmd *cobra.Command) *aiUpdater {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	asJSON, _ := cmd.Flags().GetBool("json")
	logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	return &aiUpdater{
		runner: execrun.NewRunner(dryRun, logger),
		home:   homeFromCmd(cmd),
		p:      printerFrom(cmd),
		json:   asJSON,
	}
}

// run executes a mutating command and maps its outcome onto an updateStep.
// A successful command reports "ran", not "updated": only callers that probe a
// version before and after can tell whether anything actually changed. Under
// --dry-run nothing executes, so the step reports "skipped".
func (u *aiUpdater) run(ctx context.Context, tool, step string, name string, args ...string) updateStep {
	if u.runner.DryRun {
		return updateStep{Tool: tool, Step: step, Status: stepSkipped, Detail: "dry-run: would run " + name + " " + strings.Join(args, " ")}
	}
	u.progress("%s: %s", tool, step)
	res, err := u.runner.Run(ctx, name, args...)
	if err != nil {
		return updateStep{Tool: tool, Step: step, Status: stepFailed, Detail: cmdFailureDetail(res, err)}
	}
	return updateStep{Tool: tool, Step: step, Status: stepRan, Detail: firstLine(strings.TrimSpace(res.Stdout))}
}

// withVersionDelta upgrades a "ran" step to updated or up-to-date by comparing
// the tool's version before and after the command.
func (u *aiUpdater) withVersionDelta(ctx context.Context, step updateStep, tool, before, suffix string) updateStep {
	if step.Status != stepRan {
		return step
	}
	after := u.toolVersion(ctx, tool)
	step.Status = stepUpdated
	if after == before {
		step.Status = stepCurrent
	}
	step.Detail = fmt.Sprintf("%s → %s%s", before, after, suffix)
	return step
}

// cmdFailureDetail prefers the command's stderr over the wrapped error string,
// which only carries the exit status on its first line.
func cmdFailureDetail(res *execrun.Result, err error) string {
	if res != nil {
		if detail := strings.TrimSpace(res.Stderr); detail != "" {
			return firstLine(detail)
		}
	}
	return firstLine(err.Error())
}

func (u *aiUpdater) progress(format string, a ...any) {
	if !u.json {
		u.p.Line("  "+ui.StyleHint.Render(format), a...)
	}
}

func (u *aiUpdater) skip(tool, step, detail string) updateStep {
	return updateStep{Tool: tool, Step: step, Status: stepSkipped, Detail: detail}
}

// toolVersion probes `<bin> --version`, returning "(not installed)" when the
// binary is absent. Probes always run, even under --dry-run.
func (u *aiUpdater) toolVersion(ctx context.Context, tool string) string {
	bin := toolBinary(tool)
	if !u.runner.CommandExists(bin) {
		return "(not installed)"
	}
	res, err := u.runner.RunQuery(ctx, bin, "--version")
	if err != nil {
		return "(probe failed)"
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		out = strings.TrimSpace(res.Stderr)
	}
	return firstLine(out)
}

func (u *aiUpdater) claudeLastUpdate() (string, string, bool) {
	data, err := u.runner.ReadFile(filepath.Join(u.home, ".claude", ".last-update-result.json"))
	if err != nil {
		return "", "", false
	}
	return parseClaudeLastUpdate(data)
}

func (u *aiUpdater) installedPluginsPath() string {
	return filepath.Join(u.home, ".claude", "plugins", "installed_plugins.json")
}

func (u *aiUpdater) installedPlugins() []installedPlugin {
	data, err := u.runner.ReadFile(u.installedPluginsPath())
	if err != nil {
		return nil
	}
	plugins, err := parseInstalledPlugins(data)
	if err != nil {
		return nil
	}
	return plugins
}

func (u *aiUpdater) updateClaude(ctx context.Context) []updateStep {
	if !u.runner.CommandExists("claude") {
		return []updateStep{u.skip("claude", "self-update", "claude not in PATH")}
	}
	var steps []updateStep

	// Surface a previously failed native self-update before retrying it, so a
	// broken installer reads as a known state instead of a fresh mystery.
	priorFailure := false
	if outcome, status, ok := u.claudeLastUpdate(); ok && outcome == "failed" {
		priorFailure = true
		if !u.json {
			u.p.Warn("  claude native self-update previously failed (%s); attempting once more", status)
		}
	}
	before := u.toolVersion(ctx, "claude")
	self := u.withVersionDelta(ctx, u.run(ctx, "claude", "self-update", "claude", "update"), "claude", before, "")
	if self.Status == stepFailed && priorFailure {
		self.Detail += " (native installer previously failed — reinstall Claude Code manually)"
	}
	steps = append(steps, self)

	steps = append(steps, u.run(ctx, "claude", "marketplace-update", "claude", "plugin", "marketplace", "update"))

	pluginsBefore := u.installedPlugins()
	if len(pluginsBefore) == 0 {
		steps = append(steps, u.skip("claude", "plugins", "no installed plugins found"))
		return steps
	}
	if u.runner.DryRun {
		steps = append(steps, u.skip("claude", "plugins",
			fmt.Sprintf("dry-run: would update %d installed plugin(s)", len(pluginsBefore))))
		return steps
	}
	for _, pl := range pluginsBefore {
		scope := pl.Scope
		if scope == "" {
			scope = "user"
		}
		u.progress("claude: plugin update %s", pl.ID)
		if res, err := u.runner.Run(ctx, "claude", "plugin", "update", pl.ID, "-s", scope); err != nil {
			steps = append(steps, updateStep{Tool: "claude", Step: "plugin " + pl.ID, Status: stepFailed, Detail: cmdFailureDetail(res, err)})
		}
	}
	changed := diffPlugins(pluginsBefore, u.installedPlugins())
	detail := fmt.Sprintf("%d plugin(s) checked, %d changed", len(pluginsBefore), len(changed))
	if len(changed) > 0 {
		detail += ": " + strings.Join(changed, ", ")
	}
	status := stepCurrent
	if len(changed) > 0 {
		status = stepUpdated
	}
	steps = append(steps, updateStep{Tool: "claude", Step: "plugins", Status: status, Detail: detail})
	return steps
}
func (u *aiUpdater) updateCodex(ctx context.Context) []updateStep {
	if !u.runner.CommandExists("codex") {
		return []updateStep{u.skip("codex", "self-update", "codex not in PATH")}
	}
	before := u.toolVersion(ctx, "codex")
	self := u.withVersionDelta(ctx, u.run(ctx, "codex", "self-update", "codex", "update"), "codex", before, "")
	// Codex has no per-plugin update; refreshing marketplace snapshots is the
	// only available upgrade path.
	return []updateStep{
		self,
		u.run(ctx, "codex", "marketplace-upgrade", "codex", "plugin", "marketplace", "upgrade"),
		codexClaudeMemCacheStep(u.home),
	}
}

// codexClaudeMemCacheStep verifies the codex claude-mem plugin cache is still
// runnable after a marketplace upgrade: a snapshot refresh pulls new plugin
// code without installing its dependencies, which silently breaks the native
// hooks. Read-only, so it also runs under --dry-run. Detect-and-instruct only;
// dot never auto-runs codex plugin remove/add.
func codexClaudeMemCacheStep(home string) updateStep {
	step := updateStep{Tool: "codex", Step: "claude-mem-cache"}
	path, runnable := aisettings.CodexClaudeMemCache(home)
	switch {
	case path == "":
		step.Status, step.Detail = stepSkipped, "no codex claude-mem cache (plugin not installed)"
	case runnable:
		step.Status, step.Detail = stepCurrent, "runtime ok: "+path
	default:
		step.Status, step.Detail = stepFailed, path+" missing .install-version/node_modules; run: "+aisettings.ClaudeMemRepairCommand
	}
	return step
}

// selfUpdate runs a tool's native self-update subcommand and reports the
// version delta around it. suffix is appended to the delta detail. A missing
// binary is skipped, not an error.
func (u *aiUpdater) selfUpdate(ctx context.Context, tool, subcmd, suffix string) []updateStep {
	bin := toolBinary(tool)
	if !u.runner.CommandExists(bin) {
		return []updateStep{u.skip(tool, "self-update", bin+" not in PATH")}
	}
	before := u.toolVersion(ctx, tool)
	self := u.run(ctx, tool, "self-update", bin, subcmd)
	return []updateStep{u.withVersionDelta(ctx, self, tool, before, suffix)}
}

// updateGemini reinstalls the npm package: gemini has no self-update
// subcommand. Mirrors internal/module/node.go — fnm first, plain npm fallback.
func (u *aiUpdater) updateGemini(ctx context.Context) []updateStep {
	const pkg = "@google/gemini-cli"
	before := u.toolVersion(ctx, "gemini")
	var step updateStep
	switch {
	case u.runner.CommandExists("fnm"):
		step = u.run(ctx, "gemini", "npm-install", "fnm", "exec", "--using=default", "--", "npm", "install", "-g", pkg)
	case u.runner.CommandExists("npm"):
		step = u.run(ctx, "gemini", "npm-install", "npm", "install", "-g", pkg)
	default:
		return []updateStep{u.skip("gemini", "npm-install", "neither fnm nor npm in PATH")}
	}
	return []updateStep{u.withVersionDelta(ctx, step, "gemini", before, "")}
}

// updateSkills delegates entirely to maru: docs/BOUNDARIES.md forbids dot
// writing under ~/.maru/skills or any tool skill root.
func (u *aiUpdater) updateSkills(ctx context.Context) []updateStep {
	if !u.runner.CommandExists("maru") {
		return []updateStep{u.skip("skills", "maru", "maru not in PATH")}
	}
	var steps []updateStep
	if u.runner.CommandExists("brew") {
		// Tolerated failure: an outdated maru still performs skill updates, but
		// the real reason is kept rather than relabelled as "no change".
		brew := u.run(ctx, "skills", "brew-upgrade", "brew", "upgrade", "maru-cli")
		if brew.Status == stepFailed {
			brew.Status = stepSkipped
		}
		steps = append(steps, brew)
	}

	// `maru skills ... --check` exits non-zero when drift exists (same
	// convention as `dot check`), so stdout is parsed regardless of exit code.
	updateOut, _ := u.runner.RunQuery(ctx, "maru", "skills", "update", "--check", "--json")
	if available, active, avail, perr := parseMaruUpdateCheck([]byte(updateOut.Stdout)); perr != nil {
		steps = append(steps, updateStep{Tool: "skills", Step: "bundle", Status: stepFailed, Detail: firstLine(perr.Error())})
	} else if !available {
		steps = append(steps, updateStep{Tool: "skills", Step: "bundle", Status: stepCurrent, Detail: "active " + active})
	} else {
		step := u.run(ctx, "skills", "bundle", "maru", "skills", "update", "--apply")
		if step.Status == stepRan {
			step.Status = stepUpdated
			step.Detail = fmt.Sprintf("%s → %s", active, avail)
		}
		steps = append(steps, step)
	}

	syncOut, _ := u.runner.RunQuery(ctx, "maru", "skills", "sync", "--check", "--tools", "claude,codex", "--json")
	if pending, perr := parseMaruSyncPending([]byte(syncOut.Stdout)); perr != nil {
		steps = append(steps, updateStep{Tool: "skills", Step: "sync", Status: stepFailed, Detail: firstLine(perr.Error())})
	} else if pending == 0 {
		steps = append(steps, updateStep{Tool: "skills", Step: "sync", Status: stepCurrent, Detail: "no pending actions"})
	} else {
		step := u.run(ctx, "skills", "sync", "maru", "skills", "sync", "--apply", "--tools", "claude,codex")
		if step.Status == stepRan {
			step.Status = stepUpdated
			step.Detail = fmt.Sprintf("%d pending action(s) applied", pending)
		}
		steps = append(steps, step)
	}
	return steps
}

func (u *aiUpdater) skillsCheck(ctx context.Context) map[string]any {
	out := map[string]any{}
	if !u.runner.CommandExists("maru") {
		out["maru"] = "not installed"
		return out
	}
	if res, err := u.runner.RunQuery(ctx, "maru", "skills", "update", "--check", "--json"); err == nil || res.Stdout != "" {
		if available, active, avail, perr := parseMaruUpdateCheck([]byte(res.Stdout)); perr == nil {
			out["update_available"] = available
			out["active"] = active
			out["available"] = avail
		}
	}
	if res, err := u.runner.RunQuery(ctx, "maru", "skills", "sync", "--check", "--tools", "claude,codex", "--json"); err == nil || res.Stdout != "" {
		if pending, perr := parseMaruSyncPending([]byte(res.Stdout)); perr == nil {
			out["pending_sync_actions"] = pending
		}
	}
	return out
}

func (u *aiUpdater) skillsCheckLines(ctx context.Context) []string {
	info := u.skillsCheck(ctx)
	if msg, ok := info["maru"].(string); ok {
		return []string{"maru " + msg}
	}
	var lines []string
	if available, ok := info["update_available"].(bool); ok {
		state := "up-to-date"
		if available {
			state = fmt.Sprintf("update available: %v → %v", info["active"], info["available"])
		}
		lines = append(lines, "bundle  "+state)
	}
	if pending, ok := info["pending_sync_actions"].(int); ok {
		lines = append(lines, fmt.Sprintf("sync    %d pending action(s)", pending))
	}
	if len(lines) == 0 {
		lines = append(lines, "maru probe returned no usable output")
	}
	return lines
}

func printUpdateSteps(p *Printer, steps []updateStep) {
	current := ""
	for _, s := range steps {
		if s.Tool != current {
			p.Section(s.Tool)
			current = s.Tool
		}
		marker, style := updateStepMarker(s.Status)
		label := fmt.Sprintf("%-22s %-11s", ui.StyleValue.Render(s.Step), s.Status)
		if s.Detail != "" {
			label += " " + ui.StyleHint.Render(s.Detail)
		}
		p.Bullet(style.Render(marker), label)
	}
}

func updateStepMarker(status string) (string, interface{ Render(...string) string }) {
	switch status {
	case stepUpdated:
		return ui.MarkPresent, ui.StyleSuccess
	case stepRan, stepCurrent:
		return ui.MarkPresent, ui.StyleHint
	case stepFailed:
		return ui.MarkFail, ui.StyleError
	default:
		return ui.MarkAbsent, ui.StyleHint
	}
}

func resolveUpdateTools(cmd *cobra.Command) ([]string, error) {
	requested, _ := cmd.Flags().GetStringSlice("tool")
	if len(requested) == 0 {
		return updateTools, nil
	}
	var out []string
	for _, raw := range requested {
		for _, part := range strings.Split(raw, ",") {
			id := strings.TrimSpace(part)
			if id == "" {
				continue
			}
			if !containsString(updateTools, id) {
				return nil, fmt.Errorf("unknown tool %q (valid: %s)", id, strings.Join(updateTools, ", "))
			}
			if !containsString(out, id) {
				out = append(out, id)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--tool resolved to no tools (valid: %s)", strings.Join(updateTools, ", "))
	}
	// Keep the fixed phase order regardless of flag order.
	ordered := make([]string, 0, len(out))
	for _, id := range updateTools {
		if containsString(out, id) {
			ordered = append(ordered, id)
		}
	}
	return ordered, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}

// ── parsers ───────────────────────────────────────────────────────────────

// installedPlugin is one entry of ~/.claude/plugins/installed_plugins.json.
type installedPlugin struct {
	ID      string `json:"id"`
	Scope   string `json:"scope"`
	Version string `json:"version"`
	SHA     string `json:"sha"`
}

// parseInstalledPlugins reads the v2 installed_plugins.json schema, where
// "plugins" maps "<name>@<marketplace>" to a list of installation records
// (one per scope). Only v2 is accepted — an older schema means the loop below
// would silently update nothing.
func parseInstalledPlugins(data []byte) ([]installedPlugin, error) {
	var doc struct {
		Version int `json:"version"`
		Plugins map[string][]struct {
			Scope        string `json:"scope"`
			Version      string `json:"version"`
			GitCommitSHA string `json:"gitCommitSha"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse installed_plugins.json: %w", err)
	}
	if doc.Version != 2 {
		return nil, fmt.Errorf("unsupported installed_plugins.json schema version %d (want 2)", doc.Version)
	}
	var out []installedPlugin
	for id, records := range doc.Plugins {
		for _, rec := range records {
			out = append(out, installedPlugin{ID: id, Scope: rec.Scope, Version: rec.Version, SHA: rec.GitCommitSHA})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Scope < out[j].Scope
	})
	return out, nil
}

// diffPlugins returns the ids whose sha or version changed between two reads
// of installed_plugins.json.
func diffPlugins(before, after []installedPlugin) []string {
	prev := make(map[string]installedPlugin, len(before))
	for _, pl := range before {
		prev[pl.ID+"\x00"+pl.Scope] = pl
	}
	var changed []string
	seen := map[string]bool{}
	for _, pl := range after {
		old, ok := prev[pl.ID+"\x00"+pl.Scope]
		if !ok {
			continue
		}
		if (old.SHA != pl.SHA || old.Version != pl.Version) && !seen[pl.ID] {
			seen[pl.ID] = true
			changed = append(changed, pl.ID)
		}
	}
	sort.Strings(changed)
	return changed
}

// parseClaudeLastUpdate reads ~/.claude/.last-update-result.json. ok is false
// when the file is unparseable or records no outcome.
func parseClaudeLastUpdate(data []byte) (outcome, status string, ok bool) {
	var doc struct {
		Outcome string `json:"outcome"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Outcome == "" {
		return "", "", false
	}
	return doc.Outcome, doc.Status, true
}

// parseMaruUpdateCheck reads `maru skills update --check --json`.
func parseMaruUpdateCheck(data []byte) (updateAvailable bool, active, available string, err error) {
	var doc struct {
		UpdateAvailable bool `json:"updateAvailable"`
		Active          struct {
			DisplayVersion string `json:"displayVersion"`
		} `json:"active"`
		Available struct {
			DisplayVersion string `json:"displayVersion"`
		} `json:"available"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return false, "", "", fmt.Errorf("parse maru update check: %w", err)
	}
	return doc.UpdateAvailable, doc.Active.DisplayVersion, doc.Available.DisplayVersion, nil
}

// parseMaruSyncPending counts pending actions from `maru skills sync --check --json`.
func parseMaruSyncPending(data []byte) (int, error) {
	var doc struct {
		Actions []json.RawMessage `json:"actions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return 0, fmt.Errorf("parse maru sync check: %w", err)
	}
	return len(doc.Actions), nil
}
