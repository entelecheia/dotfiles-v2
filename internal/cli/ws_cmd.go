package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
	"github.com/entelecheia/dotfiles-v2/internal/ws"
)

// newWorkspaceDualCmd returns the `dot ws` command with subcommands for
// dual-workspace (work + gdrive) folder operations.
func newWorkspaceDualCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ws",
		Short: "Dual-workspace (work + gdrive) folder operations",
		Long: `Operate on both ~/workspace/work/ and ~/gdrive-workspace/work/ simultaneously.

Subcommands keep the two trees in structural sync:
  mkdir      Create a folder on both sides
  mv         Rename/move on both sides
  rm         Remove on both sides (use --recursive for non-empty)
  audit      Report structural mismatches (read-only)
  reconcile  Interactively resolve mismatches`,
	}
	cmd.AddCommand(newWsMkdirCmd())
	cmd.AddCommand(newWsMvCmd())
	cmd.AddCommand(newWsRmCmd())
	cmd.AddCommand(newWsAuditCmd())
	cmd.AddCommand(newWsReconcileCmd())
	return cmd
}

// wsBootstrap loads workspace config and builds a Runner.
func wsBootstrap(cmd *cobra.Command) (ws.Roots, *exec.Runner, bool, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")
	homeOverride, _ := cmd.Flags().GetString("home")

	var state *config.UserState
	var err error
	if homeOverride != "" {
		state, err = config.LoadStateForHome(homeOverride)
	} else {
		state, err = config.LoadState()
	}
	if err != nil {
		return ws.Roots{}, nil, false, fmt.Errorf("loading state: %w", err)
	}

	if state.Modules.Workspace.Path == "" || state.Modules.Workspace.GdriveSymlink == "" {
		return ws.Roots{}, nil, false, fmt.Errorf("dual workspace not configured (Path + GdriveSymlink required); run 'dotfiles reconfigure'")
	}

	home, _ := os.UserHomeDir()
	if homeOverride != "" {
		home = homeOverride
	}
	expand := func(p string) string {
		if strings.HasPrefix(p, "~/") {
			return filepath.Join(home, p[2:])
		}
		return p
	}

	roots := ws.Roots{
		Work:   filepath.Join(expand(state.Modules.Workspace.Path), "work"),
		Gdrive: filepath.Join(expand(state.Modules.Workspace.GdriveSymlink), "work"),
	}

	if fi, err := os.Stat(roots.Work); err != nil || !fi.IsDir() {
		return ws.Roots{}, nil, false, fmt.Errorf("work root not accessible: %s", roots.Work)
	}
	if fi, err := os.Stat(roots.Gdrive); err != nil || !fi.IsDir() {
		return ws.Roots{}, nil, false, fmt.Errorf("gdrive root not accessible: %s (is Drive mounted?)", roots.Gdrive)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := exec.NewRunner(dryRun, logger)
	return roots, runner, yes, nil
}

// --- Subcommands ---

func newWsMkdirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mkdir <path>",
		Short: "Create a directory on both workspaces",
		Args:  cobra.ExactArgs(1),
		RunE:  runWsMkdir,
	}
}

func runWsMkdir(cmd *cobra.Command, args []string) error {
	roots, runner, _, err := wsBootstrap(cmd)
	if err != nil {
		return err
	}
	msgs, err := ws.Mkdir(runner, roots, args[0])
	for _, m := range msgs {
		fmt.Println(m)
	}
	return err
}

func newWsMvCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mv <src> <dst>",
		Short: "Rename/move a directory on both workspaces",
		Args:  cobra.ExactArgs(2),
		RunE:  runWsMv,
	}
}

func runWsMv(cmd *cobra.Command, args []string) error {
	roots, runner, _, err := wsBootstrap(cmd)
	if err != nil {
		return err
	}
	msgs, err := ws.Move(context.Background(), runner, roots, args[0], args[1])
	for _, m := range msgs {
		fmt.Println(m)
	}
	return err
}

func newWsRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <path>",
		Short: "Remove a directory from both workspaces",
		Args:  cobra.ExactArgs(1),
		RunE:  runWsRm,
	}
	cmd.Flags().BoolP("recursive", "r", false, "Remove non-empty directories")
	return cmd
}

