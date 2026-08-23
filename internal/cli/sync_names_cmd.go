package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

const nfdCLIPlanLimit = 20

// newSyncNamesCmd owns workspace filename maintenance below `dot sync names`.
// The parent sync command supplies the inherited --profile, --dry-run, and
// --yes flags; keeping those flags inherited avoids a second source of truth.
func newSyncNamesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "names",
		Short: "Normalize selected workspace names to Unicode NFD",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		SilenceUsage: true,
	}
	cmd.AddCommand(newSyncNamesNormalizeCmd())
	return cmd
}

func newSyncNamesNormalizeCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "normalize",
		Short:        "Plan or apply Unicode NFD filename normalization",
		Args:         cobra.NoArgs,
		RunE:         runSyncNamesNormalize,
		SilenceUsage: true,
	}
}

func runSyncNamesNormalize(cmd *cobra.Command, _ []string) error {
	p := printerFrom(cmd)
	bs, err := syncer.Bootstrap(syncBootstrapOptions(cmd, true))
	if err != nil {
		return err
	}
	cfg := bs.Config

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")
	result, err := syncer.NormalizeWorkspaceNames(cfg, true)
	if err != nil {
		return err
	}
	printNFDNormalizationPlan(p, result.Plan, dryRun)
	if len(result.Plan.Renames) == 0 {
		if syncer.NFDMigrationMarked(result.Plan.WorkspaceRoot) {
			p.Success("Workspace names are already normalized to NFD.")
			return nil
		}
		if dryRun {
			p.Line("Dry run: no migration marker written.")
			return nil
		}
	}

	if dryRun {
		p.Line("Dry run: no names or migration marker changed.")
		return nil
	}
	if !yes {
		p.Line("Pass --yes to apply this plan.")
		return nil
	}

	release, lockErr := syncer.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Warn("%s", lockErr)
		return nil
	}
	defer release()

	// Re-plan under the lock so names created or removed while the report was
	// being read cannot turn the apply phase into a stale partial operation.
	result, err = syncer.NormalizeWorkspaceNames(cfg, false)
	if err != nil {
		return err
	}
	p.Success("Normalized %d path name(s) to NFD.", result.Applied)
	p.KV("Migration marker", result.MarkerPath)
	return nil
}

func printNFDNormalizationPlan(p *Printer, plan *syncer.NameNormalizationPlan, dryRun bool) {
	p.Header("Workspace Name Normalization (NFD)")
	if plan == nil {
		p.Line("No plan available.")
		return
	}
	p.KV("Workspace", plan.WorkspaceRoot)
	p.KV("Renames", fmt.Sprintf("%d", len(plan.Renames)))
	if plan.Skipped > 0 {
		p.KV("Skipped", fmt.Sprintf("%d", plan.Skipped))
	}
	limit := len(plan.Renames)
	if limit > nfdCLIPlanLimit {
		limit = nfdCLIPlanLimit
	}
	for _, rename := range plan.Renames[:limit] {
		p.Bullet(ui.StyleHint.Render(ui.MarkPending), fmt.Sprintf("%s -> %s", rename.OldRel, rename.NewRel))
	}
	if len(plan.Renames) > limit {
		p.Line("  ... and %d more rename(s)", len(plan.Renames)-limit)
	}
	if dryRun {
		p.Line("Preview only.")
	}
}
