package cli

import (
	"fmt"
	"log/slog"
	"os"
	osexec "os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/workspace"
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
			p := printerFrom(cmd)

			if !force {
				p.Raw("Stop session %q? [y/N] ", name)
				var answer string
				fmt.Scanln(&answer)
				if strings.ToLower(strings.TrimSpace(answer)) != "y" {
					p.Line("Aborted.")
					return nil
				}
			}

			runner := exec.NewRunner(false, slog.Default())
			res, err := runner.RunQuery(cmd.Context(), "tmux", "kill-session", "-t", name)
			if err != nil {
				stderr := strings.TrimSpace(res.Stderr)
				if stderr == "" {
					stderr = err.Error()
				}
				return fmt.Errorf("stopping session %q: %s", name, stderr)
			}
			p.Line("Session %q stopped.", name)
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
			runner := exec.NewRunner(false, slog.Default())
			res, err := runner.RunQuery(cmd.Context(), "tmux", "list-sessions", "-F", "#{session_name}")
			if err == nil {
				for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
					if line != "" {
						activeSessions[line] = true
					}
				}
			}

			p := printerFrom(cmd)
			p.Line("Projects (%d):", len(cfg.Projects))
			if len(cfg.Projects) == 0 {
				p.Line("  (none — use 'dotfiles register <name>' to add one)")
			}
			for _, proj := range cfg.Projects {
				layout := cfg.EffectiveLayout(&proj)
				theme := cfg.EffectiveTheme(&proj)
				status := " "
				if activeSessions[proj.Name] {
					status = "*"
					delete(activeSessions, proj.Name)
				}
				p.Line("  %s %-18s %s  (layout=%s, theme=%s)", status, proj.Name, proj.Path, layout, theme)
			}

			// Show other active sessions not in our project list
			if len(activeSessions) > 0 {
				p.Line("\nOther tmux sessions:")
				for name := range activeSessions {
					p.Line("    %-18s (not registered)", name)
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
			printerFrom(cmd).Line("Registered project %q → %s", name, proj.Path)
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

			printerFrom(cmd).Line("Removed project %q", name)
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
			p := printerFrom(cmd)
			p.Line("Available layouts:")
			p.Line("")
			p.Line("  dev (default)")
			p.Line("    5-pane laptop-friendly layout")
			p.Line("    Claude + monitor + files | lazygit + shell")
			p.Line("")
			p.Line("  claude")
			p.Line("    7-pane Claude-focused layout")
			p.Line("    Claude + monitor + files + remote | lazygit + shell + logs")
			p.Line("")
			p.Line("  monitor")
			p.Line("    4-pane server monitoring layout")
			p.Line("    monitor + shell | lazygit + logs")
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
			p := printerFrom(cmd)

			p.Line("Workspace tool status:")
			p.Line("")

			status := workspace.CheckDeps(runner)

			for _, name := range status.Installed {
				path, _ := osexec.LookPath(name)
				p.Line("  ✓ %-12s %s", name, path)
			}
			for _, name := range status.Required {
				p.Line("  ✗ %-12s (required — run 'dotfiles apply' to install)", name)
			}
			for _, name := range status.Missing {
				p.Line("  ○ %-12s (optional — fallback available)", name)
			}

			// tmux version
			p.Line("")
			if res, err := runner.RunQuery(cmd.Context(), "tmux", "-V"); err != nil {
				p.Line("  tmux: not available")
			} else {
				p.Line("  %s", strings.TrimSpace(res.Stdout))
			}

			// Terminal info
			p.Line("  TERM: %s", os.Getenv("TERM"))
			if tp := os.Getenv("TERM_PROGRAM"); tp != "" {
				p.Line("  TERM_PROGRAM: %s", tp)
			}
			p.Line("  SHELL: %s", os.Getenv("SHELL"))

			// Workspace config
			configPath, _ := workspace.ConfigPath()
			dataDir, _ := workspace.DataDir()
			p.Line("\n  Config:  %s", configPath)
			p.Line("  Scripts: %s", dataDir)

			return nil
		},
	}
}
