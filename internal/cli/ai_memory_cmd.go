package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newAIMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage shared claude-mem integration for Codex, Kimi, and Kiro",
		Long: `Use one claude-mem store across Codex, Kimi Code, and Kiro CLI.

Codex keeps the plugin's native lifecycle hooks. Kimi and Kiro receive the
same MCP recall server plus a workspace-aware transcript capture bridge.`,
	}
	cmd.AddCommand(newAIMemoryInstallCmd())
	cmd.AddCommand(newAIMemoryStatusCmd())
	cmd.AddCommand(newAIMemoryMCPServerCmd())
	cmd.AddCommand(newAIMemoryBridgeCmd())
	return cmd
}

func newAIMemoryInstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "install",
		Short: "Install and start the cross-CLI claude-mem integration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			forceAgents, _ := cmd.Flags().GetBool("force-agents")
			mgr, err := newClaudeMemManagerFromCmd(cmd)
			if err != nil {
				return err
			}
			p := printerFrom(cmd)
			if dryRun {
				config, err := mgr.BuildTranscriptConfigForDisplay()
				if err != nil {
					return err
				}
				p.Header("Claude-mem Integration")
				p.Line("would merge MCP config for Kimi and Kiro")
				p.Line("would install %s", mgr.LaunchdPlistPath())
				p.KV("Kimi sessions", fmt.Sprintf("%d", config["kimi"]))
				p.KV("Kiro sessions", fmt.Sprintf("%d", config["kiro"]))
				return nil
			}
			if _, err := mgr.LocatePlugin(); err != nil {
				return err
			}

			agents := newAgentsManagerFromCmd(cmd)
			instructionsChanged, err := aisettings.EnsureMemoryInstructions(agents.SSOTPath())
			if err != nil {
				return err
			}
			apply, err := agents.Apply(aisettings.ApplyOptions{
				Tools: []string{"codex", "kimi", "kiro"}, Force: forceAgents,
			})
			if err != nil {
				return err
			}

			result, err := mgr.Install(cmd.Context())
			if err != nil {
				return err
			}
			auditAIEventBestEffort(cmd, "ai.memory.install", map[string]any{
				"bridge_path":          result.BridgePath,
				"config_paths":         result.ConfigPaths,
				"instructions_changed": instructionsChanged,
				"kimi_sessions":        result.WatchCount["kimi"],
				"kiro_sessions":        result.WatchCount["kiro"],
			})

			p.Header("Claude-mem Integration")
			p.KV("Plugin", result.PluginRoot)
			p.KV("Bridge", result.BridgePath)
			p.KV("Kimi sessions", fmt.Sprintf("%d", result.WatchCount["kimi"]))
			p.KV("Kiro sessions", fmt.Sprintf("%d", result.WatchCount["kiro"]))
			if instructionsChanged {
				p.Line("Persistent-memory policy added to the agents SSOT.")
			}
			printAgentsApplyResult(p, apply)
			p.Success("Codex, Kimi, and Kiro now share claude-mem")
			return nil
		},
	}
	c.Flags().Bool("force-agents", false, "Back up and overwrite externally edited Codex/Kimi/Kiro instruction targets")
	return c
}

func newAIMemoryStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show claude-mem integration health for all three CLIs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := newClaudeMemManagerFromCmd(cmd)
			if err != nil {
				return err
			}
			ssot := newAgentsManagerFromCmd(cmd).SSOTPath()
			status := mgr.Status(cmd.Context(), ssot)
			p := printerFrom(cmd)
			p.Header("Claude-mem Status")
			plugin := status.PluginRoot
			if plugin == "" {
				plugin = "not found"
			} else if status.PluginVersion != "" {
				plugin += " (" + status.PluginVersion + ")"
			}
			p.KV("Plugin", plugin)
			p.Section("Tools")
			printMemoryState(p, "codex", status.CodexNativeHooks, "native hooks + plugin MCP")
			printMemoryState(p, "kimi", status.KimiMCP, fmt.Sprintf("MCP + %d transcript(s)", status.WatchCount["kimi"]))
			printMemoryState(p, "kiro", status.KiroMCP, fmt.Sprintf("MCP + %d transcript(s)", status.WatchCount["kiro"]))
			p.Section("Shared runtime")
			printMemoryState(p, "instructions", status.InstructionsEnabled, "agents SSOT recall policy")
			printMemoryState(p, "bridge", status.BridgeInstalled && status.BridgeRunning, bridgeStatusDetail(status))
			return nil
		},
	}
}

func printMemoryState(p *Printer, label string, ok bool, detail string) {
	marker := ui.StyleHint.Render(ui.MarkAbsent)
	state := "missing"
	if ok {
		marker = ui.StyleSuccess.Render(ui.MarkPresent)
		state = "ready"
	}
	p.Bullet(marker, fmt.Sprintf("%-13s %-7s %s", ui.StyleValue.Render(label), state, detail))
}

func bridgeStatusDetail(status aisettings.ClaudeMemStatus) string {
	if !status.BridgeInstalled {
		return "LaunchAgent not installed"
	}
	if !status.BridgeRunning {
		return "LaunchAgent installed but stopped"
	}
	return "LaunchAgent running"
}

func newAIMemoryMCPServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "mcp-server",
		Short:  "Run the claude-mem stdio MCP server",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := newClaudeMemManagerFromCmd(cmd)
			if err != nil {
				return err
			}
			return mgr.RunMCPServer(cmd.Context())
		},
	}
}

func newAIMemoryBridgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "bridge",
		Short:  "Run the Kimi/Kiro transcript bridge",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := newClaudeMemManagerFromCmd(cmd)
			if err != nil {
				return err
			}
			return mgr.RunBridge(context.Background())
		},
	}
}

func newClaudeMemManagerFromCmd(cmd *cobra.Command) (*aisettings.ClaudeMemManager, error) {
	home := homeFromCmd(cmd)
	dotPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve dot executable: %w", err)
	}
	dotPath, err = filepath.Abs(dotPath)
	if err != nil {
		return nil, err
	}
	mgr := aisettings.NewClaudeMemManager(home, dotPath, "")
	if nodePath, lookupErr := exec.LookPath("node"); lookupErr == nil {
		if nodePath, absErr := filepath.Abs(nodePath); absErr == nil {
			mgr.NodePath = nodePath
		}
	}
	if bunPath, lookupErr := exec.LookPath("bun"); lookupErr == nil {
		if bunPath, absErr := filepath.Abs(bunPath); absErr == nil {
			mgr.BunPath = bunPath
		}
	}
	return mgr, nil
}
