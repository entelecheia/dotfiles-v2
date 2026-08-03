package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

// PeerProfile is the sync profile name used for machine-to-machine sync.
const PeerProfile = "peer"

// peerAllowHeader seeds the peer profile's allow.txt. The cloud mirror keeps
// secrets out; a peer is a second machine the operator already trusts with the
// same work, and without env files and tokens the workspace does not actually
// run there. That difference in intent is exactly why the two profiles have
// separate allow files.
const peerAllowHeader = `# dot peer allow.txt — secrets opt-in for machine-to-machine sync.
#
# Unlike the cloud mirror, a peer is another machine you already trust with
# this workspace, and the workspace does not function there without these.
# The transport is ssh, so these never touch a cloud provider.
#
# Remove a line to stop syncing that class of secret.
/.maru/secrets/**
/.secrets/**
**/.env
**/.env.*
`

// peerHomePathsHeader documents the second rsync pass.
const peerHomePathsHeader = `# dot peer home-paths.txt — host-local paths carried to the peer.
#
# One path per line, relative to $HOME. Comments start with '#'.
# These sit outside the workspace but the workspace does not work without
# them: 16 of this workspace's 19 submodule remotes are SSH, so ~/.ssh is
# load-bearing, and the MCP server list lives in ~/.claude.json.
#
# Deliberately absent, because copying them breaks the target:
#   .maru/env                  venv console scripts bake an absolute
#                              interpreter path — rebuild, never copy
#   .maru/skills/_builtin      re-materialized by the Maru app
#   .maru/skills/_sources      branch-specific checkouts; reproduce, don't copy
#   .claude/plugins/cache      cache
#   .codex/sessions            history
#   .codex/auth.json           credential; re-auth instead
#
# Also unreachable by any file copy: tokens in the macOS keychain (gh, for
# one). They cannot be transferred and cannot even be verified over ssh.
.ssh
.gitconfig
.config/git
.config/dotfiles
.config/shell/90-secrets.sh
.claude.json
.claude/settings.json
.claude/settings.local.json
.claude/skills
.claude/plans
.agents
.codex/config.toml
.codex/hooks.json
.codex/memories
.maru/settings.json
.maru/sites.json
.maru/agents.json
.maru/state
.maru/telegram
`

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

Peer sync never deletes. Deletion is the one irreversible operation and there
is no shared history to justify it, so a file removed on one machine simply
stops being sent. Every overwrite is quarantined under .sync-conflicts/, so a
file edited on both machines keeps both versions.

A peer that is offline is not an error: the scheduled run probes reachability
first and exits cleanly when the other machine is away.`,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}

	cmd.AddCommand(newPeerInitCmd())
	cmd.AddCommand(newPeerDoctorCmd())
	cmd.AddCommand(newPeerSyncCmd())
	return cmd
}

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
			state, err := config.LoadState()
			if err != nil {
				return fmt.Errorf("loading state: %w", err)
			}
			cfg, err := syncer.ResolveConfigForProfile(state, PeerProfile)
			if err != nil {
				return err
			}
			paths := cfg.LocalPaths
			if paths == nil {
				return fmt.Errorf("peer store unresolved")
			}
			if remotePath == "" {
				// Same absolute layout on both machines is the norm here; the
				// workspace path is part of the environment being replicated.
				remotePath = strings.TrimRight(cfg.LocalPath, "/")
			}

			local, ok, err := syncer.LoadLocalConfig(paths)
			if err != nil {
				return err
			}
			if !ok || local == nil {
				local = &syncer.LocalConfig{}
			}
			local.Target = "ssh:" + host + ":" + remotePath
			local.FilterMode = syncer.FilterModeInclude
			// Never delete across peers.
			local.Propagation = syncer.PropagationPolicy{Create: true, Update: true, Delete: false}
			local.IncludeSubmodules = true
			if hostname, err := syncer.ShortHostname(); err == nil {
				// Peer profiles are per-machine by construction (the store is
				// gitignored), so the owner is simply this machine.
				local.Owner = hostname
			}
			if err := syncer.SaveLocalConfig(paths, local); err != nil {
				return err
			}

			if err := seedFileIfAbsent(paths.AllowFile, peerAllowHeader); err != nil {
				return err
			}
			if err := seedFileIfAbsent(peerHomePathsFile(paths), peerHomePathsHeader); err != nil {
				return err
			}

			p.Success("peer profile ready")
			p.KV("target", local.Target)
			p.KV("store", paths.StoreDir)
			p.KV("secrets", "opted in via "+paths.AllowFile)
			p.KV("host paths", peerHomePathsFile(paths))
			p.KV("deletes", "never propagated")
			p.Blank()
			p.Line("Next: dot peer doctor")
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "ssh destination for the other machine (user@host)")
	cmd.Flags().StringVar(&remotePath, "remote-path", "", "workspace path on the peer (default: same as local)")
	return cmd
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
			state, cfg, runner, err := peerBootstrap(c)
			if err != nil {
				return err
			}
			_ = state
			if !cfg.Target.IsSSH() {
				return fmt.Errorf("peer profile target is %q, expected an ssh: target; run dot peer init", cfg.Target.String())
			}
			host := cfg.Target.Host
			p.Section("peer")
			p.KV("target", cfg.Target.String())

			ctx := context.Background()
			problems := 0

			if err := syncer.CheckSSH(ctx, runner, host); err != nil {
				p.Warn("unreachable: %v", err)
				p.Line("  A scheduled run would exit cleanly here; a manual one has nothing to do.")
				return nil
			}
			p.Success("reachable")

			rp, err := syncer.RemoteRsyncPath(ctx, runner, host)
			if err != nil {
				p.Fail("remote rsync: %v", err)
				problems++
			} else if rp == "" {
				p.Success("remote rsync: default is usable")
			} else {
				p.Success("remote rsync: %s", rp)
			}

			if skew, err := peerClockSkew(ctx, runner, host); err != nil {
				p.Warn("clock skew: %v", err)
			} else {
				abs := skew
				if abs < 0 {
					abs = -abs
				}
				switch {
				case abs <= 2*time.Second:
					p.Success("clock skew: %s", skew)
				default:
					p.Warn("clock skew: %s — newer-wins comparisons get unreliable past a few seconds", skew)
					problems++
				}
			}

			if out, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", host, "df -h / | tail -1"); err == nil {
				p.KV("peer disk", strings.Join(strings.Fields(strings.TrimSpace(out.Stdout)), " "))
			}

			p.Blank()
			p.Line("Reminder: keychain-backed tokens (gh, for one) cannot be synced by any file")
			p.Line("copy, and cannot be verified over ssh — check them in a local terminal.")

			if problems > 0 {
				return fmt.Errorf("%d peer precondition(s) need attention", problems)
			}
			return nil
		},
	}
}

func newPeerSyncCmd() *cobra.Command {
	var pushOnly, pullOnly bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Exchange workspace and host paths with the peer (both directions)",
		Args:  cobra.NoArgs,
		Long: `Pull the peer's changes, then push this machine's.

