package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Self-update dotfiles and re-apply",
		Long:  "Pull the latest dotfiles from the remote repository, then apply the configuration.",
		RunE:  runUpdate,
	}
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	runner := exec.NewRunner(false, logger)

	// Determine repo directory: use executable's directory or fall back to cwd.
	repoDir, err := repoDirectory()
	if err != nil {
		return fmt.Errorf("detecting repo directory: %w", err)
	}

	fmt.Printf("Updating dotfiles repo at: %s\n", repoDir)
	result, err := runner.Run(ctx, "git", "-C", repoDir, "pull", "--ff-only")
	if err != nil {
		return fmt.Errorf("git pull: %w", err)
	}
	if result.Stdout != "" {
		fmt.Print(result.Stdout)
	}

	fmt.Println()
	fmt.Println("Applying updated configuration...")
	return runApply(cmd, nil)
}

// repoDirectory returns the dotfiles repository root to run git pull in.
// It checks DOTFILES_REPO_DIR env var first, then falls back to the current
// working directory.
func repoDirectory() (string, error) {
	if v := os.Getenv("DOTFILES_REPO_DIR"); v != "" {
		return v, nil
	}
	return os.Getwd()
}
