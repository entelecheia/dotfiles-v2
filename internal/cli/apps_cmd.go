package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/appsettings"
	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/config/catalog"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/sliceutil"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

// newAppsCmd returns the `dotfiles apps` command with subcommands for
// macOS application install + settings backup/restore.
func newAppsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "macOS app install and settings backup/restore",
		Long: `Manage macOS cask applications and their user settings.

Subcommands:
  list     Show the embedded cask catalog and terminal tool status.
  install  Install the selected casks (uses saved state, brew install --cask).
  status   Report install + backup presence for each tracked app.
  backup   Copy app settings to the host-scoped backup archive.
  restore  Copy app settings back from the archive.`,
	}
	cmd.AddCommand(newAppsListCmd())
	cmd.AddCommand(newAppsInstallCmd())
	cmd.AddCommand(newAppsStatusCmd())
	cmd.AddCommand(newAppsBackupCmd())
	cmd.AddCommand(newAppsRestoreCmd())
	return cmd
}

// --- list ---

func newAppsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the cask catalog and terminal tool status",
		Args:  cobra.NoArgs,
		RunE:  runAppsList,
	}
}

func runAppsList(cmd *cobra.Command, _ []string) error {
	cat, err := catalog.LoadMacApps()
	if err != nil {
		return err
	}
	var installed map[string]bool
	var installedTools map[string]bool
	toolStatusKnown := false
	_, brew, _, _ := appsBrewCtx(cmd)
	if brew != nil && brew.IsAvailable() {
		if runtime.GOOS == "darwin" {
			installed = brew.InstalledCasks()
		}
		installedTools, toolStatusKnown = brew.InstalledFormulas()
	}
	defaults := make(map[string]bool, len(cat.Defaults))
	for _, t := range cat.Defaults {
		defaults[t] = true
	}
	recommended := make(map[string]bool, len(cat.Recommended))
	for _, t := range cat.Recommended {
		recommended[t] = true
	}

	p := printerFrom(cmd)
	p.Header("macOS Cask Catalog")
	for _, g := range cat.Groups {
		p.Section(g.Name)
		for _, a := range g.Apps {
			marks := []string{}
			if defaults[a.Token] {
				marks = append(marks, ui.StyleSuccess.Render(ui.MarkStarred))
			}
			if recommended[a.Token] && !defaults[a.Token] {
				marks = append(marks, ui.StyleSuccess.Render(ui.MarkPreferred))
			}
			if installed != nil && installed[a.Token] {
				marks = append(marks, ui.StyleSuccess.Render(ui.MarkPresent))
			}
			marker := strings.Join(marks, " ")
			if marker == "" {
				marker = " "
			}
			p.Bullet(marker, fmt.Sprintf("%s  %s",
				ui.StyleValue.Render(a.Token),
				ui.StyleHint.Render(a.Name)))
		}
	}
	p.Section("Terminal Tools")
	for _, tool := range config.TerminalToolOptions() {
		marker := ui.StyleHint.Render(ui.MarkPartial)
		if toolStatusKnown {
			if installedTools[tool.Formula] {
				marker = ui.StyleSuccess.Render(ui.MarkPresent)
			} else {
				marker = ui.StyleHint.Render(ui.MarkAbsent)
			}
		}
		p.Bullet(marker, fmt.Sprintf("%s  %s",
			ui.StyleValue.Render(tool.Formula),
			ui.StyleHint.Render(tool.Name)))
	}
	p.Blank()
	p.Line("  %s", ui.StyleHint.Render(ui.MarkStarred+" default   "+ui.MarkPreferred+" recommended   "+ui.MarkPresent+" installed   "+ui.MarkAbsent+" missing   "+ui.MarkPartial+" unknown"))
	return nil
}

// --- install ---

func newAppsInstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "install [token...]",
		Short: "Install macOS cask apps (interactive by default; args skip the picker)",
		Long: `Install macOS cask applications.

Modes:
  - positional args       : install exactly those tokens.
  - --defaults            : install the catalog's default set.
  - --recommended         : install the catalog's recommended set.
  - --all                 : install every cask in the catalog.
  - --select              : open the checkbox picker even when state is set.
  - no args + interactive : open the checkbox picker, preselected from saved state.
  - no args + --yes       : use saved state (falls back to catalog recommended).

Casks whose .app already exists under /Applications (e.g. installed via the
App Store or downloaded directly) are skipped by default. Pass --force to
reinstall them over the existing bundle.

After an interactive run, the updated selection can be saved back to the user
state file so subsequent 'dotfiles apply' runs honour it.`,
		Args: cobra.ArbitraryArgs,
		RunE: runAppsInstall,
	}
	c.Flags().Bool("defaults", false, "Install the catalog's default set regardless of saved state")
	c.Flags().Bool("recommended", false, "Install the catalog's recommended set regardless of saved state")
	c.Flags().Bool("all", false, "Install every app in the catalog")
	c.Flags().Bool("select", false, "Force the interactive picker even when state has a list")
	c.Flags().Bool("no-save", false, "Do not persist the interactive selection back to state")
	c.Flags().Bool("force", false, "Reinstall even when the .app already exists under /Applications")
	return c
}

func runAppsInstall(cmd *cobra.Command, args []string) error {
	p := printerFrom(cmd)
	if runtime.GOOS != "darwin" {
		p.Line("%s", ui.StyleWarning.Render("not macOS — apps install is a no-op"))
		return nil
	}
	ctx := context.Background()
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	useDefaults, _ := cmd.Flags().GetBool("defaults")
	useRecommended, _ := cmd.Flags().GetBool("recommended")
	useAll, _ := cmd.Flags().GetBool("all")
	forceSelect, _ := cmd.Flags().GetBool("select")
	noSave, _ := cmd.Flags().GetBool("no-save")
	force, _ := cmd.Flags().GetBool("force")

	if useDefaults && useRecommended {
		return fmt.Errorf("--defaults and --recommended are mutually exclusive")
	}

	state, brew, _, err := appsBrewCtx(cmd)
	if err != nil {
		return err
	}
	if !brew.IsAvailable() {
		return fmt.Errorf("homebrew not available")
	}

	cat, err := catalog.LoadMacApps()
	if err != nil {
		return err
	}

	// Source of truth for `want`:
	var want []string
	interactive := false
	saveAfter := false

	switch {
	case useAll:
		want = cat.AllTokens()
	case useDefaults:
		want = cat.Defaults
	case useRecommended:
		want = cat.Recommended
	case len(args) > 0:
		// Trust explicit args; merge with user's stored extras if asked later.
		want = sliceutil.Dedupe(args)
	case forceSelect || (!yes):
		interactive = true
	default:
		// --yes without args: use saved state, fall back to recommended.
		want = append([]string(nil), state.Modules.MacApps.Casks...)
		want = append(want, state.Modules.MacApps.CasksExtra...)
		if len(want) == 0 {
			want = cat.Recommended
		}
	}

	if interactive {
		tokens := cat.AllTokens()
		sort.Strings(tokens)
		preselect := append([]string(nil), state.Modules.MacApps.Casks...)
		if len(preselect) == 0 {
			preselect = cat.Recommended
		}
		p.Line("%s", ui.StyleHint.Render(fmt.Sprintf(
			"  Catalog: %d apps across %d groups  (★ defaults, ✓ installed)", len(tokens), len(cat.Groups))))
		selected, err := ui.MultiSelect("Pick apps to install", tokens, preselect, false)
		if err != nil {
			return err
		}
		extraDefault := strings.Join(state.Modules.MacApps.CasksExtra, " ")
		extraRaw, err := ui.Input("Additional casks (space-separated, optional)", extraDefault, false)
		if err != nil {
			return err
		}
		extra := splitTokenList(extraRaw)
		want = sliceutil.Dedupe(append(append([]string(nil), selected...), extra...))

		if !noSave {
			save, err := ui.ConfirmBool("Save this selection to state?", true, false)
			if err != nil {
				return err
			}
			if save {
				saveAfter = true
				state.Modules.MacApps.Enabled = true
				state.Modules.MacApps.Casks = selected
				state.Modules.MacApps.CasksExtra = extra
			}
		}
	}

	if len(want) == 0 {
		return fmt.Errorf("nothing to install")
	}

	missing := brew.MissingCasks(want)
	if len(missing) == 0 {
		p.Line("%s", ui.StyleSuccess.Render("✓ all selected casks already installed"))
		if saveAfter {
			return persistUserState(cmd, state)
		}
		return nil
	}

	// Filter out casks whose .app already exists under /Applications (e.g.
	// installed via the App Store). Without this, brew aborts the whole batch
	// on the first conflict. --force bypasses the skip and reinstalls.
	if !force {
		existing := brew.ExistingCaskTargets(missing)
		if len(existing) > 0 {
			var toInstall, skipped []string
			for _, c := range missing {
				if existing[c] {
					skipped = append(skipped, c)
				} else {
					toInstall = append(toInstall, c)
				}
			}
			p.Line("%s", ui.StyleHint.Render(fmt.Sprintf(
				"↷ skipping %d already-present app(s): %s  (use --force to reinstall)",
				len(skipped), strings.Join(skipped, ", "))))
			missing = toInstall
		}
	}

	if len(missing) == 0 {
		p.Line("%s", ui.StyleSuccess.Render("✓ nothing to install"))
		if saveAfter {
			return persistUserState(cmd, state)
		}
		return nil
	}
	if dryRun {
		p.Line("dry-run: would install %d cask(s): %s", len(missing), strings.Join(missing, ", "))
		if saveAfter {
			return persistUserState(cmd, state)
		}
		return nil
	}
	p.Line("Installing %d cask(s): %s", len(missing), strings.Join(missing, ", "))
	if err := brew.InstallCask(ctx, missing, force); err != nil {
		return fmt.Errorf("install casks: %w", err)
	}
	p.Line("%s", ui.StyleSuccess.Render("✓ install complete"))
	if saveAfter {
		return persistUserState(cmd, state)
	}
	return nil
}

