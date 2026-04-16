package cli

import (
	"fmt"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
	"github.com/spf13/cobra"
)

// printWelcome shows a friendly getting-started guide when invoked without args.
func printWelcome(version, commit string) {
	v, c := ResolveVersion(version, commit)
	fmt.Println()
	fmt.Println(ui.StyleHeader.Render(" dotfiles " + v + " (" + c + ") "))
	fmt.Println()
	fmt.Println("  " + ui.StyleValue.Render("Declarative user environment & workspace manager."))
	fmt.Println()

	// Detect status
	state, _ := config.LoadState()
	initialized := state != nil && state.Name != ""

	if !initialized {
		fmt.Println(ui.StyleSection.Render("▸ Not configured yet"))
		fmt.Println()
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("1."), ui.StyleValue.Render("dotfiles init"))
		fmt.Printf("     %s\n", ui.StyleHint.Render("Interactive setup — asks for name, email, profile, modules"))
		fmt.Println()
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("2."), ui.StyleValue.Render("dotfiles apply"))
		fmt.Printf("     %s\n", ui.StyleHint.Render("Apply configuration to the system"))
		fmt.Println()
	} else {
		fmt.Println(ui.StyleSection.Render("▸ Current configuration"))
		fmt.Println()
		fmt.Printf("  %s  %s  %s\n", ui.StyleKey.Render("Profile:"), ui.StyleValue.Render(state.Profile), ui.StyleHint.Render("("+state.Name+")"))
		fmt.Println()
	}

	fmt.Println(ui.StyleSection.Render("▸ Common commands"))
	fmt.Println()
	printWelcomeCmd("dotfiles status", "Full environment status at a glance")
	printWelcomeCmd("dotfiles apply", "Apply configuration")
	printWelcomeCmd("dotfiles check", "Show pending changes without applying")
	printWelcomeCmd("dotfiles config", "Display current config")
	printWelcomeCmd("dotfiles clean", "Remove junk directories (node_modules, caches)")
	printWelcomeCmd("dotfiles sync", "Sync binaries with remote server via rsync")
	printWelcomeCmd("dotfiles clone", "Sync workspace with Google Drive via rclone")
	printWelcomeCmd("dotfiles apps install", "Install macOS cask apps (interactive picker)")
	printWelcomeCmd("dotfiles apps backup", "Snapshot macOS app settings")
	printWelcomeCmd("dotfiles profile backup", "Version-snapshot config + app lists + secrets")
	printWelcomeCmd("dotfiles open <project>", "Launch/resume a tmux workspace")
	printWelcomeCmd("dotfiles list", "Show registered projects")
	fmt.Println()
	fmt.Println(ui.StyleHint.Render("  Tip: 'dot' is an alias for 'dotfiles' (e.g., 'dot apply')"))
	fmt.Println(ui.StyleHint.Render("  See 'dotfiles usecase' for detailed workflows"))
	fmt.Println(ui.StyleHint.Render("  See 'dotfiles help' for all commands"))
	fmt.Println()
}

func printWelcomeCmd(cmd, desc string) {
	fmt.Printf("  %s  %s\n",
		ui.StyleValue.Bold(true).Render(cmd),
		ui.StyleHint.Render("— "+desc))
}

// newUsecaseCmd returns a command that prints detailed workflow examples.
func newUsecaseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "usecase",
		Short: "Show detailed use cases and workflows",
		Long:  "Walk through common workflows with real commands: first-time setup, daily use, sync, workspace, upgrades, troubleshooting.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			printUsecases()
			return nil
		},
	}
}

