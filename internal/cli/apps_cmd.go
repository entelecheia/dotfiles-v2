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
  list     Show the embedded cask catalog (groups, defaults).
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
		Short: "Show the cask catalog (groups + defaults)",
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
	if runtime.GOOS == "darwin" {
		_, brew, _, _ := appsBrewCtx(cmd)
		if brew != nil && brew.IsAvailable() {
			installed = brew.InstalledCasks()
		}
	}
	defaults := make(map[string]bool, len(cat.Defaults))
	for _, t := range cat.Defaults {
		defaults[t] = true
	}

	fmt.Println(ui.StyleHeader.Render(" macOS Cask Catalog "))
	for _, g := range cat.Groups {
		fmt.Println()
		fmt.Println(ui.StyleSection.Render("▸ " + g.Name))
		for _, a := range g.Apps {
			marks := []string{}
			if defaults[a.Token] {
				marks = append(marks, ui.StyleSuccess.Render("★"))
			}
			if installed != nil && installed[a.Token] {
				marks = append(marks, ui.StyleSuccess.Render("✓"))
			}
			prefix := strings.Join(marks, " ")
			if prefix != "" {
				prefix += " "
			} else {
				prefix = "  "
			}
			fmt.Printf("  %s%s  %s\n", prefix, ui.StyleValue.Render(a.Token), ui.StyleHint.Render(a.Name))
		}
	}
	fmt.Println()
	fmt.Println(ui.StyleHint.Render("  ★ default preselection   ✓ installed"))
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
  - --all                 : install every cask in the catalog.
  - --select              : open the checkbox picker even when state is set.
  - no args + interactive : open the checkbox picker, preselected from saved state.
  - no args + --yes       : use saved state (falls back to catalog defaults).

