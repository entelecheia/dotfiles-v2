package cli

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

// peerGitStatusSchemaVersion is the peer-git status document schema. Like
// `dot peer status --json`, it stays at 1 and new fields are optional, so a
// bumped schema never stops a mixed-version pair.
const peerGitStatusSchemaVersion = 1

type peerGitSummaryJSON struct {
	Total           int `json:"total"`
	Aligned         int `json:"aligned"`
	Realignable     int `json:"realignable"`
	Realigned       int `json:"realigned"`
	NoMatch         int `json:"noMatch"`
	Skipped         int `json:"skipped"`
	Unresolvable    int `json:"unresolvable"`
	LinkedWorktrees int `json:"linkedWorktrees"`
}

type peerGitStatusJSON struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Kind          string                  `json:"kind"`
	Workspace     string                  `json:"workspace"`
	Git           string                  `json:"git"`
	Summary       peerGitSummaryJSON      `json:"summary"`
	Repos         []*syncer.GitRepoReport `json:"repos"`
}

func peerGitSummary(res *syncer.GitStateResult) peerGitSummaryJSON {
	counts := res.CountByStatus()
	return peerGitSummaryJSON{
		Total:           len(res.Repos),
		Aligned:         counts[syncer.GitRepoAligned],
		Realignable:     counts[syncer.GitRepoRealignable],
		Realigned:       counts[syncer.GitRepoRealigned],
		NoMatch:         counts[syncer.GitRepoNoMatch],
		Skipped:         counts[syncer.GitRepoSkipped],
		Unresolvable:    counts[syncer.GitRepoUnresolvable],
		LinkedWorktrees: counts[syncer.GitRepoLinkedWorktree],
	}
}

// peerGitWorkspace resolves the peer profile's workspace root without
// creating the peer store: `dot peer git` reads and realigns repositories,
// it never touches the sync state.
func peerGitWorkspace(cmd *cobra.Command) (string, error) {
	bs, err := peerBootstrapReadOnly(cmd)
	if err != nil {
		return "", err
	}
	return bs.Config.LocalPath, nil
}

func newPeerGitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git",
		Short: "Realign HEAD and index with the files peer sync delivered",
		Args:  cobra.NoArgs,
		Long: `Peer sync moves files, not git state: HEAD, index and refs stay behind on
the machine that did not make the commits. After a switch, the newly active
Mac realigns instead of pulling: each repo's HEAD and index move forward to
the descendant commit its files already match, through git's compare-and-swap
ref update. The worktree is never written by git, uncommitted modifications
survive, and untracked files never block.

Repos with a lock, an operation in progress, unmerged entries or staged
changes are skipped and reported. The commands never fetch; run git fetch
first when fresh upstream state is wanted.`,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}
	cmd.AddCommand(newPeerGitStatusCmd())
	cmd.AddCommand(newPeerGitRealignCmd())
	return cmd
}

func newPeerGitStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status [<repo>...]",
		Short:        "Classify every workspace repo against its recorded commit",
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			root, err := peerGitWorkspace(c)
			if err != nil {
				return err
			}
			res, err := syncer.PeerGitStatus(context.Background(), root, args)
			if err != nil {
				return err
			}
			if jsonOutput, _ := c.Flags().GetBool("json"); jsonOutput {
				repos := res.Repos
				if repos == nil {
					repos = []*syncer.GitRepoReport{}
				}
				encoder := json.NewEncoder(c.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(peerGitStatusJSON{
					SchemaVersion: peerGitStatusSchemaVersion,
					Kind:          "peer-git-status",
					Workspace:     res.Root,
					Git:           res.Git,
					Summary:       peerGitSummary(res),
					Repos:         repos,
				})
			}
			p := printerFrom(c)
			p.Header("Peer Git Status")
			p.KV("Workspace", res.Root)
			printPeerGitRepos(p, res, false)
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "print a stable machine-readable status document")
	return cmd
}

func newPeerGitRealignCmd() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:          "realign [--apply] [<repo>...]",
		Short:        "Move HEAD and index to the descendant commit the files already match",
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			root, err := peerGitWorkspace(c)
			if err != nil {
				return err
			}
			// The preview is the default. The global --dry-run flag always
			// wins over --apply: under it nothing changes in .git or in
			// either peer store (#99, #103).
			dryRun, _ := c.Flags().GetBool("dry-run")
			apply = apply && !dryRun
			res, err := syncer.PeerGitRealign(context.Background(), root, args, apply)
			if err != nil {
				return err
			}
			p := printerFrom(c)
			summary := peerGitSummary(res)
			if apply {
				p.Header("Peer Git Realign")
			} else {
				p.Header("Peer Git Realign (preview)")
			}
			p.KV("Workspace", res.Root)
			printPeerGitRepos(p, res, true)
			switch {
			case summary.Realigned > 0:
				// undo lines were printed per repo already
			case dryRun && summary.Realignable > 0:
				p.Blank()
				p.Line("--dry-run: nothing changed. Re-run without it to apply.")
			case summary.Realignable > 0:
				p.Blank()
				p.Line("Run with --apply to realign.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "move HEAD and index (default is a dry-run preview)")
	return cmd
}

// printPeerGitRepos renders the per-repo report and the status tally. In a
// realign preview each realignable repo shows the move it would make; after
// an applied run it shows the undo command for each realigned repo.
func printPeerGitRepos(p *Printer, res *syncer.GitStateResult, withMoves bool) {
	for _, rep := range res.Repos {
		line := string(rep.Status)
		switch {
		case rep.Status == syncer.GitRepoRealignable && withMoves:
			line += "  " + shortSHA(rep.Head) + " -> " + shortSHA(rep.Target)
		case rep.Status == syncer.GitRepoRealigned:
			line += "  " + shortSHA(rep.PreviousHead) + " -> " + shortSHA(rep.Target)
		}
		if rep.Reason != "" {
			line += "  (" + rep.Reason + ")"
		}
		p.Bullet(peerGitStatusMarker(rep.Status), rep.Path+"  "+line)
		if rep.Status == syncer.GitRepoRealigned {
			p.Line("      undo: git -C %s reset --mixed -q %s", peerGitRepoAbs(res.Root, rep.Path), rep.PreviousHead)
		}
	}
	summary := peerGitSummary(res)
	p.Section("summary")
	p.KV(string(syncer.GitRepoAligned), strconv.Itoa(summary.Aligned))
	p.KV(string(syncer.GitRepoRealignable), strconv.Itoa(summary.Realignable))
	if summary.Realigned > 0 {
		p.KV(string(syncer.GitRepoRealigned), strconv.Itoa(summary.Realigned))
	}
	p.KV(string(syncer.GitRepoNoMatch), strconv.Itoa(summary.NoMatch))
	p.KV(string(syncer.GitRepoSkipped), strconv.Itoa(summary.Skipped))
	p.KV(string(syncer.GitRepoUnresolvable), strconv.Itoa(summary.Unresolvable))
	if summary.LinkedWorktrees > 0 {
		p.KV("linked worktrees", strconv.Itoa(summary.LinkedWorktrees))
	}
}

func peerGitStatusMarker(status syncer.GitRepoStatus) string {
	switch status {
	case syncer.GitRepoAligned, syncer.GitRepoRealigned:
		return ui.MarkPresent
	case syncer.GitRepoRealignable:
		return ui.MarkPending
	case syncer.GitRepoSkipped, syncer.GitRepoLinkedWorktree:
		return ui.MarkPartial
	default:
		return ui.MarkWarn
	}
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func peerGitRepoAbs(root, rel string) string {
	if rel == "." {
		return root
	}
	return root + "/" + rel
}