// splitTokenList parses whitespace/comma-separated tokens into a clean list.
func splitTokenList(s string) []string {
	replacer := strings.NewReplacer(",", " ", "\t", " ")
	parts := strings.Fields(replacer.Replace(s))
	return sliceutil.Dedupe(parts)
}

// splitCommaList parses a strictly comma-separated list into trimmed entries.
// Unlike splitTokenList it preserves internal whitespace, so values like
// "Moom Classic" survive intact.
func splitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return sliceutil.Dedupe(out)
}

// persistUserState writes user state honouring the --home override.
func persistUserState(cmd *cobra.Command, state *config.UserState) error {
	homeOverride, _ := cmd.Flags().GetString("home")
	if homeOverride != "" {
		return config.SaveStateForHome(homeOverride, state)
	}
	return config.SaveState(state)
}

// --- status ---

func newAppsStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "Show install + backup presence for tracked apps",
		Args:  cobra.NoArgs,
		RunE:  runAppsStatus,
	}
	c.Flags().String("from", "", "Backup root to inspect (overrides configured BackupDir)")
	return c
}

func runAppsStatus(cmd *cobra.Command, _ []string) error {
	eng, err := newAppsEngine(cmd)
	if err != nil {
		return err
	}
	mf := eng.Manifest

	var installed map[string]bool
	if runtime.GOOS == "darwin" {
		_, brew, _, _ := appsBrewCtx(cmd)
		if brew != nil && brew.IsAvailable() {
			installed = brew.InstalledCasks()
		}
	}

	p := printerFrom(cmd)
	p.Header("macOS App Settings Status")
	p.KV("Host", eng.Hostname)
	p.KV("Backup", eng.HostRoot())
	p.Section("Apps")

	statuses := eng.Status(nil)
	tokens := make([]string, 0, len(statuses))
	byToken := make(map[string]appsettings.AppStatus, len(statuses))
	for _, s := range statuses {
		tokens = append(tokens, s.Token)
		byToken[s.Token] = s
	}
	sort.Strings(tokens)

	for _, token := range tokens {
		s := byToken[token]
		marker := ui.StyleHint.Render(ui.MarkPartial) // unknown: brew unavailable
		if installed != nil {
			if installed[token] {
				marker = ui.StyleSuccess.Render(ui.MarkPresent)
			} else {
				marker = ui.StyleHint.Render(ui.MarkPartial)
			}
		}
		live := fmt.Sprintf("%d/%d", s.PresentLive, s.TotalLive)
		bak := fmt.Sprintf("%d/%d", s.PresentBak, s.TotalBak)
		if s.PresentBak == 0 {
			bak = ui.StyleHint.Render(bak)
		} else if s.PresentBak == s.TotalBak {
			bak = ui.StyleSuccess.Render(bak)
		} else {
			bak = ui.StyleWarning.Render(bak)
		}
		p.Bullet(marker, fmt.Sprintf("%-22s  live:%-6s  backup:%-8s",
			ui.StyleValue.Render(token), live, bak))
	}
	_ = mf
	return nil
}

