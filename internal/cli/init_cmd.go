package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Interactive setup for dotfiles",
		Long:  "Collect user preferences and save them to the dotfiles state file.",
		RunE:  runInit,
	}
}

func runInit(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")

	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	// If state already has data, ask whether to reconfigure.
	if state.Name != "" && !yes {
		fmt.Printf("Current configuration:\n")
		printStateSnapshot(state)
		fmt.Println()

		reconfigure, err := ui.ConfirmBool("Reconfigure existing settings?", false, false)
		if err != nil {
			return err
		}
		if !reconfigure {
			fmt.Println("Keeping existing configuration.")
			return nil
		}
	}

	isMacOS := runtime.GOOS == "darwin"

	// --- Identity ---
	name, err := ui.Input("Full name", state.Name, yes)
	if err != nil {
		return err
	}

	email, err := ui.Input("Email address", state.Email, yes)
	if err != nil {
		return err
	}

	githubUser, err := ui.Input("GitHub username", state.GithubUser, yes)
	if err != nil {
		return err
	}

	tz := state.Timezone
	if tz == "" {
		tz = "Asia/Seoul"
	}
	timezone, err := ui.Input("Timezone", tz, yes)
	if err != nil {
		return err
	}

	// --- Profile ---
	profileDefault := state.Profile
	if profileDefault == "" {
		profileDefault = "full"
	}
	profile, err := ui.Select("Profile", config.AvailableProfiles(), profileDefault, yes)
	if err != nil {
		return err
	}

	// --- Workspace ---
	enableWorkspace, err := ui.ConfirmBool("Enable workspace module?", state.Modules.Workspace.Path != "", yes)
	if err != nil {
		return err
	}

	workspacePath := state.Modules.Workspace.Path
	gdrivePath := state.Modules.Workspace.Gdrive
	if enableWorkspace {
		if workspacePath == "" {
			workspacePath = "~/ai-workspace"
		}
		workspacePath, err = ui.Input("Workspace path", workspacePath, yes)
		if err != nil {
			return err
		}

		if isMacOS {
			gdrivePath, err = ui.Input("Google Drive path (leave blank to skip)", gdrivePath, yes)
			if err != nil {
				return err
			}
		}
	}

	// --- AI Tools ---
	aiToolsDefault := state.Modules.AITools
	if state.Name == "" {
		aiToolsDefault = true // default true for fresh setups
	}
	enableAITools, err := ui.ConfirmBool("Enable AI tools (Claude Code, etc.)?", aiToolsDefault, yes)
	if err != nil {
		return err
	}

	// --- Warp terminal (macOS only) ---
	enableWarp := state.Modules.Warp
	if isMacOS {
		enableWarp, err = ui.ConfirmBool("Enable Warp terminal?", state.Modules.Warp, yes)
		if err != nil {
			return err
		}
	}

	// --- SSH key name ---
	sshKeyDefault := state.SSH.KeyName
	if sshKeyDefault == "" {
		if githubUser != "" {
			sshKeyDefault = "id_ed25519_" + githubUser
		} else {
			sshKeyDefault = "id_ed25519"
		}
	}
	sshKeyName, err := ui.Input("SSH key name", sshKeyDefault, yes)
	if err != nil {
		return err
	}

	// --- Font family (full profile only) ---
	fontFamily := state.Modules.Fonts.Family
	if profile == "full" {
		if fontFamily == "" {
			fontFamily = "FiraCode"
		}
		fontFamily, err = ui.Select("Font family", []string{"FiraCode", "JetBrainsMono", "Hack"}, fontFamily, yes)
		if err != nil {
			return err
		}
	}

	// --- Persist ---
	state.Name = name
	state.Email = email
	state.GithubUser = githubUser
	state.Timezone = timezone
	state.Profile = profile
	state.SSH.KeyName = sshKeyName
	state.Modules.AITools = enableAITools
	state.Modules.Warp = enableWarp
	state.Modules.Fonts.Family = fontFamily

	if enableWorkspace {
		state.Modules.Workspace.Path = workspacePath
		state.Modules.Workspace.Gdrive = gdrivePath
	} else {
		state.Modules.Workspace.Path = ""
		state.Modules.Workspace.Gdrive = ""
	}

	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	fmt.Println()
	fmt.Println("Configuration saved.")
	fmt.Printf("  State file: %s\n", config.StatePath())
	fmt.Println()
	printStateSnapshot(state)
	fmt.Println()
	fmt.Println("Run 'dotfiles apply' to apply the configuration.")
	return nil
}

func printStateSnapshot(state *config.UserState) {
	fmt.Printf("  Name:         %s\n", state.Name)
	fmt.Printf("  Email:        %s\n", state.Email)
	fmt.Printf("  GitHub:       %s\n", state.GithubUser)
	fmt.Printf("  Timezone:     %s\n", state.Timezone)
	fmt.Printf("  Profile:      %s\n", state.Profile)
	fmt.Printf("  SSH key:      %s\n", state.SSH.KeyName)
	fmt.Printf("  AI tools:     %v\n", state.Modules.AITools)
	fmt.Printf("  Warp:         %v\n", state.Modules.Warp)
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
