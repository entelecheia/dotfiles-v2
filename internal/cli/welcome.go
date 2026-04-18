package cli

import (
	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

// printWelcome shows a friendly getting-started guide when invoked without args.
func printWelcome(cmd *cobra.Command, version, commit string) {
	p := printerFrom(cmd)
	v, c := ResolveVersion(version, commit)
	p.Header("dotfiles " + v + " (" + c + ")")
	p.Line("  %s", ui.StyleValue.Render("Declarative user environment & workspace manager."))

	// Detect status
	state, _ := config.LoadState()
	initialized := state != nil && state.Name != ""

	if !initialized {
		p.Section("Not configured yet")
		p.Line("  %s  %s", ui.StyleKey.Render("1."), ui.StyleValue.Render("dotfiles init"))
		p.Line("     %s", ui.StyleHint.Render("Interactive setup — asks for name, email, profile, modules"))
		p.Line("  %s  %s", ui.StyleKey.Render("2."), ui.StyleValue.Render("dotfiles apply"))
		p.Line("     %s", ui.StyleHint.Render("Apply configuration to the system"))
	} else {
		p.Section("Current configuration")
		p.KV("Profile", state.Profile+" ("+state.Name+")")
	}

	p.Section("Common commands")
	printWelcomeCmd(p, "dotfiles status", "Full environment status at a glance")
	printWelcomeCmd(p, "dotfiles apply", "Apply configuration")
	printWelcomeCmd(p, "dotfiles check", "Show pending changes without applying")
	printWelcomeCmd(p, "dotfiles config", "Display current config")
	printWelcomeCmd(p, "dotfiles clean", "Remove junk directories (node_modules, caches)")
	printWelcomeCmd(p, "dotfiles sync", "Sync binaries with remote server via rsync")
	printWelcomeCmd(p, "dotfiles clone", "Sync workspace with Google Drive via rclone")
	printWelcomeCmd(p, "dotfiles apps install", "Install macOS cask apps (interactive picker)")
	printWelcomeCmd(p, "dotfiles apps backup", "Snapshot macOS app settings")
	printWelcomeCmd(p, "dotfiles profile backup", "Version-snapshot config + app lists + secrets")
	printWelcomeCmd(p, "dotfiles open <project>", "Launch/resume a tmux workspace")
	printWelcomeCmd(p, "dotfiles list", "Show registered projects")

	p.Blank()
	p.Line("  %s", ui.StyleHint.Render("Tip: 'dot' is an alias for 'dotfiles' (e.g., 'dot apply')"))
	p.Line("  %s", ui.StyleHint.Render("See 'dotfiles usecase' for detailed workflows"))
	p.Line("  %s", ui.StyleHint.Render("See 'dotfiles help' for all commands"))
	p.Blank()
}

func printWelcomeCmd(p *Printer, cmd, desc string) {
	p.Line("  %s  %s",
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
			printUsecases(cmd)
			return nil
		},
	}
}

func printUsecases(cmd *cobra.Command) {
	p := printerFrom(cmd)
	p.Header("dotfiles Use Cases")
	p.Line("  %s", ui.StyleHint.Render("Note: 'dot' is a shorthand alias for 'dotfiles'."))
	p.Line("  %s", ui.StyleHint.Render("Examples below use 'dotfiles' — substitute 'dot' if you prefer."))

	section(p, "1. First-time setup on a new machine",
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

	section(p, "2. Safe apply — preserve existing environment",
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

	section(p, "3. Daily workspace — tmux + AI tools",
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

	section(p, "4. Workspace cleanup",
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

	section(p, "5. Remote server sync (rsync)",
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

	section(p, "6. Google Drive sync (rclone)",
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

	section(p, "7. Google Drive exclusions (macOS)",
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

	section(p, "8. Secrets management (age encryption)",
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

	section(p, "9. macOS app management (cask install + settings backup)",
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

	section(p, "10. Profile snapshots (versioned config backup)",
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

	section(p, "11. Cross-machine migration",
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

	section(p, "12. Prompt style",
		"Switch between a minimal and a rich Starship prompt.",
		[]usecase{
			{"dotfiles reconfigure",
				"Re-run init (Prompt style: minimal / rich)"},
			{"dotfiles apply --module terminal",
				"Deploy the selected starship.toml immediately"},
		})

	section(p, "13. Updates and reconfiguration",
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

	section(p, "14. GPU server / DGX provisioning",
		"Deploy on a fresh GPU server — auto-detects NVIDIA + CUDA.",
		[]usecase{
			{"curl -fsSL .../install.sh | bash",
				"Install binary"},
			{"dotfiles init --yes",
				"Auto-selects 'server' profile when GPU detected"},
			{"dotfiles apply --yes",
				"Apply server profile (no workspace, fonts, gpg, secrets)"},
		})

	section(p, "15. Troubleshooting",
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

	p.Section("Global flags (work with most commands)")
	printFlag(p, "--yes", "Unattended mode, skip all prompts")
	printFlag(p, "--dry-run", "Preview without making changes")
	printFlag(p, "--profile", "Override profile (minimal|full|server)")
	printFlag(p, "--module", "Run specific modules only (repeatable)")
	printFlag(p, "--config", "Custom config YAML path")
	printFlag(p, "--home", "Override home directory (admin setup)")

	p.Blank()
	p.Line("  %s", ui.StyleHint.Render("Run 'dotfiles <command> --help' for detailed options"))
	p.Blank()
}

type usecase struct {
	cmd  string
	desc string
}

func section(p *Printer, title, intro string, cases []usecase) {
	p.Section(title)
	p.Line("  %s", ui.StyleHint.Render(intro))
	for _, c := range cases {
		p.Line("  $ %s", ui.StyleValue.Bold(true).Render(c.cmd))
		p.Line("    %s", ui.StyleHint.Render(c.desc))
	}
}

func printFlag(p *Printer, flag, desc string) {
	p.Line("  %s  %s",
		ui.StyleKey.Render(flag),
		ui.StyleHint.Render(desc))
}
