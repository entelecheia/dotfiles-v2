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
	p := printerFrom(cmd)

	p.Line("")
	p.Line("%s", ui.StyleHeader.Render(" Workspace Cleanup "))
	p.Line("")
	p.Line("  Scanning %s ...\n", ui.StyleHint.Render(root))

	scanner := clean.NewScanner(root, includeRisky)
	result, err := scanner.Scan()
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	if len(result.Matches) == 0 && len(result.Protected) == 0 {
		p.Line("  No junk found. Workspace is clean.")
		p.Line("")
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

		p.Line("%s", ui.StyleSection.Render(fmt.Sprintf(
			"▸ %s (%d items, %s)", cat, len(catMatches), clean.FormatSize(catSize))))

		// For .DS_Store, just show count
		if cat == "misc" {
			p.Line("  %s  %s",
				ui.StyleHint.Render(".DS_Store"),
				ui.StyleHint.Render(fmt.Sprintf("(%d files)", len(catMatches))))
		} else {
			for _, m := range catMatches {
				p.Line("  %-15s %s  %s",
					ui.StyleHint.Render(m.Pattern.Name),
					ui.StyleValue.Render(m.RelPath),
					ui.StyleHint.Render(clean.FormatSize(m.Size)))
			}
		}
		p.Line("")
	}

	// Protected items
	if len(result.Protected) > 0 {
		p.Line("%s", ui.StyleSection.Render("▸ Protected (skipped)"))
		for _, m := range result.Protected {
			p.Line("  %-15s %s  %s",
				ui.StyleHint.Render(m.Pattern.Name),
				ui.StyleValue.Render(m.RelPath),
				ui.StyleHint.Render("(inside _sys/)"))
		}
		p.Line("")
	}

	// Summary
	summary := fmt.Sprintf("  Total: %d items, ~%s to free",
		len(result.Matches), clean.FormatSize(result.TotalSize()))
	if len(result.Protected) > 0 {
		summary += fmt.Sprintf(" (%d protected, not touched)", len(result.Protected))
	}
	p.Line("%s", summary)

	// Action
	if dryRun || !yes {
		p.Line("")
		hint := "  Run with --yes to delete"
		if !includeRisky {
			hint += ", or --all --yes to include dist/build/out/target"
		}
		p.Line("%s", ui.StyleHint.Render(hint))
		p.Line("")
		return nil
	}

	// Actually delete
	p.Line("")
	deleted, freed, errs := clean.Delete(result.Matches)
	if len(errs) > 0 {
		for _, e := range errs {
			p.Line("  ✗ %s", e)
		}
	}
	p.Line("  ✓ Deleted %d items, freed %s", deleted, clean.FormatSize(freed))
	p.Line("")
	return nil
}