// --- backup ---

func newAppsBackupCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "backup [token...]",
		Short: "Snapshot macOS app settings to the backup archive",
		Long: `Back up macOS application settings listed in the embedded manifest.

Modes:
  - positional args       : back up exactly those tokens.
  - --all                 : back up every manifest entry.
  - --select              : open the checkbox picker even when state has a list.
  - no args + interactive : open the checkbox picker. The list shows the
                            installed casks that also have a manifest entry,
                            plus any custom tokens you added previously.
                            Apps with an existing backup snapshot (or in your
                            saved selection) come pre-ticked. You can also
                            type extra tokens; each is validated against the
                            manifest before being accepted.
  - no args + --yes       : use saved state (falls back to manifest ∩ installed).`,
		Args: cobra.ArbitraryArgs,
		RunE: runAppsBackup,
	}
	c.Flags().String("to", "", "Backup root (overrides configured BackupDir)")
	c.Flags().Bool("all", false, "Back up every manifest entry (default: manifest ∩ installed casks)")
	c.Flags().Bool("select", false, "Force the interactive picker even when state has a list")
	c.Flags().Bool("no-save", false, "Do not persist the interactive selection back to state")
	return c
}

func runAppsBackup(cmd *cobra.Command, args []string) error {
	p := printerFrom(cmd)
	if runtime.GOOS != "darwin" {
		p.Line("%s", ui.StyleWarning.Render("not macOS — apps backup is a no-op"))
		return nil
	}
	yes, _ := cmd.Flags().GetBool("yes")
	useAll, _ := cmd.Flags().GetBool("all")
	forceSelect, _ := cmd.Flags().GetBool("select")
	noSave, _ := cmd.Flags().GetBool("no-save")

	eng, err := newAppsEngine(cmd)
	if err != nil {
		return err
	}

	state, brew, _, err := appsBrewCtx(cmd)
	if err != nil {
		return err
	}

	var tokens []string
	saveAfter := false

	switch {
	case len(args) > 0:
		tokens = sliceutil.Dedupe(args)
		// Try to discover any positional arg that isn't already in the
		// manifest (e.g. a display name like "Moom Classic"). Without this
		// the engine silently drops unknown tokens during selectTokens.
		for _, t := range tokens {
			if eng.Manifest.App(t) != nil {
				continue
			}
			discovered := appsettings.DiscoverApp(eng.HomeDir, t)
			if discovered == nil {
				p.Line("%s", ui.StyleWarning.Render(fmt.Sprintf(
					"  ⚠ %q — not in manifest and .app bundle not found; will be ignored", t)))
				continue
			}
			eng.Manifest.Apps = append(eng.Manifest.Apps, *discovered)
			p.Line("%s", ui.StyleSuccess.Render(fmt.Sprintf(
				"  ✓ %q — discovered %d backup path(s)", t, len(discovered.Paths))))
		}
	case useAll:
		tokens = eng.Manifest.Tokens()
	case forceSelect || !yes:
		tokens, saveAfter, err = pickBackupTokens(p, eng, state, brew, noSave)
		if err != nil {
			return err
		}
	default:
		tokens = resolveBackupTokens(cmd, eng)
	}

	if len(tokens) == 0 {
		return fmt.Errorf("nothing to back up")
	}

	sum, err := eng.Backup(context.Background(), tokens)
	if err != nil {
		return err
	}
	printAppSummary(p, "Backup", sum)

	// Record last backup (skipped in dry-run)
	if !eng.Runner.DryRun {
		if err := recordLastBackup(cmd, eng.HostRoot(), sum.Files); err != nil {
			eng.Runner.Logger.Warn("record last backup", "err", err)
		}
	}
	if saveAfter {
		return persistUserState(cmd, state)
	}
	return nil
}

