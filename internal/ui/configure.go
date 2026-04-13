package ui

import (
	"bufio"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/entelecheia/dotfiles-v2/internal/config"
)

// printSection prints a styled section header.
func printSection(title string) {
	fmt.Println()
	fmt.Println(StyleSection.Render("▸ " + title))
}

// ConfigureIdentity prompts for identity fields with system detection.
func ConfigureIdentity(state *config.UserState, yes bool) error {
	printSection("Identity")

	// Name: state → git config → system user
	nameDefault, nameDetected := state.Name, false
	if nameDefault == "" {
		if v := detectGitConfig("user.name"); v != "" {
			nameDefault, nameDetected = v, true
		} else if v := os.Getenv("USER"); v != "" {
			nameDefault, nameDetected = v, true
		}
	}
	var err error
	state.Name, err = InputWithDetected("Full name", nameDefault, nameDetected, yes)
	if err != nil {
		return err
	}

	// Email: state → git config
	emailDefault, emailDetected := state.Email, false
	if emailDefault == "" {
		if v := detectGitConfig("user.email"); v != "" {
			emailDefault, emailDetected = v, true
		}
	}
	state.Email, err = InputWithDetected("Email address", emailDefault, emailDetected, yes)
	if err != nil {
		return err
	}

	// GitHub user: state → gh CLI → guess from email
	ghDefault, ghDetected := state.GithubUser, false
	if ghDefault == "" {
		if v := detectGithubUser(); v != "" {
			ghDefault, ghDetected = v, true
		}
	}
	state.GithubUser, err = InputWithDetected("GitHub username", ghDefault, ghDetected, yes)
	if err != nil {
		return err
	}

	// Timezone: state → /etc/localtime → $TZ → Asia/Seoul
	tzDefault, tzDetected := state.Timezone, false
	if tzDefault == "" {
		if v := detectTimezone(); v != "" {
			tzDefault, tzDetected = v, true
		} else {
			tzDefault = "Asia/Seoul"
		}
	}
	state.Timezone, err = InputWithDetected("Timezone", tzDefault, tzDetected, yes)
	return err
}

// ConfigureProfile prompts for profile selection. Returns updated state.
func ConfigureProfile(state *config.UserState, suggested string, yes bool) error {
	profileDefault := state.Profile
	if profileDefault == "" {
		profileDefault = suggested
	}

	var err error
	state.Profile, err = Select("Profile", config.AvailableProfiles(), profileDefault, yes)
	return err
}

// ConfigureSSH prompts for SSH key name with auto-detection of existing keys.
func ConfigureSSH(state *config.UserState, yes bool) error {
	printSection("SSH")

	keys := detectSSHKeys()

	sshKeyDefault := state.SSH.KeyName
	if sshKeyDefault == "" {
		if state.GithubUser != "" {
			sshKeyDefault = "id_ed25519_" + state.GithubUser
		} else {
			sshKeyDefault = "id_ed25519"
		}
	}

	if len(keys) > 0 && !yes {
		fmt.Println(StyleHint.Render(fmt.Sprintf("  Found %d SSH key(s):", len(keys))))
		for _, k := range keys {
			fmt.Println(StyleHint.Render("    • " + k))
		}

		options := make([]string, len(keys))
		copy(options, keys)
		options = append(options, "(enter custom name)")

		selectDefault := sshKeyDefault
		if !contains(options, selectDefault) {
			selectDefault = keys[0]
		}

		selected, err := Select("SSH key", options, selectDefault, false)
		if err != nil {
			return err
		}

		if selected == "(enter custom name)" {
			state.SSH.KeyName, err = Input("SSH key name", sshKeyDefault, false)
			return err
		}
		state.SSH.KeyName = selected
		return nil
	}

	var err error
	state.SSH.KeyName, err = Input("SSH key name", sshKeyDefault, yes)
	return err
}

