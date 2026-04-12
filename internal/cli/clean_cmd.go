package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/clean"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newCleanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clean [path]",
		Short: "Remove junk directories (node_modules, caches, venvs) from workspace",
		Long: `Scan for and remove directories that waste disk space and cause
problems with Google Drive sync: node_modules, __pycache__, .venv,
build caches, and .DS_Store files.

Default: scan and show what would be removed (preview mode).
Use --yes to actually delete. Use --all to include risky patterns
(dist/, build/, out/, target/).

The _sys/ subtree is ALWAYS protected and will never be touched.`,
		Aliases: []string{"gc"},
		Args:    cobra.MaximumNArgs(1),
		RunE:    runClean,
	}
	cmd.Flags().Bool("all", false, "Include risky patterns (dist/, build/, out/, target/)")
	return cmd
}

func runClean(cmd *cobra.Command, args []string) error {
	root := filepath.Join(defaultDriveRoot(), "work")
	if len(args) > 0 {
		root = absPath(args[0])
	}

	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("path does not exist: %s", root)
	}

	includeRisky, _ := cmd.Flags().GetBool("all")
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	fmt.Println()
	fmt.Println(ui.StyleHeader.Render(" Workspace Cleanup "))
	fmt.Println()
	fmt.Printf("  Scanning %s ...\n\n", ui.StyleHint.Render(root))

	scanner := clean.NewScanner(root, includeRisky)
	result, err := scanner.Scan()
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	if len(result.Matches) == 0 && len(result.Protected) == 0 {
		fmt.Println("  No junk found. Workspace is clean.")
		fmt.Println()
		return nil
	}

	// Group matches by category
	categories := []string{"node", "python", "cache", "build", "misc"}
	for _, cat := range categories {
		var catMatches []clean.Match
		for _, m := range result.Matches {
			if m.Pattern.Category == cat {
				catMatches = append(catMatches, m)
			}
		}
		if len(catMatches) == 0 {
			continue
		}

		var catSize int64
		for _, m := range catMatches {
			catSize += m.Size
		}

		fmt.Println(ui.StyleSection.Render(fmt.Sprintf(
			"▸ %s (%d items, %s)", cat, len(catMatches), clean.FormatSize(catSize))))

		// For .DS_Store, just show count
		if cat == "misc" {
			fmt.Printf("  %s  %s\n",
				ui.StyleHint.Render(".DS_Store"),
				ui.StyleHint.Render(fmt.Sprintf("(%d files)", len(catMatches))))
		} else {
			for _, m := range catMatches {
				fmt.Printf("  %-15s %s  %s\n",
					ui.StyleHint.Render(m.Pattern.Name),
					ui.StyleValue.Render(m.RelPath),
					ui.StyleHint.Render(clean.FormatSize(m.Size)))
			}
		}
		fmt.Println()
	}

	// Protected items
	if len(result.Protected) > 0 {
		fmt.Println(ui.StyleSection.Render("▸ Protected (skipped)"))
		for _, m := range result.Protected {
			fmt.Printf("  %-15s %s  %s\n",
				ui.StyleHint.Render(m.Pattern.Name),
				ui.StyleValue.Render(m.RelPath),
				ui.StyleHint.Render("(inside _sys/)"))
		}
		fmt.Println()
	}

	// Summary
	fmt.Printf("  Total: %d items, ~%s to free",
		len(result.Matches), clean.FormatSize(result.TotalSize()))
	if len(result.Protected) > 0 {
		fmt.Printf(" (%d protected, not touched)", len(result.Protected))
	}
	fmt.Println()

	// Action
	if dryRun || !yes {
		fmt.Println()
		hint := "  Run with --yes to delete"
		if !includeRisky {
			hint += ", or --all --yes to include dist/build/out/target"
		}
		fmt.Println(ui.StyleHint.Render(hint))
		fmt.Println()
		return nil
	}

	// Actually delete
	fmt.Println()
	deleted, freed, errs := clean.Delete(result.Matches)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Printf("  ✗ %s\n", e)
		}
	}
	fmt.Printf("  ✓ Deleted %d items, freed %s\n", deleted, clean.FormatSize(freed))
	fmt.Println()
	return nil
}