Pull first on purpose: it is the direction that can only add, so a conflict is
recorded before this machine's version goes out. Neither direction deletes.

Exits 0 when the peer is unreachable. That is what makes this safe to schedule
on a laptop.`,
		RunE: func(c *cobra.Command, _ []string) error {
			p := printerFrom(c)
			_, cfg, runner, err := peerBootstrap(c)
			if err != nil {
				return err
			}
			if !cfg.Target.IsSSH() {
				return fmt.Errorf("peer target is not ssh:; run dot peer init --host ...")
			}
			ctx := context.Background()

			if err := syncer.CheckSSH(ctx, runner, cfg.Target.Host); err != nil {
				p.Warn("peer %s unreachable; nothing to do", cfg.Target.Host)
				return nil
			}
			rp, err := syncer.RemoteRsyncPath(ctx, runner, cfg.Target.Host)
			if err != nil {
				return err
			}
			cfg.RemoteRsyncPath = rp

			dryRun, _ := c.Flags().GetBool("dry-run")
			if !pushOnly {
				p.Section("pull from peer")
				if err := reportPartial(p, syncer.PullDirect(ctx, runner, cfg, dryRun)); err != nil {
					return err
				}
			}
			if !pullOnly {
				p.Section("push to peer")
				if err := reportPartial(p, syncer.Push(ctx, runner, cfg, dryRun)); err != nil {
					return err
				}
			}
			p.Blank()
			p.Success("peer sync complete")
			p.Line("Conflicting edits keep both versions; list them with:")
			p.Line("  dot sync conflicts --profile=%s", PeerProfile)
			return nil
		},
	}
	cmd.Flags().BoolVar(&pushOnly, "push-only", false, "send local changes without pulling first")
	cmd.Flags().BoolVar(&pullOnly, "pull-only", false, "receive peer changes without pushing")
	return cmd
}

// reportPartial turns rsync's partial-transfer outcome into a warning instead of
// a hard stop. Exit 23/24 mean "moved almost everything"; treating them as fatal
// is what once aborted a run before its second pass while reporting success.
func reportPartial(p *Printer, err error) error {
	if err == nil {
		return nil
	}
	if syncer.IsPartialTransfer(err) {
		p.Warn("partial transfer: %v", err)
		p.Line("  Some files were skipped; the rest arrived. Re-run to retry them.")
		return nil
	}
	return err
}

func peerBootstrap(cmd *cobra.Command) (*config.UserState, *syncer.Config, *exec.Runner, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	state, err := config.LoadState()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading state: %w", err)
	}
	cfg, err := syncer.ResolveConfigForProfile(state, PeerProfile)
	if err != nil {
		return nil, nil, nil, err
	}
	verbose, _ := cmd.Flags().GetBool("verbose")
	cfg.Verbose = verbose
	runner := exec.NewRunner(dryRun, nil)
	return state, cfg, runner, nil
}

func peerHomePathsFile(paths *syncer.LocalPaths) string {
	return filepath.Join(paths.StoreDir, "home-paths.txt")
}

func seedFileIfAbsent(path, body string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// peerClockSkew reports peer-time minus local-time.
func peerClockSkew(ctx context.Context, runner *exec.Runner, host string) (time.Duration, error) {
	res, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", host, "date -u +%s")
	if err != nil {
		return 0, err
	}
	remote, err := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected peer clock output %q", strings.TrimSpace(res.Stdout))
	}
	return time.Duration(remote-time.Now().UTC().Unix()) * time.Second, nil
}
