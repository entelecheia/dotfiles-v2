package ui

import (
	"fmt"
	"runtime"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

// ConfigureWorkspace prompts for workspace settings. Skipped for server profile.
func ConfigureWorkspace(state *config.UserState, profile string, yes bool) error {
	if profile == "server" {
		state.Modules.Workspace.Path = ""
		state.Modules.Workspace.Gdrive = ""
		state.Modules.Workspace.GdriveSymlink = ""
		state.Modules.Workspace.Symlink = ""
		state.Modules.Workspace.Repos = nil
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
		state.Modules.Workspace.GdriveSymlink = ""
		state.Modules.Workspace.Symlink = ""
		state.Modules.Workspace.Repos = nil
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

		// GDrive symlink: convenience symlink for the Drive path
		if state.Modules.Workspace.Gdrive != "" {
			gsDefault := state.Modules.Workspace.GdriveSymlink
			if gsDefault == "" {
				gsDefault = "~/gdrive-workspace"
			}
			state.Modules.Workspace.GdriveSymlink, err = Input("GDrive symlink name (blank to skip)", gsDefault, yes)
			if err != nil {
				return err
			}
		} else {
			state.Modules.Workspace.GdriveSymlink = ""
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
		if symlinkDefault == "" && state.Modules.Workspace.Gdrive != "" && state.Modules.Workspace.GdriveSymlink == "" {
			// Only default to Gdrive when no separate GdriveSymlink is configured
			symlinkDefault = state.Modules.Workspace.Gdrive
		}
		state.Modules.Workspace.Symlink, err = Input("Symlink target (blank to skip)", symlinkDefault, false)
		if err != nil {
			return err
		}
	}

	// --- Workspace git repos (optional) ---
	if !yes {
		configureRepos, err := ConfirmBool("Configure workspace git repos?", len(state.Modules.Workspace.Repos) > 0, false)
		if err != nil {
			return err
		}
		if configureRepos {
			oldRepos := state.Modules.Workspace.Repos
			state.Modules.Workspace.Repos = nil
			for _, name := range []string{"work", "vault"} {
				existingRemote := ""
				for _, r := range oldRepos {
					if r.Name == name {
						existingRemote = r.Remote
						break
					}
				}
				remote, err := Input(
					fmt.Sprintf("Git remote for %s/%s (blank to skip)", state.Modules.Workspace.Path, name),
					existingRemote, false)
				if err != nil {
					return err
				}
				if remote != "" {
					state.Modules.Workspace.Repos = append(state.Modules.Workspace.Repos, config.RepoConfig{
						Name:   name,
						Remote: remote,
					})
				}
			}
		}
	}
	return nil
}