func runWsRm(cmd *cobra.Command, args []string) error {
	roots, runner, yes, err := wsBootstrap(cmd)
	if err != nil {
		return err
	}
	recursive, _ := cmd.Flags().GetBool("recursive")

	// Safety confirm for recursive delete (unless --yes)
	if recursive && !yes {
		workAbs, gdriveAbs := roots.ResolvePair(args[0])
		fmt.Printf("Recursively delete:\n  %s\n  %s\n", workAbs, gdriveAbs)
		ok, err := ui.ConfirmBool("Continue?", false, false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Aborted.")
			return nil
		}
	}

	msgs, err := ws.Remove(context.Background(), runner, roots, args[0], recursive)
	for _, m := range msgs {
		fmt.Println(m)
	}
	return err
}

func newWsAuditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "audit [scope]",
		Short: "Report structural mismatches between workspaces",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runWsAudit,
	}
}

func runWsAudit(cmd *cobra.Command, args []string) error {
	roots, _, _, err := wsBootstrap(cmd)
	if err != nil {
		return err
	}
	scope := ""
	if len(args) == 1 {
		scope = args[0]
	}

	mismatches, err := ws.Audit(roots, ws.AuditOptions{Scope: scope})
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(ui.StyleHeader.Render(" Workspace Audit "))
	fmt.Println()
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Work:"), ui.StyleValue.Render(roots.Work))
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render("GDrive:"), ui.StyleValue.Render(roots.Gdrive))
	if scope != "" {
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Scope:"), ui.StyleValue.Render(scope))
	}
	fmt.Println()

	if len(mismatches) == 0 {
		fmt.Println(ui.StyleSuccess.Render("✓ Trees are in sync."))
		return nil
	}

	var workOnly, gdriveOnly []ws.Mismatch
	for _, m := range mismatches {
		if m.OnlyOn == ws.SideWork {
			workOnly = append(workOnly, m)
		} else {
			gdriveOnly = append(gdriveOnly, m)
		}
	}

	if len(workOnly) > 0 {
		fmt.Println(ui.StyleSection.Render(fmt.Sprintf("▸ Only on work (%d)", len(workOnly))))
		for _, m := range workOnly {
			printMismatch(m)
		}
		fmt.Println()
	}
	if len(gdriveOnly) > 0 {
		fmt.Println(ui.StyleSection.Render(fmt.Sprintf("▸ Only on gdrive (%d)", len(gdriveOnly))))
		for _, m := range gdriveOnly {
			printMismatch(m)
		}
		fmt.Println()
	}
	fmt.Printf("%d mismatch(es). Run 'dotfiles ws reconcile' to resolve.\n", len(mismatches))
	return nil
}

func printMismatch(m ws.Mismatch) {
	tag := ui.StyleHint.Render("(empty)")
	if !m.IsEmpty {
		tag = ui.StyleHint.Render(fmt.Sprintf("(%s)", ws.FormatSize(m.Size)))
	}
	fmt.Printf("  %s  %s\n", ui.StyleValue.Render(m.RelPath), tag)
}

func newWsReconcileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile [scope]",
		Short: "Interactively resolve workspace mismatches",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runWsReconcile,
	}
}