// ConfigureWorkspace prompts for workspace settings. Skipped for server profile.
func ConfigureWorkspace(state *config.UserState, profile string, yes bool) error {
	if profile == "server" {
		state.Modules.Workspace.Path = ""
		state.Modules.Workspace.Gdrive = ""
		state.Modules.Workspace.Symlink = ""
		return nil
	}

	printSection("Workspace")

	enableWorkspace, err := ConfirmBool("Enable workspace module?", state.Modules.Workspace.Path != "", yes)
	if err != nil {
		return err
	}
	if !enableWorkspace {
		state.Modules.Workspace.Path = ""
		state.Modules.Workspace.Gdrive = ""
		state.Modules.Workspace.Symlink = ""
		return nil
	}

	// Workspace path: state → detected local → default
	wsDefault, wsDetected := state.Modules.Workspace.Path, false
	if wsDefault == "" {
		if v := detectWorkspacePath(); v != "" {
			wsDefault, wsDetected = v, true
		} else {
			wsDefault = "~/workspace"
		}
	}
	state.Modules.Workspace.Path, err = InputWithDetected("Workspace path", wsDefault, wsDetected, yes)
	if err != nil {
		return err
	}

	if runtime.GOOS == "darwin" {
		// Google Drive path: state → detected
		gdDefault, gdDetected := state.Modules.Workspace.Gdrive, false
		if gdDefault == "" {
			if v := detectGoogleDrivePath(); v != "" {
				gdDefault, gdDetected = v, true
			}
		}
		state.Modules.Workspace.Gdrive, err = InputWithDetected("Google Drive path (blank to skip)", gdDefault, gdDetected, yes)
		if err != nil {
			return err
		}
	}

	expandedPath := expandHome(state.Modules.Workspace.Path)
	if !yes {
		currentTarget := readSymlinkTarget(expandedPath)
		if currentTarget != "" {
			fmt.Println(StyleHint.Render(fmt.Sprintf("  Current symlink: %s → %s", state.Modules.Workspace.Path, currentTarget)))

			keepCurrent, err := ConfirmBool("Keep existing symlink?", true, false)
			if err != nil {
				return err
			}
			if keepCurrent {
				state.Modules.Workspace.Symlink = ""
				return nil
			}
		}

		symlinkDefault := state.Modules.Workspace.Symlink
		if symlinkDefault == "" && state.Modules.Workspace.Gdrive != "" {
			symlinkDefault = state.Modules.Workspace.Gdrive
		}
		state.Modules.Workspace.Symlink, err = Input("Symlink target (blank to skip)", symlinkDefault, false)
		if err != nil {
			return err
		}
	}
	return nil
}

// ConfigureAITools prompts for AI tools toggle.
func ConfigureAITools(state *config.UserState, yes bool) error {
	printSection("AI Tools")

	aiDefault := state.Modules.AITools
	if state.Name == "" {
		aiDefault = true
	}

	var err error
	state.Modules.AITools, err = ConfirmBool("Enable AI tools (Claude Code, etc.)?", aiDefault, yes)
	return err
}