After an interactive run, the updated selection can be saved back to the user
state file so subsequent 'dotfiles apply' runs honour it.`,
		Args: cobra.ArbitraryArgs,
		RunE: runAppsInstall,
	}
	c.Flags().Bool("defaults", false, "Install the catalog's default set regardless of saved state")
	c.Flags().Bool("all", false, "Install every app in the catalog")
	c.Flags().Bool("select", false, "Force the interactive picker even when state has a list")
	c.Flags().Bool("no-save", false, "Do not persist the interactive selection back to state")
	return c
}

func runAppsInstall(cmd *cobra.Command, args []string) error {
	if runtime.GOOS != "darwin" {
		fmt.Println(ui.StyleWarning.Render("not macOS — apps install is a no-op"))
		return nil
	}
	ctx := context.Background()
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	useDefaults, _ := cmd.Flags().GetBool("defaults")
	useAll, _ := cmd.Flags().GetBool("all")
	forceSelect, _ := cmd.Flags().GetBool("select")
	noSave, _ := cmd.Flags().GetBool("no-save")

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
	case len(args) > 0:
		// Trust explicit args; merge with user's stored extras if asked later.
		want = dedupe(args)
	case forceSelect || (!yes):
		interactive = true
	default:
		// --yes without args: use saved state, fall back to defaults.
		want = append([]string(nil), state.Modules.MacApps.Casks...)
		want = append(want, state.Modules.MacApps.CasksExtra...)
		if len(want) == 0 {
			want = cat.Defaults
		}
	}

	if interactive {
		tokens := cat.AllTokens()
		preselect := append([]string(nil), state.Modules.MacApps.Casks...)
		if len(preselect) == 0 {
			preselect = cat.Defaults
		}
		fmt.Println(ui.StyleHint.Render(fmt.Sprintf(
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
		want = dedupe(append(append([]string(nil), selected...), extra...))

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
		fmt.Println(ui.StyleSuccess.Render("✓ all selected casks already installed"))
		if saveAfter {
			return persistUserState(cmd, state)
		}
		return nil
	}
	if dryRun {
		fmt.Printf("dry-run: would install %d cask(s): %s\n", len(missing), strings.Join(missing, ", "))
		if saveAfter {
			return persistUserState(cmd, state)
		}
		return nil
	}
	fmt.Printf("Installing %d cask(s): %s\n", len(missing), strings.Join(missing, ", "))
	if err := brew.InstallCask(ctx, missing); err != nil {
		return fmt.Errorf("install casks: %w", err)
	}
	fmt.Println(ui.StyleSuccess.Render("✓ install complete"))
	if saveAfter {
		return persistUserState(cmd, state)
	}
	return nil
}

// dedupe preserves first-seen order.
func dedupe(items []string) []string {
	seen := make(map[string]bool, len(items))
	var out []string
	for _, v := range items {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// splitTokenList parses whitespace/comma-separated tokens into a clean list.
func splitTokenList(s string) []string {
	replacer := strings.NewReplacer(",", " ", "\t", " ")
	parts := strings.Fields(replacer.Replace(s))
	return dedupe(parts)
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

	fmt.Println(ui.StyleHeader.Render(" macOS App Settings Status "))
	fmt.Println()
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Host:"), ui.StyleValue.Render(eng.Hostname))
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Backup:"), ui.StyleValue.Render(eng.HostRoot()))
	fmt.Println()

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
		inst := "?"
		if installed != nil {
			if installed[token] {
				inst = ui.StyleSuccess.Render("✓")
			} else {
				inst = ui.StyleWarning.Render("·")
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
		fmt.Printf("  %s  %-22s  live:%-6s  backup:%-8s\n",
			inst, ui.StyleValue.Render(token), live, bak)
	}
	_ = mf
	fmt.Println()
	return nil
}

// --- backup ---

func newAppsBackupCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "backup [token...]",
		Short: "Snapshot macOS app settings to the backup archive",
		Args:  cobra.ArbitraryArgs,
		RunE:  runAppsBackup,
	}
	c.Flags().String("to", "", "Backup root (overrides configured BackupDir)")
	c.Flags().Bool("all", false, "Back up every manifest entry (default: manifest ∩ installed casks)")
	return c
}

func runAppsBackup(cmd *cobra.Command, args []string) error {
	if runtime.GOOS != "darwin" {
		fmt.Println(ui.StyleWarning.Render("not macOS — apps backup is a no-op"))
		return nil
	}
	eng, err := newAppsEngine(cmd)
	if err != nil {
		return err
	}

	tokens := args
	if len(tokens) == 0 {
		tokens = resolveBackupTokens(cmd, eng)
	}

	sum, err := eng.Backup(context.Background(), tokens)
	if err != nil {
		return err
	}
	printAppSummary("Backup", sum)

	// Record last backup (skipped in dry-run)
	if !eng.Runner.DryRun {
		if err := recordLastBackup(cmd, eng.HostRoot(), sum.Files); err != nil {
			eng.Runner.Logger.Warn("record last backup", "err", err)
		}
	}
	return nil
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
	if runtime.GOOS != "darwin" {
		fmt.Println(ui.StyleWarning.Render("not macOS — apps restore is a no-op"))
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
		fmt.Println(ui.StyleWarning.Render("This overwrites local app settings. Quit target apps first."))
		ok, err := ui.ConfirmBool("Continue with restore?", false, false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("aborted")
			return nil
		}
	}

	sum, err := eng.Restore(context.Background(), tokens)
	if err != nil {
		return err
	}
	printAppSummary("Restore", sum)
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

func printAppSummary(label string, sum *appsettings.Summary) {
	fmt.Println()
	fmt.Println(ui.StyleHeader.Render(fmt.Sprintf(" %s Summary ", label)))
	fmt.Println()
	for _, a := range sum.Apps {
		line := fmt.Sprintf("  %s  paths: %d copied / %d missing  files: %d  bytes: %d",
			ui.StyleValue.Render(a.Token), a.Copied, a.Missing, a.Files, a.Bytes)
		fmt.Println(line)
	}
	fmt.Println()
	fmt.Printf("Total: %d file(s), %d byte(s)\n", sum.Files, sum.Bytes)
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