func runWsReconcile(cmd *cobra.Command, args []string) error {
	roots, runner, yes, err := wsBootstrap(cmd)
	if err != nil {
		return err
	}
	scope := ""
	if len(args) == 1 {
		scope = args[0]
	}

	mismatches, err := ws.Audit(roots, ws.AuditOptions{Scope: scope})
	if err != nil {
		return err
	}
	if len(mismatches) == 0 {
		fmt.Println(ui.StyleSuccess.Render("✓ Trees are in sync. Nothing to reconcile."))
		return nil
	}

	ctx := context.Background()
	var copied, deleted, skipped int
	for i, m := range mismatches {
		srcSide := m.OnlyOn
		otherSide := srcSide.Other()
		fmt.Println()
		tag := "empty"
		if !m.IsEmpty {
			tag = ws.FormatSize(m.Size)
		}
		fmt.Println(ui.StyleSection.Render(fmt.Sprintf(
			"[%d/%d] %s — only on %s (%s)",
			i+1, len(mismatches), m.RelPath, srcSide.Name(), tag)))

		copyLabel := fmt.Sprintf("Copy to %s", otherSide.Name())
		deleteLabel := fmt.Sprintf("Delete from %s", srcSide.Name())

		var choice string
		if yes {
			// Unattended: always copy (safe — never delete)
			choice = copyLabel
			fmt.Printf("  %s (--yes)\n", choice)
		} else {
			options := []string{copyLabel, deleteLabel, "Skip", "Quit"}
			choice, err = ui.Select("Action?", options, "Skip", false)
			if err != nil {
				return err
			}
		}

		switch choice {
		case copyLabel:
			srcAbs, dstAbs := resolveSidePair(roots, m.RelPath, srcSide)
			dstParent := filepath.Dir(dstAbs)
			if !isDirCheap(dstParent) {
				if err := runner.MkdirAll(dstParent, 0755); err != nil {
					fmt.Printf("  ⚠ mkdir parent %s: %v\n", dstParent, err)
					skipped++
					continue
				}
			}
			if _, err := runner.Run(ctx, "cp", "-R", srcAbs, dstAbs); err != nil {
				fmt.Printf("  ⚠ cp failed: %v\n", err)
				skipped++
				continue
			}
			fmt.Printf("  ✓ copied %s → %s\n", srcAbs, dstAbs)
			copied++
		case deleteLabel:
			srcAbs, _ := resolveSidePair(roots, m.RelPath, srcSide)
			if !m.IsEmpty {
				fmt.Printf("  About to delete non-empty dir: %s (%s)\n", srcAbs, ws.FormatSize(m.Size))
				confirm, err := ui.ConfirmBool("Really delete?", false, false)
				if err != nil {
					return err
				}
				if !confirm {
					fmt.Println("  skipped")
					skipped++
					continue
				}
			}
			if _, err := runner.Run(ctx, "rm", "-rf", srcAbs); err != nil {
				fmt.Printf("  ⚠ rm failed: %v\n", err)
				skipped++
				continue
			}
			fmt.Printf("  ✓ deleted %s\n", srcAbs)
			deleted++
		case "Skip":
			skipped++
		case "Quit":
			fmt.Println("  (quit)")
			goto summary
		}
	}
summary:
	fmt.Println()
	fmt.Printf("Reconciled: %d copied, %d deleted, %d skipped (of %d).\n",
		copied, deleted, skipped, len(mismatches))
	return nil
}

// resolveSidePair returns (srcAbs, dstAbs) where src is on srcSide and dst is on the other side.
func resolveSidePair(roots ws.Roots, rel string, srcSide ws.Side) (string, string) {
	workAbs, gdriveAbs := roots.ResolvePair(rel)
	if srcSide == ws.SideWork {
		return workAbs, gdriveAbs
	}
	return gdriveAbs, workAbs
}

func isDirCheap(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// --- Alias top-level commands ---

func newWsMkdirAliasCmd() *cobra.Command {
	c := newWsMkdirCmd()
	c.Use = "ws-mkdir <path>"
	c.Short = "Alias for 'ws mkdir'"
	return c
}
func newWsMvAliasCmd() *cobra.Command {
	c := newWsMvCmd()
	c.Use = "ws-mv <src> <dst>"
	c.Short = "Alias for 'ws mv'"
	return c
}
func newWsRmAliasCmd() *cobra.Command {
	c := newWsRmCmd()
	c.Use = "ws-rm <path>"
	c.Short = "Alias for 'ws rm'"
	return c
}
func newWsAuditAliasCmd() *cobra.Command {
	c := newWsAuditCmd()
	c.Use = "ws-audit [scope]"
	c.Short = "Alias for 'ws audit'"
	return c
}
func newWsReconcileAliasCmd() *cobra.Command {
	c := newWsReconcileCmd()
	c.Use = "ws-reconcile [scope]"
	c.Short = "Alias for 'ws reconcile'"
	return c
}
