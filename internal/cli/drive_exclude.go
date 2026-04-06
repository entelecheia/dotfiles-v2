package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/driveexclude"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
	"github.com/spf13/cobra"
)

func newDriveExcludeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drive-exclude",
		Short: "Manage Google Drive sync exclusions",
		Long: `Exclude node_modules, build caches, and other heavy directories
from Google Drive sync using macOS xattr (com.google.drivefs.ignorecontent).`,
		RunE: runDriveExcludeInteractive,
	}

	cmd.AddCommand(
		newDriveExcludeScanCmd(),
		newDriveExcludeApplyCmd(),
		newDriveExcludeStatusCmd(),
	)

	return cmd
}

// ── interactive (default) ──────────────────────────────────────────────────

func runDriveExcludeInteractive(cmd *cobra.Command, _ []string) error {
	if !driveexclude.IsDarwin() {
		fmt.Println("drive-exclude is only supported on macOS (Google Drive File Stream).")
		return nil
	}

	root := defaultDriveRoot()
	scanner := driveexclude.NewScanner(root)
	results, err := scanner.Scan()
	if err != nil {
		return fmt.Errorf("scanning: %w", err)
	}

	pending := filterStatus(results, driveexclude.StatusPending)
	if len(pending) == 0 {
		printStatus(root, results)
		fmt.Println("\nAll directories are already excluded.")
		return nil
	}

	printScanResults(root, results)

	yes, _ := cmd.Flags().GetBool("yes")
	confirmed, err := ui.Confirm(
		fmt.Sprintf("Apply xattr to %d pending directories?", len(pending)),
		yes,
	)
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Println("Aborted.")
		return nil
	}

	return applyPending(root, pending, false)
}

// ── scan ───────────────────────────────────────────────────────────────────

func newDriveExcludeScanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan [path]",
		Short: "Scan for directories to exclude from Drive sync",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := defaultDriveRoot()
			if len(args) > 0 {
				root = absPath(args[0])
			}

			scanner := driveexclude.NewScanner(root)
			results, err := scanner.Scan()
			if err != nil {
				return fmt.Errorf("scanning: %w", err)
			}

			if len(results) == 0 {
				fmt.Println("No excludable directories found.")
				return nil
			}

			printScanResults(root, results)
			return nil
		},
	}
}

// ── apply ──────────────────────────────────────────────────────────────────

func newDriveExcludeApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply [path]",
		Short: "Apply xattr exclusion to pending directories",
		Long:  "Set com.google.drivefs.ignorecontent on directories not yet excluded.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			all, _ := cmd.Flags().GetBool("all")
			yes, _ := cmd.Flags().GetBool("yes")

			if !dryRun && !driveexclude.IsDarwin() {
				fmt.Println("drive-exclude apply is only supported on macOS.")
				return nil
			}

			root := defaultDriveRoot()
			if len(args) > 0 {
				root = absPath(args[0])
			}

			scanner := driveexclude.NewScanner(root)
			results, err := scanner.Scan()
			if err != nil {
				return fmt.Errorf("scanning: %w", err)
			}

			pending := filterStatus(results, driveexclude.StatusPending)
			if len(pending) == 0 {
				fmt.Println("Nothing to apply — all directories already excluded.")
				return nil
			}

			printScanResults(root, results)

			if !all && !yes {
				confirmed, err := ui.Confirm(
					fmt.Sprintf("Apply xattr to %d directories?", len(pending)),
					false,
				)
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Println("Aborted.")
					return nil
				}
			}

			return applyPending(root, pending, dryRun)
		},
	}
	cmd.Flags().Bool("all", false, "Apply to all pending without confirmation")
	cmd.Flags().Bool("dry-run", false, "Show what would be excluded without applying")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	return cmd
}

// ── status ─────────────────────────────────────────────────────────────────

func newDriveExcludeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current Drive exclusion status",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := defaultDriveRoot()
			scanner := driveexclude.NewScanner(root)
			results, err := scanner.Scan()
			if err != nil {
				return fmt.Errorf("scanning: %w", err)
			}
			printStatus(root, results)
			return nil
		},
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

const separator = "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

func defaultDriveRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "ai-workspace")
}

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func filterStatus(results []driveexclude.ScanResult, status driveexclude.DirStatus) []driveexclude.ScanResult {
	var filtered []driveexclude.ScanResult
	for _, r := range results {
		if r.Status == status {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func printScanResults(root string, results []driveexclude.ScanResult) {
	counts := driveexclude.CountByStatus(results)

	for _, r := range results {
		rel := driveexclude.RelPath(root, r.Path)
		size := driveexclude.FormatSize(r.Size)
		switch r.Status {
		case driveexclude.StatusExcluded:
			fmt.Printf("  [EXCLUDED]  %s (%s, xattr set)\n", rel, size)
		case driveexclude.StatusPending:
			fmt.Printf("  [PENDING]   %s (%s, no xattr)\n", rel, size)
		case driveexclude.StatusSymlink:
			fmt.Printf("  [SYMLINK]   %s → %s\n", rel, r.LinkTarget)
		}
	}

	// Show guidance for symlinks
	symlinks := filterStatus(results, driveexclude.StatusSymlink)
	if len(symlinks) > 0 {
		fmt.Println("\n  Symlinked node_modules detected. To fix:")
		fmt.Println("    1. rm <symlink>")
		fmt.Println("    2. pnpm install   (or npm install)")
		fmt.Println("    3. dot drive-exclude apply")
	}

	fmt.Printf("\nFound: %d directories", len(results))
	if c := counts[driveexclude.StatusExcluded]; c > 0 {
		fmt.Printf(" (%d excluded", c)
	} else {
		fmt.Print(" (0 excluded")
	}
	if c := counts[driveexclude.StatusPending]; c > 0 {
		fmt.Printf(", %d pending", c)
	}
	if c := counts[driveexclude.StatusSymlink]; c > 0 {
		fmt.Printf(", %d symlink", c)
	}
	fmt.Println(")")

	if pending := driveexclude.SumPendingSize(results); pending > 0 {
		fmt.Printf("Total pending: %s\n", driveexclude.FormatSize(pending))
	}
}

func printStatus(root string, results []driveexclude.ScanResult) {
	fmt.Printf("Drive Exclude Status (%s)\n", root)
	fmt.Println(separator)

	// Group by pattern category
	nodePatterns := []string{"node_modules", ".pnpm"}
	buildPatterns := []string{".astro", ".next", ".nuxt", ".svelte-kit", ".parcel-cache", ".turbo", ".angular", ".webpack"}
	pyPatterns := []string{".venv", "__pycache__", ".mypy_cache", ".pytest_cache"}

	printCategoryStatus("node_modules", results, nodePatterns)
	printCategoryStatus("build caches", results, buildPatterns)
	printCategoryStatus("python", results, pyPatterns)

	// Symlink warnings
	symlinks := filterStatus(results, driveexclude.StatusSymlink)
	if len(symlinks) > 0 {
		fmt.Printf("  ⚠ symlink legacy:   %d (rm symlink, reinstall, then apply)\n", len(symlinks))
	}

	// Totals
	excluded := filterStatus(results, driveexclude.StatusExcluded)
	var totalSize int64
	for _, r := range excluded {
		totalSize += r.Size
	}
	fmt.Printf("\nTotal excluded: %d directories\n", len(excluded))
	if totalSize > 0 {
		fmt.Printf("Estimated Drive savings: ~%s\n", driveexclude.FormatSize(totalSize))
	}
}

func printCategoryStatus(name string, results []driveexclude.ScanResult, patterns []string) {
	pset := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		pset[p] = true
	}

	var excluded, pending int
	for _, r := range results {
		if !pset[r.Pattern] {
			continue
		}
		switch r.Status {
		case driveexclude.StatusExcluded:
			excluded++
		case driveexclude.StatusPending:
			pending++
		}
	}

	if excluded+pending == 0 {
		return
	}

	icon := "✓"
	if pending > 0 {
		icon = "⚠"
	}
	fmt.Printf("  %s %-16s %d excluded, %d pending\n", icon, name+":", excluded, pending)
}

func applyPending(root string, pending []driveexclude.ScanResult, dryRun bool) error {
	for _, r := range pending {
		rel := driveexclude.RelPath(root, r.Path)
		if dryRun {
			fmt.Printf("  (dry-run) would exclude: %s\n", rel)
			continue
		}
		if err := driveexclude.ApplyXattr(r.Path); err != nil {
			fmt.Printf("  ✗ %s: %v\n", rel, err)
		} else {
			fmt.Printf("  ✓ %s excluded (%s)\n", rel, driveexclude.FormatSize(r.Size))
		}
	}
	return nil
}
