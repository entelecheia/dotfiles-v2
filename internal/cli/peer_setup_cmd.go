package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

func newPeerInitCmd() *cobra.Command {
	var host string
	var remotePath string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the peer profile pointing at another machine",
		Args:  cobra.NoArgs,
		Long: `Write <workspace>/.dotfiles/peer/ with a target, secrets opt-in, and the
host-path list.

Use a hostname that survives a network change. Tailscale MagicDNS names
(<machine>.<tailnet>.ts.net) work from any network without a static IP or an
inbound port, which is what a laptop needs.`,
		RunE: func(c *cobra.Command, _ []string) error {
			p := printerFrom(c)
			if strings.TrimSpace(host) == "" {
				return fmt.Errorf("--host is required (e.g. --host yj.lee@other.tailnet.ts.net)")
			}
			bs, err := syncer.Bootstrap(peerBootstrapOptions(c))
			if err != nil {
				return err
			}
			dryRun, _ := c.Flags().GetBool("dry-run")
			res, err := syncer.PeerInit(syncer.PeerInitOptions{
				Config:     bs.Config,
				Host:       host,
				RemotePath: remotePath,
				Runner:     bs.Runner,
				DryRun:     dryRun,
			})
			if err != nil {
				return err
			}
			if res.DryRun {
				p.Line("dry-run: would write %s", res.ConfigFile)
				p.Line("dry-run: would create %s", res.AllowFile)
				p.Line("dry-run: would create %s", res.HomePathsFile)
				p.KV("target", res.Target)
				return nil
			}

			p.Success("peer profile ready")
			p.KV("target", res.Target)
			p.KV("store", res.StoreDir)
			p.KV("secrets", "opted in via "+res.AllowFile)
			p.KV("host paths", res.HomePathsFile)
			deletes := "disabled; peer copy retained"
			if res.Propagation.Delete {
				deletes = "baseline-recorded paths quarantine on peer"
				if res.MaxDelete > 0 {
					deletes = fmt.Sprintf("%s (max %d per run)", deletes, res.MaxDelete)
				}
			}
			p.KV("deletes", deletes)
			p.Blank()
			p.Line("Next: dot peer doctor")
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "ssh destination for the other machine (user@host)")
	cmd.Flags().StringVar(&remotePath, "remote-path", "", "workspace path on the peer (default: same as local)")
	return cmd
}

func newPeerSetupCmd() *cobra.Command {
	var interval time.Duration
	var off bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install or remove the periodic peer sync job",
		Args:  cobra.NoArgs,
		Long: `Schedule dot peer sync.

An unreachable peer exits 0, so a laptop that is away simply produces quiet
no-op runs rather than failures. That is why this can be scheduled at all.

Pick an interval in minutes, not seconds: the payload is large and each run
walks the whole tree.`,
		RunE: func(c *cobra.Command, _ []string) error {
			p := printerFrom(c)
			bs, err := syncer.Bootstrap(peerBootstrapOptions(c))
			if err != nil {
				return err
			}
			dryRun, _ := c.Flags().GetBool("dry-run")
			res, err := syncer.PeerSchedule(context.Background(), syncer.PeerScheduleOptions{
				Config:   bs.Config,
				Runner:   bs.Runner,
				Probe:    probeRunner(),
				Interval: interval,
				Off:      off,
				DryRun:   dryRun,
			})
			if err != nil {
				return err
			}
			if res.Off {
				if res.DryRun {
					printPeerScheduleDryRun(p, res)
					return nil
				}
				p.Success("peer sync job removed")
				return nil
			}
			if res.DryRun {
				printPeerScheduleDryRun(p, res)
				return nil
			}
			p.Success("peer sync scheduled every %s", res.Interval)
			p.KV("plist", res.Plist)
			p.KV("log", res.LogFile)
			return nil
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 15*time.Minute, "how often to sync with the peer")
	cmd.Flags().BoolVar(&off, "off", false, "remove the scheduled job")
	return cmd
}

func printPeerScheduleDryRun(p *Printer, res *syncer.PeerScheduleResult) {
	if res.Off {
		p.Line("dry-run: would remove %s", res.Plist)
	} else {
		p.Line("dry-run: would write %s", res.Plist)
	}
	if res.TargetUserActionRequired {
		p.Warn("target-user action required: %s", syncer.SchedulerTargetUserInstruction())
	}
}

func newPeerDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that a peer sync would work before running one",
		Args:  cobra.NoArgs,
		Long: `Probe everything that silently breaks a peer transfer.

Checks, and why each exists:
  reachability   an offline peer must be a clean no-op, not a failure
  remote rsync   macOS 26 ships openrsync, which cannot receive -aHAX from a
                 3.x client — and --dry-run never surfaces it, because a dry
                 run ships no file data
  clock skew     "newer wins" is only meaningful if the clocks agree
  disk headroom  the receiving side has to hold the payload
  keychain       tokens there cannot be transferred, and cannot even be
                 verified over ssh — a reminder, not a failure`,
		RunE: func(c *cobra.Command, _ []string) error {
			p := printerFrom(c)
			bs, err := syncer.Bootstrap(peerBootstrapOptions(c))
			if err != nil {
				return err
			}
			report, err := syncer.PeerDoctor(context.Background(), syncer.PeerDoctorOptions{
				Config: bs.Config,
				Probe:  probeRunner(),
			})
			if err != nil {
				return err
			}

			p.Section("peer")
			p.KV("target", report.Target)

			if report.Unreachable {
				p.Warn("unreachable: %v", report.UnreachableErr)
				p.Line("  A scheduled run would exit cleanly here; a manual one has nothing to do.")
				return nil
			}
			p.Success("reachable")

			switch {
			case report.RemoteRsyncErr != nil:
				p.Fail("remote rsync: %v", report.RemoteRsyncErr)
			case report.RemoteRsyncPath == "":
				p.Success("remote rsync: default is usable")
			default:
				p.Success("remote rsync: %s", report.RemoteRsyncPath)
			}

			switch {
			case report.ClockSkewErr != nil:
				p.Warn("clock skew: %v", report.ClockSkewErr)
			case report.ClockSkewOK:
				p.Success("clock skew: %s", report.ClockSkew)
			default:
				p.Warn("clock skew: %s — newer-wins comparisons get unreliable past a few seconds", report.ClockSkew)
			}

			if report.DiskKnown {
				p.KV("peer disk", report.Disk)
			}

			p.Blank()
			p.Line("Reminder: keychain-backed tokens (gh, for one) cannot be synced by any file")
			p.Line("copy, and cannot be verified over ssh — check them in a local terminal.")

			if report.Problems > 0 {
				return fmt.Errorf("%d peer precondition(s) need attention", report.Problems)
			}
			return nil
		},
	}
}
