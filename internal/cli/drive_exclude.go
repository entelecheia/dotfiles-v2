package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/driveexclude"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
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
		newDriveExcludeAddCmd(),
		newDriveExcludeStatusCmd(),
	)

	return cmd
}

// ── interactive (default) ──────────────────────────────────────────────────

func runDriveExcludeInteractive(cmd *cobra.Command, _ []string) error {
	p := printerFrom(cmd)
	if !driveexclude.IsDarwin() {
		p.Line("drive-exclude is only supported on macOS (Google Drive File Stream).")
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
		printStatus(p, root, results)
		p.Line("\nAll directories are already excluded.")
		return nil
	}

	printScanResults(p, root, results)

	yes, _ := cmd.Flags().GetBool("yes")
	confirmed, err := ui.Confirm(
		fmt.Sprintf("Apply xattr to %d pending directories?", len(pending)),
		yes,
	)
	if err != nil {
		return err
	}
	if !confirmed {
		p.Line("Aborted.")
		return nil
	}

	return applyPending(p, root, pending, false)
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

			p := printerFrom(cmd)
			if len(results) == 0 {
				p.Line("No excludable directories found.")
				return nil
			}

			printScanResults(p, root, results)
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
			p := printerFrom(cmd)

			if !dryRun && !driveexclude.IsDarwin() {
				p.Line("drive-exclude apply is only supported on macOS.")
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
				p.Line("Nothing to apply — all directories already excluded.")
				return nil
			}

			printScanResults(p, root, results)

			if !all && !yes {
				confirmed, err := ui.Confirm(
					fmt.Sprintf("Apply xattr to %d directories?", len(pending)),
					false,
				)
				if err != nil {
					return err
				}
				if !confirmed {
					p.Line("Aborted.")
					return nil
				}
			}

			return applyPending(p, root, pending, dryRun)
		},
	}
	cmd.Flags().Bool("all", false, "Apply to all pending without confirmation")
	cmd.Flags().Bool("dry-run", false, "Show what would be excluded without applying")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	return cmd
}

// ── add (manual exclude) ──────────────────────────────────────────────────

func newDriveExcludeAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <path> [path...]",
		Short: "Manually exclude specific directories from Drive sync",
		Long:  "Set com.google.drivefs.ignorecontent xattr on the given directories.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			p := printerFrom(cmd)

			if !dryRun && !driveexclude.IsDarwin() {
				p.Line("drive-exclude add is only supported on macOS.")
				return nil
			}

			for _, arg := range args {
				path := absPath(arg)

				info, err := os.Stat(path)
				if err != nil {
					p.Line("  ✗ %s: %v", arg, err)
					continue
				}
				if !info.IsDir() {
					p.Line("  ✗ %s: not a directory", arg)
					continue
				}

				if dryRun {
					p.Line("  (dry-run) would exclude: %s", path)
					continue
				}

				if err := driveexclude.ApplyXattr(path); err != nil {
					p.Line("  ✗ %s: %v", path, err)
				} else {
					p.Line("  ✓ %s excluded", path)
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("dry-run", false, "Show what would be excluded without applying")
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
			printStatus(printerFrom(cmd), root, results)
			return nil
		},
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

const separator = "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

func defaultDriveRoot() string {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, "gdrive-workspace")
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return root
}

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
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

func printScanResults(p *Printer, root string, results []driveexclude.ScanResult) {
	counts := driveexclude.CountByStatus(results)

	for _, r := range results {
		rel := driveexclude.RelPath(root, r.Path)
		size := driveexclude.FormatSize(r.Size)
		switch r.Status {
		case driveexclude.StatusExcluded:
			p.Line("  [EXCLUDED]  %s (%s, xattr set)", rel, size)
		case driveexclude.StatusPending:
			p.Line("  [PENDING]   %s (%s, no xattr)", rel, size)
		case driveexclude.StatusSymlink:
			p.Line("  [SYMLINK]   %s → %s", rel, r.LinkTarget)
		}
	}

	// Show guidance for symlinks
	symlinks := filterStatus(results, driveexclude.StatusSymlink)
	if len(symlinks) > 0 {
		p.Line("\n  Symlinked node_modules detected. To fix:")
		p.Line("    1. rm <symlink>")
		p.Line("    2. pnpm install   (or npm install)")
		p.Line("    3. dot drive-exclude apply")
	}

	summary := fmt.Sprintf("\nFound: %d directories", len(results))
	if c := counts[driveexclude.StatusExcluded]; c > 0 {
		summary += fmt.Sprintf(" (%d excluded", c)
	} else {
		summary += " (0 excluded"
	}
	if c := counts[driveexclude.StatusPending]; c > 0 {
		summary += fmt.Sprintf(", %d pending", c)
	}
	if c := counts[driveexclude.StatusSymlink]; c > 0 {
		summary += fmt.Sprintf(", %d symlink", c)
	}
	p.Line("%s)", summary)

	if pending := driveexclude.SumPendingSize(results); pending > 0 {
		p.Line("Total pending: %s", driveexclude.FormatSize(pending))
	}
}

func printStatus(p *Printer, root string, results []driveexclude.ScanResult) {
	p.Line("Drive Exclude Status (%s)", root)
	p.Line("%s", separator)

	// Group by pattern category
	nodePatterns := []string{"node_modules", ".pnpm"}
	buildPatterns := []string{".astro", ".next", ".nuxt", ".svelte-kit", ".parcel-cache", ".turbo", ".angular", ".webpack"}
	pyPatterns := []string{".venv", "__pycache__", ".mypy_cache", ".pytest_cache"}

	printCategoryStatus(p, "node_modules", results, nodePatterns)
	printCategoryStatus(p, "build caches", results, buildPatterns)
	printCategoryStatus(p, "python", results, pyPatterns)

	// Symlink warnings
	symlinks := filterStatus(results, driveexclude.StatusSymlink)
	if len(symlinks) > 0 {
		p.Line("  ⚠ symlink legacy:   %d (rm symlink, reinstall, then apply)", len(symlinks))
	}

	// Totals
	excluded := filterStatus(results, driveexclude.StatusExcluded)
	var totalSize int64
	for _, r := range excluded {
		totalSize += r.Size
	}
	p.Line("\nTotal excluded: %d directories", len(excluded))
	if totalSize > 0 {
		p.Line("Estimated Drive savings: ~%s", driveexclude.FormatSize(totalSize))
	}
}

func printCategoryStatus(p *Printer, name string, results []driveexclude.ScanResult, patterns []string) {
	pset := make(map[string]bool, len(patterns))
	for _, pat := range patterns {
		pset[pat] = true
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
	p.Line("  %s %-16s %d excluded, %d pending", icon, name+":", excluded, pending)
}

func applyPending(p *Printer, root string, pending []driveexclude.ScanResult, dryRun bool) error {
	for _, r := range pending {
		rel := driveexclude.RelPath(root, r.Path)
		if dryRun {
			p.Line("  (dry-run) would exclude: %s", rel)
			continue
		}
		if err := driveexclude.ApplyXattr(r.Path); err != nil {
			p.Line("  ✗ %s: %v", rel, err)
		} else {
			p.Line("  ✓ %s excluded (%s)", rel, driveexclude.FormatSize(r.Size))
		}
	}
	return nil
}
