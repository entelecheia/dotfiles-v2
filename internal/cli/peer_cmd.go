package cli

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

// PeerProfile is the sync profile name used for machine-to-machine sync.
const PeerProfile = syncer.PeerProfile

func newPeerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peer",
		Short: "Sync the workspace directly to another machine over SSH",
		Args:  cobra.NoArgs,
		Long: `Machine-to-machine workspace sync, so either Mac can continue the same work.

This is a sibling of ` + "`dot sync`" + `, not a replacement. They answer different
questions:

  dot sync   workspace -> cloud mirror. One writer. Secrets excluded.
             For reading the workspace from a phone or another device.
  dot peer   workspace <-> another machine over ssh. Both directions.
             Secrets included. Submodule working trees included, because
             uncommitted work inside a submodule is exactly what Git has
             not seen and what a second machine still needs.

Routine one-sided changes transfer directly. A path changed differently on
both machines is resolved by the configured coordinator, and the losing peer
payload is quarantined once under .sync-conflicts/ when it exists.

With propagation.delete on, a file removed here is removed on the peer too,
into that same quarantine, and "dot sync conflicts prune" expires it later.
Only paths recorded in the baseline are eligible: a path the peer created and
this machine has never seen is not a deletion and is left alone. The set is
capped by max_delete, so a failed mount cannot present the whole tree as
deleted. With propagation.delete off, a file removed here simply stops being
sent and the peer keeps its copy.

A peer that is offline is not an error: the scheduled run probes reachability
first and exits cleanly when the other machine is away.`,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}

	cmd.AddCommand(newPeerInitCmd())
	cmd.AddCommand(newPeerStatusCmd())
	cmd.AddCommand(newPeerDoctorCmd())
	cmd.AddCommand(newPeerSyncCmd())
	cmd.AddCommand(newPeerSetupCmd())
	cmd.AddCommand(newPeerDiffCmd())
	cmd.AddCommand(newPeerHomePathsCmd())
	return cmd
}

// peerBootstrapOptions translates the peer command tree's flags into the
// engine's bootstrap options. The profile is fixed here, which is why
// `--profile` is deliberately not read: `dot peer` only ever addresses the
// peer store.
func peerBootstrapOptions(cmd *cobra.Command) syncer.BootstrapOptions {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	verbose, _ := cmd.Flags().GetBool("verbose")
	return syncer.BootstrapOptions{Profile: PeerProfile, DryRun: dryRun, Verbose: verbose, Home: homeOverrideFrom(cmd)}
}

// probeRunner is always live, even under --dry-run.
//
// Reachability, the remote rsync version and the clock are read-only questions
// about the peer, and a dry-run runner does not execute anything - it returns
// empty output. That made every probe answer "nothing found", so `peer sync
// --dry-run` failed with "no rsync found" against a peer doctor had just
// confirmed. A dry run must still be able to inspect the remote; only the
// transfer itself is what --dry-run holds back.
func probeRunner() *exec.Runner {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return exec.NewRunner(false, logger)
}

// scheduledRunEnv marks a run started by the launchd job `dot peer schedule`
// installs. The plist plants it (internal/syncer/peer_commands.go); this is
// the only place that reads it.
//
// The mechanism was chosen rather than inherited: launchd sets no
// distinguishing variable of its own, a TTY check misfires under CI and under
// a pipe, and a new persistent flag would appear in the command's help output
// for a mode no human invokes. The plist already had an EnvironmentVariables
// dict, so the marker cost one entry there.
const scheduledRunEnv = "DOT_SCHEDULED_RUN"

// isScheduledRun reports whether this process was started by the peer
// scheduler rather than by a person.
func isScheduledRun() bool { return os.Getenv(scheduledRunEnv) != "" }

// quietScheduledContention turns a lost lock race into a clean exit on a
// scheduled run and leaves every other failure exactly as loud as it was.
//
// launchd consumes the exit code. A sibling `dot peer sync` still holding the
// lock when the interval fires is normal operation on a laptop, not a job
// failure, and reporting it as one trains the operator to ignore the report.
// Contention is matched with errors.Is against the sentinel, never against
// the message, so an unwritable lock parent or an unreachable peer stays loud.
// The line goes through the runner's logger, which the plist routes to
// StandardOutPath, so a swallowed run is still recorded.
//
// ponytail: known ceiling. A plist installed by an older dot carries no
// marker, so its runs stay loud until the operator re-runs `dot peer
// schedule`. That is a one-time cost, stated here rather than discovered.
func quietScheduledContention(runner *exec.Runner, err error) error {
	if err == nil || !isScheduledRun() || !errors.Is(err, fileutil.ErrLockHeld) {
		return err
	}
	runner.Logger.Info("scheduled peer sync skipped: another run holds the lock", "err", err)
	return nil
}