// ConfigureTerminal prompts for terminal settings. Skipped for server profile.
func ConfigureTerminal(state *config.UserState, profile string, yes bool) error {
	if profile == "server" {
		state.Modules.Warp = false
		return nil
	}
	if runtime.GOOS != "darwin" {
		return nil
	}

	printSection("Terminal")

	var err error
	state.Modules.Warp, err = ConfirmBool("Enable Warp terminal?", state.Modules.Warp, yes)
	return err
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
		if !contains(options, selectDefault) {
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

// PrintStateSummary displays the current configuration summary with styled output.
func PrintStateSummary(state *config.UserState) {
	fmt.Println()
	fmt.Println(StyleHeader.Render(" Configuration Summary "))
	fmt.Println()

	printKV("Profile", state.Profile)
	printKV("Name", state.Name)
	printKV("Email", state.Email)
	printKV("GitHub", state.GithubUser)
	printKV("Timezone", state.Timezone)
	printKV("SSH key", state.SSH.KeyName)
	printKV("AI tools", formatBool(state.Modules.AITools))
	if state.Modules.Warp {
		printKV("Warp", formatBool(state.Modules.Warp))
	}
	if state.Modules.Workspace.Path != "" {
		printKV("Workspace", state.Modules.Workspace.Path)
		if state.Modules.Workspace.Gdrive != "" {
			printKV("GDrive", state.Modules.Workspace.Gdrive)
		}
		if state.Modules.Workspace.Symlink != "" {
			printKV("Symlink", state.Modules.Workspace.Path+" → "+state.Modules.Workspace.Symlink)
		}
	}
	if state.Modules.Fonts.Family != "" {
		printKV("Font family", state.Modules.Fonts.Family)
	}
	if state.Secrets.AgeIdentity != "" {
		printKV("Age identity", state.Secrets.AgeIdentity)
		if len(state.Secrets.AgeRecipients) > 0 {
			printKV("Age pubkey", state.Secrets.AgeRecipients[0])
		}
	}
	fmt.Println()
}

func printKV(key, value string) {
	if value == "" {
		value = lipgloss.NewStyle().Foreground(lipgloss.Color("#565F89")).Render("(unset)")
	} else {
		value = StyleValue.Render(value)
	}
	fmt.Printf("  %s  %s\n", StyleKey.Render(key+":"), value)
}

func formatBool(v bool) string {
	if v {
		return StyleSuccess.Render("✓") + " enabled"
	}
	return StyleHint.Render("✗ disabled")
}

// ── system detection ──────────────────────────────────────────────────────

// detectGitConfig reads a value from git config (global).
func detectGitConfig(key string) string {
	cmd := osexec.Command("git", "config", "--global", "--get", key)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// detectGithubUser attempts to get the current user from gh CLI.
func detectGithubUser() string {
	if _, err := osexec.LookPath("gh"); err != nil {
		return ""
	}
	cmd := osexec.Command("gh", "api", "user", "--jq", ".login")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// detectTimezone reads the system timezone.
func detectTimezone() string {
	// Try /etc/localtime symlink (Linux + macOS)
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		// e.g., /var/db/timezone/zoneinfo/Asia/Seoul or /usr/share/zoneinfo/Asia/Seoul
		for _, prefix := range []string{"/var/db/timezone/zoneinfo/", "/usr/share/zoneinfo/"} {
			if strings.HasPrefix(target, prefix) {
				return strings.TrimPrefix(target, prefix)
			}
		}
		// Fallback: take last 2 segments
		parts := strings.Split(target, "/")
		if len(parts) >= 2 {
			return parts[len(parts)-2] + "/" + parts[len(parts)-1]
		}
	}
	// Try $TZ env
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	// Try /etc/timezone (Debian/Ubuntu)
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

// detectWorkspacePath checks common workspace locations.
func detectWorkspacePath() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "workspace"),
		filepath.Join(home, "workspace"),
		filepath.Join(home, "work"),
	}
	for _, c := range candidates {
		if fi, err := os.Lstat(c); err == nil && fi.IsDir() || fi != nil && fi.Mode()&os.ModeSymlink != 0 {
			// Return with ~ prefix for portability
			return "~/" + filepath.Base(c)
		}
	}
	return ""
}

// detectGoogleDrivePath finds a Google Drive mount on macOS.
func detectGoogleDrivePath() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	home, _ := os.UserHomeDir()
	// Check common Drive paths
	if entries, err := os.ReadDir(home); err == nil {
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, "My Drive") || strings.Contains(name, "GoogleDrive") {
				return filepath.Join(home, name)
			}
		}
	}
	// Check /Volumes for mounted Drives
	if entries, err := os.ReadDir("/Volumes"); err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), "GoogleDrive") || strings.Contains(e.Name(), "Google Drive") {
				return filepath.Join("/Volumes", e.Name())
			}
		}
	}
	return ""
}

// ── existing detection helpers ────────────────────────────────────────────

// detectSSHKeys finds existing SSH key names in ~/.ssh/.
func detectSSHKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	sshDir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return nil
	}

	var keys []string
	seen := make(map[string]bool)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".pub") || strings.HasSuffix(name, ".age") {
			continue
		}
		if name == "config" || name == "known_hosts" || name == "authorized_keys" ||
			name == "authorized_age_keys" || name == "environment" || name == "agent" ||
			strings.HasPrefix(name, "config.") || strings.HasPrefix(name, "known_hosts.") ||
			strings.HasPrefix(name, "age_key") {
			continue
		}
		if !fileExists(filepath.Join(sshDir, name+".pub")) {
			continue
		}
		if !seen[name] {
			seen[name] = true
			keys = append(keys, name)
		}
	}

	sort.Slice(keys, func(i, j int) bool {
		iEd := strings.Contains(keys[i], "ed25519")
		jEd := strings.Contains(keys[j], "ed25519")
		if iEd != jEd {
			return iEd
		}
		return keys[i] < keys[j]
	})

	return keys
}

// detectAgeKeys finds existing age identity files in ~/.ssh/.
func detectAgeKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	sshDir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return nil
	}

	var keys []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".pub") {
			continue
		}
		if strings.HasPrefix(name, "age_key") && !strings.HasSuffix(name, ".pub") {
			keys = append(keys, filepath.Join("~/.ssh", name))
		}
	}
	sort.Strings(keys)
	return keys
}

// readAgePublicKey attempts to read the .pub file corresponding to an age identity.
func readAgePublicKey(identityPath string) string {
	expanded := expandHome(identityPath)
	pubPath := expanded + ".pub"
	f, err := os.Open(pubPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "age1") {
			return line
		}
	}
	return ""
}

// readSymlinkTarget returns the target of a symlink, or "" if not a symlink.
func readSymlinkTarget(path string) string {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return target
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