// pickBackupTokens runs the interactive backup picker.
//
// List construction:
//   - Base options = manifest tokens ∩ installed casks (the apps that are
//     present on this machine AND have backup paths defined).
//   - Options are union-ed with the user's previously saved selection
//     (state.BackupApps), so any custom tokens added in earlier runs remain
//     visible.
//   - All options must exist in the manifest — a token without backup paths
//     would yield an empty snapshot.
//
// Pre-selection: apps whose archive directory already contains files (a prior
// successful backup) OR apps present in state.BackupApps.
//
// Extras: a single comma-separated input so tokens containing spaces
// (e.g. "Moom Classic") work without escaping. Each entry is trimmed and
// rejected unless it appears in the manifest. A warning is shown when the
// entered token isn't currently installed, but the entry is kept (so a
// machine-less backup is still possible).
func pickBackupTokens(p *Printer, eng *appsettings.Engine, state *config.UserState, brew *exec.Brew, noSave bool) ([]string, bool, error) {
	manifestTokens := eng.Manifest.Tokens()
	inManifest := make(map[string]bool, len(manifestTokens))
	for _, t := range manifestTokens {
		inManifest[t] = true
	}

	var installed map[string]bool
	if brew != nil && brew.IsAvailable() {
		installed = brew.InstalledCasks()
	}

	// Apps with an existing archive directory (prior successful backup).
	successSet := make(map[string]bool)
	for _, s := range eng.Status(manifestTokens) {
		if s.PresentBak > 0 {
			successSet[s.Token] = true
		}
	}

	// User's prior selection (includes any custom tokens).
	priorSet := make(map[string]bool)
	for _, t := range state.Modules.MacApps.BackupApps {
		priorSet[t] = true
	}

	// Build option list: installed∩manifest first (manifest order), then
	// additional prior/success entries that weren't installed at query time.
	optionsSet := make(map[string]bool)
	var options []string
	for _, t := range manifestTokens {
		if installed == nil || installed[t] {
			optionsSet[t] = true
			options = append(options, t)
		}
	}
	for _, t := range manifestTokens {
		if optionsSet[t] {
			continue
		}
		if priorSet[t] || successSet[t] {
			optionsSet[t] = true
			options = append(options, t)
		}
	}
	if len(options) == 0 {
		// No brew or no installed matches → fall back to the full manifest so
		// the user still has something to tick.
		options = manifestTokens
	}

	// Preselect: prior selection ∪ prior successful backups (intersected
	// with options so huh doesn't complain about unknown values).
	var preselect []string
	for _, t := range options {
		if priorSet[t] || successSet[t] {
			preselect = append(preselect, t)
		}
	}

	p.Line("%s", ui.StyleHint.Render(fmt.Sprintf(
		"  %d candidate app(s) — pre-ticked: saved selection + previously backed-up",
		len(options))))

	selected, err := ui.MultiSelect("Select apps to back up", options, preselect, false)
	if err != nil {
		return nil, false, err
	}

	// Prior custom entries — tokens the user added by hand in a previous run
	// that aren't surfaced by the checkbox list (either not installed as a
	// cask, or only discoverable by display name like "Moom Classic"). Carry
	// them forward as the default for the free-form input so they don't have
	// to be retyped; the validation loop below will re-resolve each one.
	selectedSet := make(map[string]bool, len(selected))
	for _, t := range selected {
		selectedSet[t] = true
	}
	var priorCustoms []string
	for _, t := range state.Modules.MacApps.BackupApps {
		if selectedSet[t] {
			continue
		}
		if inManifest[t] && installed != nil && installed[t] {
			continue // already in the checkbox list
		}
		priorCustoms = append(priorCustoms, t)
	}

	// Prefill the input with the user's prior custom entries (comma-separated)
	// so they can be edited rather than retyped; the comma separator keeps
	// multi-word tokens like "Moom Classic" intact.
	extraDefault := strings.Join(priorCustoms, ", ")
	p.Line("%s", ui.StyleHint.Render(
		"  Separate multiple entries with commas; spaces inside an entry are kept (e.g. Moom Classic, Hazel)."))
	extraRaw, err := ui.Input("Additional apps", extraDefault, false)
	if err != nil {
		return nil, false, err
	}

	var validExtras []string
	for _, entry := range splitCommaList(extraRaw) {
		if selectedSet[entry] || sliceutil.Contains(validExtras, entry) {
			continue
		}
		if inManifest[entry] {
			if installed != nil && !installed[entry] {
				p.Line("%s", ui.StyleWarning.Render(fmt.Sprintf(
					"  ⚠ %q — not currently installed; backup will skip missing paths", entry)))
			}
			validExtras = append(validExtras, entry)
			continue
		}
		// Not in the embedded manifest — try to discover the app on disk by
		// name (e.g. "Moom Classic") and synthesise a runtime entry. Accept
		// it only if we can read its bundle identifier and find at least one
		// standard Library location.
		discovered := appsettings.DiscoverApp(eng.HomeDir, entry)
		if discovered == nil {
			p.Line("%s", ui.StyleError.Render(fmt.Sprintf(
				"  ✗ %q — .app bundle not found and not in manifest; skipped", entry)))
			continue
		}
		eng.Manifest.Apps = append(eng.Manifest.Apps, *discovered)
		inManifest[entry] = true
		p.Line("%s", ui.StyleSuccess.Render(fmt.Sprintf(
			"  ✓ %q — discovered %d backup path(s)", entry, len(discovered.Paths))))
		validExtras = append(validExtras, entry)
	}

	tokens := sliceutil.Dedupe(append(append([]string(nil), selected...), validExtras...))
	if len(tokens) == 0 {
		return nil, false, fmt.Errorf("no apps selected for backup")
	}

	if noSave {
		return tokens, false, nil
	}
	save, err := ui.ConfirmBool("Save this selection to state?", true, false)
	if err != nil {
		return nil, false, err
	}
	if !save {
		return tokens, false, nil
	}
	state.Modules.MacApps.Enabled = true
	state.Modules.MacApps.BackupApps = tokens
	return tokens, true, nil
}