// renderPeerEvent turns engine progress into the lines `dot peer sync` has
// always printed. One renderer for every step, so the wording cannot drift
// apart between the pull, push and host-path passes.
func renderPeerEvent(p *Printer) func(syncer.PeerEvent) {
	return func(e syncer.PeerEvent) {
		switch e.Kind {
		case syncer.PeerEventPullStart:
			p.Section("pull from peer")
		case syncer.PeerEventRemoteDeletesHeld:
			p.Warn("remote-only deletes held: peer baseline provenance is not established")
		case syncer.PeerEventPropagateDeletesStart:
			p.Section("propagate deletions")
		case syncer.PeerEventLocalDeletesHeld:
			p.Warn("local deletes held: peer baseline provenance is not established")
		case syncer.PeerEventPushStart:
			p.Section("push to peer")
		case syncer.PeerEventHostPathsStart:
			p.Section("host paths")
		case syncer.PeerEventHostPathsMissing:
			p.Warn("no host-path list at %s; skipping", e.Path)
		case syncer.PeerEventPartialTransfer:
			reportPartial(p, e.Err)
		}
	}
}

// reportPartial annotates rsync's partial-transfer outcome. The engine has
// already classified it and returns it as a hard transaction failure; this is
// only the announcement.
func reportPartial(p *Printer, err error) {
	p.Warn("partial transfer: %v", err)
	p.Line("  Peer transaction stopped; baseline unchanged. Re-run to retry it.")
}

// newPeerDiffCmd reports paths where the two machines disagree.
//
// This exists because "no data loss" and "no surprises" are different
// properties. Peer sync runs with --update, so when the same path was edited on
// both machines and the timestamps do not order cleanly, rsync skips it in both
// directions: nothing is destroyed, but the machines quietly stop agreeing and
// no one is told. Measured on a real round trip.
//
// The detector is a metadata-only dry run in each direction. A path that both
// sides want to send is a path where they differ. That misses two files with
// identical size and mtime but different content - rsync cannot see that without
// reading every byte, and a checksum pass over 60 GB is not something to do on a
// schedule.
func newPeerDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff",
		Short: "List paths where this machine and the peer disagree",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			p := printerFrom(c)
			bs, err := syncer.Bootstrap(peerBootstrapOptions(c))
			if err != nil {
				return err
			}
			res, err := syncer.PeerDiff(context.Background(), syncer.PeerDiffOptions{
				Config: bs.Config,
				Probe:  probeRunner(),
			})
			if err != nil {
				return err
			}
			if res.Unreachable {
				p.Warn("peer %s unreachable", bs.Config.Target.Host)
				return nil
			}
			plan := res.Plan

			p.Section("peer divergence")
			p.KV("would send", strconv.Itoa(len(plan.Push)+len(plan.DeleteRemote)))
			p.KV("would receive", strconv.Itoa(len(plan.Pull)+len(plan.DeleteLocal)))
			if !plan.HasConflicts() {
				p.Success("no path is contested")
				return nil
			}
			p.Warn("%d path(s) changed on BOTH machines:", len(plan.Conflicts))
			for i, conflict := range plan.Conflicts {
				if i >= 40 {
					p.Line("  ... and %d more", len(plan.Conflicts)-i)
					break
				}
				p.Line("  %s", conflict.RelPath)
			}
			p.Blank()
			p.Line("The profile owner is the coordinator; its version wins on the next peer sync.")
			p.Line("The losing peer payload is quarantined once under .sync-conflicts/.")
			return nil
		},
	}
}

func newPeerSyncCmd() *cobra.Command {
	var pushOnly, pullOnly, skipHome bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Exchange workspace and host paths with the peer (both directions)",
		Args:  cobra.NoArgs,
		Long: `Build one baseline-aware plan, accept one-sided peer changes, then
publish coordinator changes. Routine updates are backup-free; only simultaneous
conflicts and propagated deletes create quarantine copies.

When propagation.delete is on, baseline-proven deletes can flow in either
direction. Unknown peer-created paths are pulled and never classified as local
deletions. The common baseline advances only after the complete workspace and
host-path transaction succeeds.

Exits 0 when the peer is unreachable. That is what makes this safe to schedule
on a laptop.`,
		RunE: func(c *cobra.Command, _ []string) error {
			p := printerFrom(c)
			bs, err := syncer.Bootstrap(peerBootstrapOptions(c))
			if err != nil {
				return err
			}
			dryRun, _ := c.Flags().GetBool("dry-run")
			res, err := syncer.PeerSync(context.Background(), syncer.PeerSyncOptions{
				Config:   bs.Config,
				Runner:   bs.Runner,
				Probe:    probeRunner(),
				PushOnly: pushOnly,
				PullOnly: pullOnly,
				SkipHome: skipHome,
				DryRun:   dryRun,
				Progress: renderPeerEvent(p),
			})
			if err != nil {
				return quietScheduledContention(bs.Runner, err)
			}
			if res.Unreachable {
				p.Warn("peer %s unreachable; nothing to do", bs.Config.Target.Host)
				return nil
			}
			p.Blank()
			if res.Complete {
				p.Success("peer sync complete")
			} else {
				p.Warn("peer sync held destructive transitions; baseline unchanged")
			}
			p.Line("Conflicting edits keep both versions; list them with:")
			p.Line("  dot sync conflicts --profile=%s", PeerProfile)
			return nil
		},
	}
	cmd.Flags().BoolVar(&pushOnly, "push-only", false, "send local changes without pulling first")
	cmd.Flags().BoolVar(&pullOnly, "pull-only", false, "receive peer changes without pushing")
	cmd.Flags().BoolVar(&skipHome, "skip-home", false, "workspace only; skip the host-path pass")
	return cmd
}
