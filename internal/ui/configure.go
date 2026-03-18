package ui

import (
	"fmt"
	"runtime"

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

// ConfigureSSH prompts for SSH key name. Returns updated state.
func ConfigureSSH(state *config.UserState, yes bool) error {
	fmt.Println("\n--- SSH ---")

	sshKeyDefault := state.SSH.KeyName
	if sshKeyDefault == "" {
		if state.GithubUser != "" {
			sshKeyDefault = "id_ed25519_" + state.GithubUser
		} else {
			sshKeyDefault = "id_ed25519"
		}
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
	}
	if state.Modules.Fonts.Family != "" {
		fmt.Printf("  Font family:  %s\n", state.Modules.Fonts.Family)
	}
}