// --- restore ---

func newAppsRestoreCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "restore [token...]",
		Short: "Restore macOS app settings from the backup archive",
		Args:  cobra.ArbitraryArgs,
		RunE:  runAppsRestore,
	}
	c.Flags().String("from", "", "Backup root (overrides configured BackupDir)")
	c.Flags().Bool("all", false, "Restore every manifest entry")
	return c
}

func runAppsRestore(cmd *cobra.Command, args []string) error {
	p := printerFrom(cmd)
	if runtime.GOOS != "darwin" {
		p.Line("%s", ui.StyleWarning.Render("not macOS — apps restore is a no-op"))
		return nil
	}
	yes, _ := cmd.Flags().GetBool("yes")
	eng, err := newAppsEngine(cmd)
	if err != nil {
		return err
	}

	tokens := args
	if len(tokens) == 0 {
		tokens = resolveBackupTokens(cmd, eng)
	}

	if !yes {
		p.Line("%s", ui.StyleWarning.Render("This overwrites local app settings. Quit target apps first."))
		ok, err := ui.ConfirmBool("Continue with restore?", false, false)
		if err != nil {
			return err
		}
		if !ok {
			p.Line("aborted")
			return nil
		}
	}

	sum, err := eng.Restore(context.Background(), tokens)
	if err != nil {
		return err
	}
	printAppSummary(p, "Restore", sum)
	if !eng.Runner.DryRun {
		eng.FlushCFPrefsd(context.Background())
	}
	return nil
}

// --- helpers ---

