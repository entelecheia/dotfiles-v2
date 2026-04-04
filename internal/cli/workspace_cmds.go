package cli

import (
	"fmt"
	"log/slog"
	"os"
	osexec "os/exec"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/workspace"
	"github.com/spf13/cobra"
)

// ── stop ────────────────────────────────────────────────────────────────────

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <project>",
		Short: "Stop a tmux workspace session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			force, _ := cmd.Flags().GetBool("force")

			if !force {
				fmt.Printf("Stop session %q? [y/N] ", name)
				var answer string
				fmt.Scanln(&answer)
				if strings.ToLower(strings.TrimSpace(answer)) != "y" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			out, err := osexec.Command("tmux", "kill-session", "-t", name).CombinedOutput()
			if err != nil {
				return fmt.Errorf("stopping session %q: %s", name, strings.TrimSpace(string(out)))
			}
			fmt.Printf("Session %q stopped.\n", name)
			return nil
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Skip confirmation")
	return cmd
}

// ── list ────────────────────────────────────────────────────────────────────

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List registered projects and active tmux sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Registered projects first (primary data)
			cfg, err := workspace.LoadConfig()
			if err != nil {
				return err
			}

			// Collect active session names for status display
			activeSessions := make(map[string]bool)
			out, err := osexec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
			if err == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					if line != "" {
						activeSessions[line] = true
					}
				}
			}

			fmt.Printf("Projects (%d):\n", len(cfg.Projects))
			if len(cfg.Projects) == 0 {
				fmt.Println("  (none — use 'dotfiles register <name>' to add one)")
			}
			for _, p := range cfg.Projects {
				layout := cfg.EffectiveLayout(&p)
				theme := cfg.EffectiveTheme(&p)
				status := " "
				if activeSessions[p.Name] {
					status = "*"
					delete(activeSessions, p.Name)
				}
				fmt.Printf("  %s %-18s %s  (layout=%s, theme=%s)\n", status, p.Name, p.Path, layout, theme)
			}

			// Show other active sessions not in our project list
			if len(activeSessions) > 0 {
				fmt.Printf("\nOther tmux sessions:\n")
				for name := range activeSessions {
					fmt.Printf("    %-18s (not registered)\n", name)
				}
			}

			return nil
		},
	}
}

// ── register ────────────────────────────────────────────────────────────────

func newRegisterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register <name> [path]",
		Short: "Register a project for workspace management",
		Long:  "Register a directory as a named project. Defaults to current directory.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := "."
			if len(args) > 1 {
				path = args[1]
			}

			layout, _ := cmd.Flags().GetString("layout")
			theme, _ := cmd.Flags().GetString("theme")

			if layout != "" && !workspace.IsValidLayout(layout) {
				return fmt.Errorf("unknown layout %q; valid: %v", layout, workspace.ValidLayouts())
			}
			if theme != "" && !workspace.IsValidTheme(theme) {
				return fmt.Errorf("unknown theme %q; valid: %v", theme, workspace.ValidThemes())
			}

			cfg, err := workspace.LoadConfig()
			if err != nil {
				return err
			}

			if err := cfg.AddProject(name, path, layout, theme); err != nil {
				return err
			}

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			proj := cfg.FindProject(name)
			fmt.Printf("Registered project %q → %s\n", name, proj.Path)
			return nil
		},
	}
	cmd.Flags().String("layout", "", "Default layout for this project")
	cmd.Flags().String("theme", "", "Default theme for this project")
	return cmd
}

// ── unregister ──────────────────────────────────────────────────────────────

func newUnregisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "unregister <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a registered project",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			cfg, err := workspace.LoadConfig()
			if err != nil {
				return err
			}

			if !cfg.RemoveProject(name) {
				return fmt.Errorf("project %q not found", name)
			}

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("Removed project %q\n", name)
			return nil
		},
	}
}

// ── layouts ─────────────────────────────────────────────────────────────────

func newLayoutsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "layouts",
		Short: "List available workspace layouts",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Available layouts:")
			fmt.Println()
			fmt.Println("  dev (default)")
			fmt.Println("    5-pane laptop-friendly layout")
			fmt.Println("    Claude + monitor + files | lazygit + shell")
			fmt.Println()
			fmt.Println("  claude")
			fmt.Println("    7-pane Claude-focused layout")
			fmt.Println("    Claude + monitor + files + remote | lazygit + shell + logs")
			fmt.Println()
			fmt.Println("  monitor")
			fmt.Println("    4-pane server monitoring layout")
			fmt.Println("    monitor + shell | lazygit + logs")
		},
	}
}

// ── doctor ──────────────────────────────────────────────────────────────────

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check workspace tool installation status",
		RunE: func(cmd *cobra.Command, args []string) error {
			runner := exec.NewRunner(false, slog.Default())

			fmt.Println("Workspace tool status:")
			fmt.Println()

			status := workspace.CheckDeps(runner)

			for _, name := range status.Installed {
				path, _ := osexec.LookPath(name)
				fmt.Printf("  ✓ %-12s %s\n", name, path)
			}
			for _, name := range status.Required {
				fmt.Printf("  ✗ %-12s (required — run 'dotfiles apply' to install)\n", name)
			}
			for _, name := range status.Missing {
				fmt.Printf("  ○ %-12s (optional — fallback available)\n", name)
			}

			// tmux version
			fmt.Println()
			out, err := osexec.Command("tmux", "-V").Output()
			if err != nil {
				fmt.Println("  tmux: not available")
			} else {
				fmt.Printf("  %s\n", strings.TrimSpace(string(out)))
			}

			// Terminal info
			fmt.Printf("  TERM: %s\n", os.Getenv("TERM"))
			if tp := os.Getenv("TERM_PROGRAM"); tp != "" {
				fmt.Printf("  TERM_PROGRAM: %s\n", tp)
			}
			fmt.Printf("  SHELL: %s\n", os.Getenv("SHELL"))

			// Workspace config
			configPath, _ := workspace.ConfigPath()
			dataDir, _ := workspace.DataDir()
			fmt.Printf("\n  Config:  %s\n", configPath)
			fmt.Printf("  Scripts: %s\n", dataDir)

			return nil
		},
	}
}
