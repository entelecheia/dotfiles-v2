package cli

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
	"github.com/entelecheia/dotfiles-v2/internal/config"
	execrun "github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Command-side assembly for the ai family: the cobra flags a command was
// given become the aisettings engine and managers that run it, plus the
// user-state load/save the persist flags need.

func newAIEngine(cmd *cobra.Command) (*aisettings.Engine, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	homeOverride, _ := cmd.Flags().GetString("home")
	var state *config.UserState
	var err error
	if homeOverride != "" {
		state, err = config.LoadStateForHome(homeOverride)
	} else {
		state, err = config.LoadState()
	}
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	home, _ := os.UserHomeDir()
	if homeOverride != "" {
		home = homeOverride
	}
	root := resolveBackupRoot(cmd, state, home)
	hostname, _ := os.Hostname()
	if idx := strings.Index(hostname, "."); idx > 0 {
		hostname = hostname[:idx]
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return &aisettings.Engine{
		Runner:   execrun.NewRunner(dryRun, logger),
		HomeDir:  home,
		Root:     root,
		Hostname: hostname,
		User:     os.Getenv("USER"),
	}, nil
}

func newAgentsManagerFromCmd(cmd *cobra.Command) *aisettings.AgentsManager {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	home, _ := os.UserHomeDir()
	if over, _ := cmd.Flags().GetString("home"); over != "" {
		home = over
	}
	logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	mgr := aisettings.NewAgentsManager(execrun.NewRunner(dryRun, logger), home)
	mgr.Out = cmd.OutOrStdout()
	return mgr
}

func newHUDManagerFromCmd(cmd *cobra.Command) *aisettings.HUDManager {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	home, _ := os.UserHomeDir()
	if over, _ := cmd.Flags().GetString("home"); over != "" {
		home = over
	}
	logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	return aisettings.NewHUDManager(execrun.NewRunner(dryRun, logger), home)
}

func newCoauthorGuardManagerFromCmd(cmd *cobra.Command) *aisettings.CoauthorGuardManager {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	home, _ := os.UserHomeDir()
	if over, _ := cmd.Flags().GetString("home"); over != "" {
		home = over
	}
	logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	return aisettings.NewCoauthorGuardManager(execrun.NewRunner(dryRun, logger), home)
}

func loadStateForCmd(cmd *cobra.Command) (*config.UserState, error) {
	if homeOverride, _ := cmd.Flags().GetString("home"); homeOverride != "" {
		return config.LoadStateForHome(homeOverride)
	}
	return config.LoadState()
}

func saveStateForCmd(cmd *cobra.Command, state *config.UserState) error {
	if homeOverride, _ := cmd.Flags().GetString("home"); homeOverride != "" {
		return config.SaveStateForHome(homeOverride, state)
	}
	return config.SaveState(state)
}
