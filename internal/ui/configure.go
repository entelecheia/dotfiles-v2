package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

// ConfigureIdentity prompts for identity fields. Returns updated state.
func ConfigureIdentity(state *config.UserState, yes bool) error {
	var err error

	fmt.Println("\n--- Identity ---")

	state.Name, err = Input("Full name", state.Name, yes)
	if err != nil {
		return err
	}

	state.Email, err = Input("Email address", state.Email, yes)
	if err != nil {
		return err
	}

	state.GithubUser, err = Input("GitHub username", state.GithubUser, yes)
	if err != nil {
		return err
	}

	tz := state.Timezone
	if tz == "" {
		tz = "Asia/Seoul"
	}
	state.Timezone, err = Input("Timezone", tz, yes)
	if err != nil {
		return err
	}

	return nil
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
	fmt.Println("\n--- SSH ---")

	// Detect existing SSH keys
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
		fmt.Printf("  Found %d SSH key(s):\n", len(keys))
		for _, k := range keys {
			fmt.Printf("    %s\n", k)
		}
		fmt.Println()

		// Build selection list: detected keys + custom option
		options := make([]string, len(keys))
		copy(options, keys)
		options = append(options, "(enter custom name)")

		// Set default to current config or best match
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

	fmt.Println("\n--- Workspace ---")

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

	workspacePath := state.Modules.Workspace.Path
	if workspacePath == "" {
		workspacePath = "~/ai-workspace"
	}
	state.Modules.Workspace.Path, err = Input("Workspace path", workspacePath, yes)
	if err != nil {
		return err
	}

	if runtime.GOOS == "darwin" {
		state.Modules.Workspace.Gdrive, err = Input("Google Drive path (leave blank to skip)", state.Modules.Workspace.Gdrive, yes)
		if err != nil {
			return err
		}
	}

	// Symlink target: detect existing symlink or prompt
	expandedPath := expandHome(state.Modules.Workspace.Path)
	if !yes {
		currentTarget := readSymlinkTarget(expandedPath)
		if currentTarget != "" {
			fmt.Printf("  Current symlink: %s -> %s\n", state.Modules.Workspace.Path, currentTarget)

			// Offer to keep or change
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
		state.Modules.Workspace.Symlink, err = Input("Symlink target (leave blank to skip)", symlinkDefault, false)
		if err != nil {
			return err
		}
	}

	return nil
}

// ConfigureAITools prompts for AI tools toggle.
func ConfigureAITools(state *config.UserState, yes bool) error {
	fmt.Println("\n--- AI Tools ---")

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

	fmt.Println("\n--- Terminal ---")

	var err error
	state.Modules.Warp, err = ConfirmBool("Enable Warp terminal?", state.Modules.Warp, yes)
	return err
}

// ConfigureFonts prompts for font family. Skipped for server/minimal profile.
func ConfigureFonts(state *config.UserState, profile string, yes bool) error {
	if profile == "server" || profile == "minimal" {
		return nil
	}

	fmt.Println("\n--- Fonts ---")

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

	fmt.Println("\n--- Secrets (age encryption) ---")

	// Detect existing age keys
	ageKeys := detectAgeKeys()

	if len(ageKeys) == 0 && state.Secrets.AgeIdentity == "" {
		fmt.Println("  No age keys found. Skipping secrets configuration.")
		fmt.Println("  (Create one later with: age-keygen -o ~/.ssh/age_key)")
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

	// Age identity (private key)
	identityDefault := state.Secrets.AgeIdentity
	if identityDefault == "" && len(ageKeys) > 0 {
		identityDefault = ageKeys[0]
	}

	if len(ageKeys) > 0 && !yes {
		fmt.Printf("  Found %d age key(s):\n", len(ageKeys))
		for _, k := range ageKeys {
			fmt.Printf("    %s\n", k)
		}
		fmt.Println()

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

	// Age recipient (public key) — try to read from .pub file
	recipientDefault := ""
	if len(state.Secrets.AgeRecipients) > 0 {
		recipientDefault = state.Secrets.AgeRecipients[0]
	}
	if recipientDefault == "" {
		recipientDefault = readAgePublicKey(state.Secrets.AgeIdentity)
	}

	if recipientDefault != "" {
		fmt.Printf("  Public key: %s\n", recipientDefault)
		state.Secrets.AgeRecipients = []string{recipientDefault}
	} else if !yes {
		recipient, err := Input("Age recipient (public key, leave blank to skip)", "", false)
		if err != nil {
			return err
		}
		if recipient != "" {
			state.Secrets.AgeRecipients = []string{recipient}
		}
	}

	return nil
}

// PrintStateSummary displays the current configuration summary.
func PrintStateSummary(state *config.UserState) {
	fmt.Println("\n=== Summary ===")
	fmt.Printf("  Profile:      %s\n", state.Profile)
	fmt.Printf("  Name:         %s\n", state.Name)
	fmt.Printf("  Email:        %s\n", state.Email)
	fmt.Printf("  GitHub:       %s\n", state.GithubUser)
	fmt.Printf("  Timezone:     %s\n", state.Timezone)
	fmt.Printf("  SSH key:      %s\n", state.SSH.KeyName)
	fmt.Printf("  AI tools:     %v\n", state.Modules.AITools)
	if state.Modules.Warp {
		fmt.Printf("  Warp:         %v\n", state.Modules.Warp)
	}
	if state.Modules.Workspace.Path != "" {
		fmt.Printf("  Workspace:    %s\n", state.Modules.Workspace.Path)
		if state.Modules.Workspace.Gdrive != "" {
			fmt.Printf("  GDrive:       %s\n", state.Modules.Workspace.Gdrive)
		}
		if state.Modules.Workspace.Symlink != "" {
			fmt.Printf("  Symlink:      %s -> %s\n", state.Modules.Workspace.Path, state.Modules.Workspace.Symlink)
		}
	}
	if state.Modules.Fonts.Family != "" {
		fmt.Printf("  Font family:  %s\n", state.Modules.Fonts.Family)
	}
	if state.Secrets.AgeIdentity != "" {
		fmt.Printf("  Age identity: %s\n", state.Secrets.AgeIdentity)
		if len(state.Secrets.AgeRecipients) > 0 {
			fmt.Printf("  Age pubkey:   %s\n", state.Secrets.AgeRecipients[0])
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

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
		// Skip directories, public keys, known files
		if e.IsDir() || strings.HasSuffix(name, ".pub") || strings.HasSuffix(name, ".age") {
			continue
		}
		if name == "config" || name == "known_hosts" || name == "authorized_keys" ||
			name == "authorized_age_keys" || name == "environment" || name == "agent" ||
			strings.HasPrefix(name, "config.") || strings.HasPrefix(name, "known_hosts.") ||
			strings.HasPrefix(name, "age_key") {
			continue
		}
		// Must have a matching .pub file to be a key pair
		if !fileExists(filepath.Join(sshDir, name+".pub")) {
			continue
		}
		if !seen[name] {
			seen[name] = true
			keys = append(keys, name)
		}
	}

	// Sort: ed25519 first, then rsa
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
	data, err := os.ReadFile(pubPath)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	if strings.HasPrefix(line, "age1") {
		return line
	}
	// Multi-line: find the age1... line
	for _, l := range strings.Split(line, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "age1") {
			return l
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
