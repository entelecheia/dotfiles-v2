package ui

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/appsettings"
	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/config/catalog"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
	"github.com/entelecheia/dotfiles-v2/internal/sliceutil"
)

// ConfigureAI prompts for AI CLI/config helper setup.
func ConfigureAI(state *config.UserState, yes bool, freshDefault bool) error {
	printSection("AI")

	aiDefault := state.Modules.AI.Enabled
	if freshDefault {
		aiDefault = true
	}

	if !yes {
		fmt.Println(StyleHint.Render("  Enables shell aliases and assistant config files; app installation is managed by `dotfiles apps`."))
		fmt.Println(StyleHint.Render("  Managed files: ~/.config/shell/30-ai.sh, ~/.config/claude/settings.json"))
	}

	var err error
	state.Modules.AI.Enabled, err = ConfirmBool("Enable AI CLI/config helpers?", aiDefault, yes)
	return err
}

// ConfigureTerminal prompts for prompt style, GUI terminal apps, and CLI
// terminal tools. Prompt style and tools apply on all platforms; GUI apps are
// macOS-only.
func ConfigureTerminal(state *config.UserState, profile string, yes bool) error {
	printSection("Terminal")

	// Prompt style — useful on every platform including servers.
	promptDefault := state.Modules.PromptStyle
	if promptDefault == "" {
		if profile == "server" || profile == "minimal" {
			promptDefault = "minimal"
		} else {
			promptDefault = "rich"
		}
	}
	var err error
	state.Modules.PromptStyle, err = Select("Prompt style",
		[]string{"minimal", "rich"}, promptDefault, yes)
	if err != nil {
		return err
	}

	if runtime.GOOS == "darwin" && profile != "server" {
		appDefault := state.Modules.TerminalApps.Casks
		if !state.Modules.TerminalApps.Enabled && len(appDefault) == 0 {
			appDefault = config.DefaultTerminalApps(profile)
		}
		selectedApps, err := MultiSelectLabeled("Terminal apps to install", terminalAppSelectOptions(), appDefault, yes)
		if err != nil {
			return err
		}
		state.Modules.TerminalApps.Enabled = true
		state.Modules.TerminalApps.Casks = selectedApps
		state.Modules.Warp = sliceutil.Contains(selectedApps, "warp")
	} else if profile == "server" {
		state.Modules.Warp = false
		state.Modules.TerminalApps = config.UserTerminalAppsState{}
	}

	toolDefault := state.Modules.TerminalTools.Formulas
	if !state.Modules.TerminalTools.Enabled && len(toolDefault) == 0 {
		toolDefault = config.DefaultTerminalTools(profile)
	}
	selectedTools, err := MultiSelectLabeled("Terminal tools to install", terminalToolSelectOptions(), toolDefault, yes)
	if err != nil {
		return err
	}
	state.Modules.TerminalTools.Enabled = true
	state.Modules.TerminalTools.Formulas = selectedTools

	extraDefault := strings.Join(state.Modules.TerminalTools.FormulasExtra, " ")
	extraStr, err := Input("Additional terminal formulas (space-separated, optional)", extraDefault, yes)
	if err != nil {
		return err
	}
	state.Modules.TerminalTools.FormulasExtra = splitCaskList(extraStr)

	return nil
}

func terminalAppSelectOptions() []SelectOption {
	apps := config.TerminalAppOptions()
	options := make([]SelectOption, len(apps))
	for i, app := range apps {
		options[i] = SelectOption{
			Label: app.Token + " — " + app.Name,
			Value: app.Token,
		}
	}
	return options
}

func terminalToolSelectOptions() []SelectOption {
	tools := config.TerminalToolOptions()
	options := make([]SelectOption, len(tools))
	for i, tool := range tools {
		options[i] = SelectOption{
			Label: tool.Formula + " — " + tool.Name,
			Value: tool.Formula,
		}
	}
	return options
}