// appsBrewCtx loads user state and constructs a Brew wrapper + Runner.
func appsBrewCtx(cmd *cobra.Command) (*config.UserState, *exec.Brew, *exec.Runner, error) {
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
		return nil, nil, nil, fmt.Errorf("load state: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := exec.NewRunner(dryRun, logger)
	brew := exec.NewBrew(runner)
	return state, brew, runner, nil
}

// newAppsEngine constructs an appsettings.Engine from flags + state.
// Resolution precedence for the backup root: --to / --from > state.BackupRoot
// > auto-detected Drive > default local dir.
func newAppsEngine(cmd *cobra.Command) (*appsettings.Engine, error) {
	state, _, runner, err := appsBrewCtx(cmd)
	if err != nil {
		return nil, err
	}

	home, _ := os.UserHomeDir()
	if over, _ := cmd.Flags().GetString("home"); over != "" {
		home = over
	}

	root := resolveBackupRoot(cmd, state, home)

	hostname, _ := os.Hostname()
	if idx := strings.Index(hostname, "."); idx > 0 {
		hostname = hostname[:idx]
	}

	mf, err := appsettings.LoadManifest()
	if err != nil {
		return nil, err
	}

	return &appsettings.Engine{
		Runner:   runner,
		HomeDir:  home,
		Root:     root,
		Hostname: hostname,
		Manifest: mf,
	}, nil
}

// resolveBackupRoot centralises the flag → state → detect → default chain.
func resolveBackupRoot(cmd *cobra.Command, state *config.UserState, home string) string {
	if v, err := cmd.Flags().GetString("to"); err == nil && v != "" {
		return appsettings.ExpandHome(v, home)
	}
	if v, err := cmd.Flags().GetString("from"); err == nil && v != "" {
		return appsettings.ExpandHome(v, home)
	}
	if state.Modules.MacApps.BackupRoot != "" {
		return appsettings.ExpandHome(state.Modules.MacApps.BackupRoot, home)
	}
	if drive := appsettings.DetectDriveCandidate(home); drive != "" {
		return drive
	}
	return appsettings.DefaultBackupRoot(home)
}

// resolveBackupTokens picks which manifest entries should be backed up / restored.
// Precedence:
//  1. explicit tokens on the command line (caller passes them)
//  2. --all flag → every manifest entry
//  3. state.Modules.MacApps.BackupApps → user's curated backup list
//  4. manifest ∩ installed casks (default when brew is available)
//  5. every manifest entry (fallback)
func resolveBackupTokens(cmd *cobra.Command, eng *appsettings.Engine) []string {
	all, _ := cmd.Flags().GetBool("all")
	tokens := eng.Manifest.Tokens()
	if all {
		return tokens
	}

	state, brew, _, _ := appsBrewCtx(cmd)
	if state != nil && len(state.Modules.MacApps.BackupApps) > 0 {
		return intersectManifest(state.Modules.MacApps.BackupApps, eng.Manifest)
	}

	if brew == nil || !brew.IsAvailable() {
		return tokens
	}
	installed := brew.InstalledCasks()
	if len(installed) == 0 {
		return tokens
	}
	var out []string
	for _, t := range tokens {
		if installed[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return tokens
	}
	return out
}

func intersectManifest(tokens []string, mf *appsettings.Manifest) []string {
	valid := make(map[string]bool)
	for _, t := range mf.Tokens() {
		valid[t] = true
	}
	var out []string
	for _, t := range tokens {
		if valid[t] {
			out = append(out, t)
		}
	}
	return out
}

func printAppSummary(p *Printer, label string, sum *appsettings.Summary) {
	p.Header(label + " Summary")
	for _, a := range sum.Apps {
		p.Bullet(ui.StyleHint.Render(ui.MarkPartial),
			fmt.Sprintf("%s  paths: %d copied / %d missing  files: %d  bytes: %d",
				ui.StyleValue.Render(a.Token), a.Copied, a.Missing, a.Files, a.Bytes))
	}
	p.Blank()
	p.Line("  Total: %d file(s), %d byte(s)", sum.Files, sum.Bytes)
}

// recordLastBackup stamps state.Modules.MacApps.LastBackup with the timestamp + counts.
func recordLastBackup(cmd *cobra.Command, path string, files int) error {
	homeOverride, _ := cmd.Flags().GetString("home")
	var state *config.UserState
	var err error
	if homeOverride != "" {
		state, err = config.LoadStateForHome(homeOverride)
	} else {
		state, err = config.LoadState()
	}
	if err != nil {
		return err
	}
	state.Modules.MacApps.LastBackup = &config.BackupRecord{
		Path:  path,
		Time:  time.Now(),
		Files: files,
	}
	if homeOverride != "" {
		return config.SaveStateForHome(homeOverride, state)
	}
	return config.SaveState(state)
}