func printUsecases() {
	fmt.Println()
	fmt.Println(ui.StyleHeader.Render(" dotfiles Use Cases "))
	fmt.Println()
	fmt.Println(ui.StyleHint.Render("  Note: 'dot' is a shorthand alias for 'dotfiles'."))
	fmt.Println(ui.StyleHint.Render("  Examples below use 'dotfiles' — substitute 'dot' if you prefer."))
	fmt.Println()

	section("1. First-time setup on a new machine",
		"Fresh install — get from zero to productive in minutes.",
		[]usecase{
			{"curl -fsSL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/install.sh | bash",
				"Download binary from GitHub Releases"},
			{"dotfiles init",
				"Interactive setup (auto-detects git config, timezone, SSH keys)"},
			{"dotfiles apply --dry-run",
				"Preview all changes before applying"},
			{"dotfiles apply",
				"Apply to system (installs packages, shell, git, etc.)"},
		})

	section("2. Safe apply — preserve existing environment",
		"Avoid overwriting your current config.",
		[]usecase{
			{"dotfiles preflight --check-only",
				"Scan environment, report conflicts"},
			{"dotfiles preflight",
				"Generate config file from detected environment"},
			{"dotfiles check",
				"Show which modules have pending changes"},
			{"dotfiles diff --module shell",
				"Preview changes for a specific module"},
			{"dotfiles apply --module shell --module git",
				"Apply only selected modules"},
		})

	section("3. Daily workspace — tmux + AI tools",
		"Launch multi-panel dev workspaces. ('dot' is an alias for 'dotfiles')",
		[]usecase{
			{"dotfiles open myproject",
				"Auto-register current dir and launch tmux workspace"},
			{"dotfiles open myproject --layout claude",
				"Launch with specific layout override"},
			{"dotfiles register myproject ~/dev/app --layout claude",
				"Register project path with default layout"},
			{"dotfiles list",
				"Show all registered projects and active sessions"},
			{"dotfiles layouts",
				"Show available pane layouts (dev, claude, monitor)"},
			{"dotfiles doctor",
				"Check which workspace tools are installed"},
			{"dotfiles stop myproject",
				"Stop a running tmux session"},
		})

	section("4. Workspace cleanup",
		"Remove junk that wastes disk and breaks Drive sync. _sys/ is always protected.",
		[]usecase{
			{"dotfiles clean",
				"Scan and preview (node_modules, __pycache__, .venv, .cache, .DS_Store)"},
			{"dotfiles clean --yes",
				"Actually delete safe patterns"},
			{"dotfiles clean --all --yes",
				"Include risky patterns (dist/, build/, out/, target/)"},
			{"dotfiles status",
				"Full environment dashboard (system, modules, secrets, sync, workspace)"},
		})

	section("5. Remote server sync (rsync)",
		"Sync binary files with Ubuntu server over SSH. Text files use git only.",
		[]usecase{
			{"dotfiles sync setup",
				"One-time setup: rsync, SSH key, remote host, binary extensions, scheduler"},
			{"dotfiles sync",
				"Pull-then-push (default): pull newer binaries, then push local"},
			{"dotfiles sync pull",
				"Pull only: remote → local (--update, safe)"},
			{"dotfiles sync push",
				"Push only: local → remote (--delete-after, local is authority)"},
			{"dotfiles sync status",
				"Show sync health, scheduler state, last result"},
		})

	section("6. Google Drive sync (rclone)",
		"Keep workspace synced across Macs via rclone.",
		[]usecase{
			{"dotfiles clone setup",
				"One-time setup: install rclone, configure remote, deploy filter & scheduler"},
			{"dotfiles clone",
				"Pull from remote (safe, read-only on remote)"},
			{"dotfiles clone push",
				"Push local changes to remote"},
			{"dotfiles clone all",
				"Bidirectional sync (pull then push)"},
			{"dotfiles clone status",
				"Show sync health, last run, scheduler state"},
		})

	section("7. Google Drive exclusions (macOS)",
		"Exclude heavy directories (node_modules, build caches) from Drive sync.",
		[]usecase{
			{"dotfiles drive-exclude scan",
				"Find excludable directories under ~/workspace"},
			{"dotfiles drive-exclude apply --dry-run",
				"Preview exclusions"},
			{"dotfiles drive-exclude apply --yes",
				"Apply xattr to all pending directories"},
			{"dotfiles drive-exclude add ~/myproject/node_modules",
				"Manually exclude a specific path"},
		})

	section("8. Secrets management (age encryption)",
		"Encrypt SSH keys and shell secrets with age.",
		[]usecase{
			{"dotfiles secrets init --scaffold",
				"Create empty shell secrets template (0600), then encrypt SSH key + any shell secrets"},
			{"dotfiles secrets init",
				"Encrypt existing SSH key + shell secrets"},
			{"dotfiles secrets status",
				"Show decrypted/encrypted file status"},
			{"dotfiles secrets backup ~/backup",
				"Copy encrypted files to backup location"},
			{"dotfiles secrets restore ~/backup",
				"Decrypt from backup on new machine"},
		})

	section("9. macOS app management (cask install + settings backup)",
		"Install apps from an embedded catalog; back up and restore per-app settings.",
		[]usecase{
			{"dotfiles apps list",
				"Browse the cask catalog (★ defaults, ✓ installed)"},
			{"dotfiles apps install",
				"Interactive checkbox picker → save selection → install missing casks"},
			{"dotfiles apps install raycast obsidian",
				"Install specific casks by token"},
			{"dotfiles apps install --defaults",
				"Install the catalog's 20-app default set"},
			{"dotfiles apps status",
				"Show install + backup presence for each tracked app"},
			{"dotfiles apps backup",
				"Snapshot per-app settings (plists, Application Support) to backup root"},
			{"dotfiles apps backup moom hazel --to ~/backup",
				"Back up specific apps to a custom path"},
			{"dotfiles apps restore",
				"Restore all backed-up settings (confirms first, flushes cfprefsd)"},
		})

	section("10. Profile snapshots (versioned config backup)",
		"Snapshot config.yaml, cask lists, and optional secrets into timestamped versions.",
		[]usecase{
			{"dotfiles profile backup",
				"Create a new version snapshot under <backup-root>/profiles/<host>/"},
			{"dotfiles profile backup --tag pre-migration --include-secrets",
				"Tag the snapshot and include ~/.ssh/age_key*"},
			{"dotfiles profile list",
				"List all snapshots for this host (★ marks latest)"},
			{"dotfiles profile restore",
				"Restore the latest snapshot (config.yaml → ~/.config/dotfiles/)"},
			{"dotfiles profile restore --version 20260416T083412Z --include-secrets",
				"Restore a specific version including age keys"},
			{"dotfiles profile prune --keep 5",
				"Delete snapshots older than the 5 most recent"},
		})

	section("11. Cross-machine migration",
		"Move your full setup to a new Mac in minutes.",
		[]usecase{
			{"dotfiles profile backup --tag pre-migration --include-secrets",
				"[old machine] Snapshot config + age keys to Drive"},
			{"dotfiles apps backup",
				"[old machine] Snapshot per-app settings to Drive"},
			{"dotfiles profile restore --include-secrets",
				"[new machine] Restore config.yaml + age keys from latest snapshot"},
			{"dotfiles apply",
				"[new machine] Apply: packages, shell, git, cask installs, workspace…"},
			{"dotfiles apps restore",
				"[new machine] Restore plists and app settings"},
		})

	section("12. Prompt style",
		"Switch between a minimal and a rich Starship prompt.",
		[]usecase{
			{"dotfiles reconfigure",
				"Re-run init (Prompt style: minimal / rich)"},
			{"dotfiles apply --module terminal",
				"Deploy the selected starship.toml immediately"},
		})

	section("13. Updates and reconfiguration",
		"Keep the tool and config current.",
		[]usecase{
			{"dotfiles update --check",
				"Check for newer version"},
			{"dotfiles update",
				"Download and install latest release"},
			{"dotfiles reconfigure",
				"Re-run init prompts with current values as defaults"},
			{"dotfiles version",
				"Show installed version and build info"},
		})

	section("14. GPU server / DGX provisioning",
		"Deploy on a fresh GPU server — auto-detects NVIDIA + CUDA.",
		[]usecase{
			{"curl -fsSL .../install.sh | bash",
				"Install binary"},
			{"dotfiles init --yes",
				"Auto-selects 'server' profile when GPU detected"},
			{"dotfiles apply --yes",
				"Apply server profile (no workspace, fonts, gpg, secrets)"},
		})

	section("15. Troubleshooting",
		"Diagnose and recover from issues.",
		[]usecase{
			{"dotfiles doctor",
				"Check workspace tool installation"},
			{"dotfiles status",
				"Full environment dashboard at a glance"},
			{"dotfiles config",
				"Show loaded configuration and system info"},
			{"dotfiles clone reconnect",
				"Fix expired Google Drive authentication"},
			{"dotfiles clone log 100",
				"View recent rclone sync log entries"},
			{"dotfiles sync log 100",
				"View recent rsync sync log entries"},
		})

	fmt.Println(ui.StyleSection.Render("▸ Global flags (work with most commands)"))
	fmt.Println()
	printFlag("--yes", "Unattended mode, skip all prompts")
	printFlag("--dry-run", "Preview without making changes")
	printFlag("--profile", "Override profile (minimal|full|server)")
	printFlag("--module", "Run specific modules only (repeatable)")
	printFlag("--config", "Custom config YAML path")
	printFlag("--home", "Override home directory (admin setup)")
	fmt.Println()

	fmt.Println(ui.StyleHint.Render("  Run 'dotfiles <command> --help' for detailed options"))
	fmt.Println()
}

type usecase struct {
	cmd  string
	desc string
}

func section(title, intro string, cases []usecase) {
	fmt.Println(ui.StyleSection.Render("▸ " + title))
	fmt.Println("  " + ui.StyleHint.Render(intro))
	fmt.Println()
	for _, c := range cases {
		fmt.Printf("  $ %s\n", ui.StyleValue.Bold(true).Render(c.cmd))
		fmt.Printf("    %s\n", ui.StyleHint.Render(c.desc))
	}
	fmt.Println()
}

func printFlag(flag, desc string) {
	fmt.Printf("  %s  %s\n",
		ui.StyleKey.Render(flag),
		ui.StyleHint.Render(desc))
}