// ConfigureFonts prompts for font family. Skipped for server/minimal profile.
func ConfigureFonts(state *config.UserState, profile string, yes bool) error {
	if profile == "server" || profile == "minimal" {
		return nil
	}

	printSection("Fonts")

	fontFamily := state.Modules.Fonts.Family
	if fontFamily == "" {
		fontFamily = "FiraCode"
	}

	var err error
	state.Modules.Fonts.Family, err = Select("Font family", []string{"FiraCode", "JetBrainsMono", "Hack"}, fontFamily, yes)
	return err
}

// ConfigureSecrets prompts for age encryption settings with auto-detection.
func ConfigureSecrets(state *config.UserState, profile string, yes bool) error {
	if profile == "server" || profile == "minimal" {
		return nil
	}

	printSection("Secrets (age encryption)")

	ageKeys := detectAgeKeys()

	if len(ageKeys) == 0 && state.Secrets.AgeIdentity == "" {
		fmt.Println(StyleHint.Render("  No age keys found. Skipping secrets configuration."))
		fmt.Println(StyleHint.Render("  Create one later with: age-keygen -o ~/.ssh/age_key"))
		return nil
	}

	enableSecrets, err := ConfirmBool("Configure age encryption for secrets?", state.Secrets.AgeIdentity != "" || len(ageKeys) > 0, yes)
	if err != nil {
		return err
	}
	if !enableSecrets {
		state.Secrets.AgeIdentity = ""
		state.Secrets.AgeRecipients = nil
		return nil
	}

	identityDefault := state.Secrets.AgeIdentity
	if identityDefault == "" && len(ageKeys) > 0 {
		identityDefault = ageKeys[0]
	}

	if len(ageKeys) > 0 && !yes {
		fmt.Println(StyleHint.Render(fmt.Sprintf("  Found %d age key(s):", len(ageKeys))))
		for _, k := range ageKeys {
			fmt.Println(StyleHint.Render("    • " + k))
		}

		options := make([]string, len(ageKeys))
		copy(options, ageKeys)
		options = append(options, "(enter custom path)")

		selectDefault := identityDefault
		if !sliceutil.Contains(options, selectDefault) {
			selectDefault = ageKeys[0]
		}

		selected, err := Select("Age identity (private key)", options, selectDefault, false)
		if err != nil {
			return err
		}

		if selected == "(enter custom path)" {
			state.Secrets.AgeIdentity, err = Input("Age identity path", identityDefault, false)
			if err != nil {
				return err
			}
		} else {
			state.Secrets.AgeIdentity = selected
		}
	} else {
		state.Secrets.AgeIdentity, err = Input("Age identity path", identityDefault, yes)
		if err != nil {
			return err
		}
	}

	recipientDefault := ""
	if len(state.Secrets.AgeRecipients) > 0 {
		recipientDefault = state.Secrets.AgeRecipients[0]
	}
	if recipientDefault == "" {
		recipientDefault = readAgePublicKey(state.Secrets.AgeIdentity)
	}

	if recipientDefault != "" {
		fmt.Println(StyleHint.Render(fmt.Sprintf("  Public key: %s", recipientDefault)))
		state.Secrets.AgeRecipients = []string{recipientDefault}
	} else if !yes {
		recipient, err := Input("Age recipient (public key, blank to skip)", "", false)
		if err != nil {
			return err
		}
		if recipient != "" {
			state.Secrets.AgeRecipients = []string{recipient}
		}
	}

	return nil
}

// ConfigureMacApps prompts for macOS cask selection and backup destination.
// Skipped on non-darwin. Mutates state.Modules.MacApps in place.
func ConfigureMacApps(state *config.UserState, profile string, yes bool) error {
	if runtime.GOOS != "darwin" {
		state.Modules.MacApps = config.UserMacAppsState{}
		return nil
	}
	if profile == "server" {
		state.Modules.MacApps = config.UserMacAppsState{}
		return nil
	}

	printSection("macOS Apps")

	cat, err := catalog.LoadMacApps()
	if err != nil {
		fmt.Println(StyleWarning.Render("  catalog load failed: " + err.Error()))
		return nil
	}

	enableDefault := state.Modules.MacApps.Enabled || len(state.Modules.MacApps.Casks) > 0
	if state.Name == "" {
		// Fresh install defaults to enabled on darwin non-server profiles.
		enableDefault = true
	}
	enable, err := ConfirmBool("Manage macOS cask apps?", enableDefault, yes)
	if err != nil {
		return err
	}
	if !enable {
		state.Modules.MacApps = config.UserMacAppsState{}
		return nil
	}
	state.Modules.MacApps.Enabled = true

	// MultiSelect presented in grouped sections. Build a single flattened list
	// so huh renders group separators as disabled-looking labels.
	tokens := cat.AllTokens()
	// Preselect: existing state → catalog recommended (curated set).
	preselect := state.Modules.MacApps.Casks
	if len(preselect) == 0 {
		preselect = cat.Recommended
	}
	// Filter preselect to tokens that actually exist in the catalog.
	seen := make(map[string]bool)
	for _, t := range tokens {
		seen[t] = true
	}
	var valid []string
	for _, t := range preselect {
		if seen[t] {
			valid = append(valid, t)
		}
	}

	if !yes {
		fmt.Println(StyleHint.Render(fmt.Sprintf("  Catalog: %d apps across %d groups", len(tokens), len(cat.Groups))))
	}
	selected, err := MultiSelect("Select apps to install", tokens, valid, yes)
	if err != nil {
		return err
	}
	state.Modules.MacApps.Casks = selected

	// Free-form additions
	extraDefault := strings.Join(state.Modules.MacApps.CasksExtra, " ")
	extraStr, err := Input("Additional casks (space-separated, optional)", extraDefault, yes)
	if err != nil {
		return err
	}
	state.Modules.MacApps.CasksExtra = splitCaskList(extraStr)

	// Backup list: default to the install list, but let the user trim it down.
	backupDefault := state.Modules.MacApps.BackupApps
	if len(backupDefault) == 0 {
		backupDefault = selected
	}
	sameAsInstall, err := ConfirmBool("Use the same list for settings backup?",
		len(state.Modules.MacApps.BackupApps) == 0 || sliceutil.Equal(state.Modules.MacApps.BackupApps, selected),
		yes)
	if err != nil {
		return err
	}
	if sameAsInstall {
		state.Modules.MacApps.BackupApps = nil // defer to install list at runtime
	} else {
		backupSel, err := MultiSelect("Select apps whose settings to back up", selected, backupDefault, yes)
		if err != nil {
			return err
		}
		state.Modules.MacApps.BackupApps = backupSel
	}

	// Backup root (shared by app-settings/ + profiles/)
	rootDefault := state.Modules.MacApps.BackupRoot
	detected := false
	if rootDefault == "" {
		if drive := appsettings.DetectDriveCandidate(fileutil.ExpandHome("~")); drive != "" {
			rootDefault = drive
			detected = true
		} else {
			home, _ := os.UserHomeDir()
			rootDefault = appsettings.DefaultBackupRoot(home)
		}
	}
	choice, err := Select("Backup root",
		[]string{"drive (auto-detected)", "local", "custom"},
		pickBackupChoice(rootDefault, detected),
		yes)
	if err != nil {
		return err
	}
	switch choice {
	case "drive (auto-detected)":
		if detected {
			state.Modules.MacApps.BackupRoot = rootDefault
		} else {
			path, inputErr := Input("Drive backup root", rootDefault, yes)
			if inputErr != nil {
				return inputErr
			}
			state.Modules.MacApps.BackupRoot = path
		}
	case "local":
		home, _ := os.UserHomeDir()
		state.Modules.MacApps.BackupRoot = appsettings.DefaultBackupRoot(home)
	case "custom":
		path, inputErr := Input("Backup root path", rootDefault, yes)
		if inputErr != nil {
			return inputErr
		}
		state.Modules.MacApps.BackupRoot = path
	}

	return nil
}
