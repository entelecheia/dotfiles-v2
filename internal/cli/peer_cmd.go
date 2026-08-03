package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

const (
	// PeerProfile is the sync profile name used for machine-to-machine sync.
	PeerProfile = syncer.PeerProfile
	// Peer deletions are normally tens of inbox paths. Keep the new-profile cap
	// well below the mirror's broader 1000-path default.
	peerDefaultMaxDelete = 100
)

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
#
# ~/.ssh is listed as a directory but known_hosts is excluded in code: it is
# per-machine trust state, merging it is meaningless, and overwriting it with the
# peer's copy deletes the host key for the channel this sync runs over. That is
# not hypothetical - it happened on the first real run and broke the next ssh.
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

Nothing is ever unlinked. Every overwrite is quarantined under
.sync-conflicts/, so a file edited on both machines keeps both versions.

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
	cmd.AddCommand(newPeerDoctorCmd())
	cmd.AddCommand(newPeerSyncCmd())
	cmd.AddCommand(newPeerSetupCmd())
	cmd.AddCommand(newPeerDiffCmd())
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
			newProfile := !ok || local == nil
			if newProfile {
				local = &syncer.LocalConfig{}
			}
			local.Target = "ssh:" + host + ":" + remotePath
			// exclude-mode, not include-mode. The mirror uses an allowlist
			// (tracked files plus a binary-extension list) because untracked text
			// round-trips through Git anyway. A peer is different: "either machine
			// can continue the same work" has to cover an untracked scratch note,
			// which is neither tracked nor a binary extension and would otherwise
			// sync by neither channel. Measured: a new .md file never arrived.
			//
			// Safe because the junk layer already excludes the caches that make
			// this expensive - node_modules, target, .next, .venv, __pycache__ -
			// and secrets remain governed by allow.txt.
			local.FilterMode = syncer.FilterModeExclude
			if newProfile {
				// Deletions propagate for a new peer profile. Without this a file
				// removed on one machine returns on the next pull. Re-running init
				// must preserve an operator's later opt-out.
				local.Propagation = syncer.PropagationPolicy{Create: true, Update: true, Delete: true}
				local.MaxDelete = peerDefaultMaxDelete
			}
			local.IncludeSubmodules = true
			// Peer profiles are per-machine by construction (the store is
			// gitignored), so the owner is simply this machine. Use the DNS-safe
			// name rather than os.Hostname(), which can be the generic "Mac".
			if name := syncer.PreferredMachineName(); name != "" {
				local.Owner = name
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
			deletes := "disabled; peer copy retained"
			if local.Propagation.Delete {
				deletes = "baseline-recorded paths quarantine on peer"
				if local.MaxDelete > 0 {
					deletes = fmt.Sprintf("%s (max %d per run)", deletes, local.MaxDelete)
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

// peerPlistTmpl runs `dot peer sync` on an interval.
//
// The PATH is explicit for the same reason the mirror unit's is: launchd hands a
// job a minimal PATH that finds Apple's /usr/bin/rsync (openrsync) before
// Homebrew's 3.x. A peer transfer with the wrong rsync fails at data time, not
// at probe time.
const peerPlistTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.dotfiles.peer</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>peer</string>
    <string>sync</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>StartInterval</key>
  <integer>%d</integer>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`

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
			_, cfg, _, err := peerBootstrap(c)
			if err != nil {
				return err
			}
			if !cfg.Target.IsSSH() {
				return fmt.Errorf("peer target is not configured; run dot peer init first")
			}
			ctx := context.Background()
			probe := probeRunner()
			if err := syncer.CheckSSH(ctx, probe, cfg.Target.Host); err != nil {
				p.Warn("peer %s unreachable", cfg.Target.Host)
				return nil
			}
			rp, err := syncer.RemoteRsyncPath(ctx, probe, cfg.Target.Host)
			if err != nil {
				return err
			}
			cfg.RemoteRsyncPath = rp

			outgoing, err := peerPendingPaths(ctx, probe, cfg, true)
			if err != nil {
				return err
			}
			incoming, err := peerPendingPaths(ctx, probe, cfg, false)
			if err != nil {
				return err
			}

			var both []string
			for path := range outgoing {
				if incoming[path] {
					both = append(both, path)
				}
			}
			sort.Strings(both)

			p.Section("peer divergence")
			p.KV("would send", strconv.Itoa(len(outgoing)))
			p.KV("would receive", strconv.Itoa(len(incoming)))
			if len(both) == 0 {
				p.Success("no path is contested")
				return nil
			}
			p.Warn("%d path(s) changed on BOTH machines:", len(both))
			for i, path := range both {
				if i >= 40 {
					p.Line("  ... and %d more", len(both)-i)
					break
				}
				p.Line("  %s", path)
			}
			p.Blank()
			p.Line("Each machine currently keeps its own version. Reconcile by hand, or")
			p.Line("force one direction for a specific path:")
			p.Line("  dot sync fetch --profile=%s <path>   # take the peer's copy", PeerProfile)
			return nil
		},
	}
}

// peerPendingPaths lists what one direction would transfer, using a
// metadata-only dry run. --update is deliberately omitted: the question is
// "does this side have something different", not "is it newer".
func peerPendingPaths(ctx context.Context, runner *exec.Runner, cfg *syncer.Config, outgoing bool) (map[string]bool, error) {
	// A distinctive prefix so rsync's own messages ("skipping non-regular
	// file ...", warnings, the stats block) cannot be mistaken for paths. The
	// runner returns combined output, so filtering by prefix is the only
	// reliable way to read a file list back out.
	args := []string{"-a", "--no-links", "--dry-run", "--out-format=@@%n",
		"--exclude=/.dotfiles/", "--exclude=/inbox/gdrive/", "--exclude=.git",
		"--exclude-from=" + cfg.ExcludesFile,
		"--exclude-from=" + cfg.IgnoreFile,
	}
	if cfg.RemoteRsyncPath != "" {
		args = append(args, "--rsync-path="+cfg.RemoteRsyncPath)
	}
	args = append(args, "-e", "ssh")
	if outgoing {
		args = append(args, cfg.LocalPath, cfg.Target.RsyncDest())
	} else {
		args = append(args, cfg.Target.RsyncDest(), cfg.LocalPath)
	}
	res, err := runner.Run(ctx, "rsync", args...)
	if err != nil {
		if !syncer.IsPartialTransfer(err) {
			return nil, err
		}
	}
	out := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "@@") {
			continue
		}
		line = strings.TrimPrefix(line, "@@")
		if line == "" || strings.HasSuffix(line, "/") {
			continue
		}
		out[line] = true
	}
	return out, nil
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
			_, cfg, runner, err := peerBootstrap(c)
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			plist := filepath.Join(home, "Library", "LaunchAgents", "com.dotfiles.peer.plist")
			ctx := context.Background()

			if off {
				dryRun, _ := c.Flags().GetBool("dry-run")
				_, _ = runner.Run(ctx, "launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/com.dotfiles.peer")
				if dryRun {
					// runner already skipped the bootout; removing the plist here
					// anyway would leave a loaded job with no on-disk definition,
					// which is the opposite of a preview.
					p.Line("dry-run: would remove %s", plist)
					return nil
				}
				if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
					return err
				}
				p.Success("peer sync job removed")
				return nil
			}
			if interval < time.Minute {
				return fmt.Errorf("--interval must be at least 1m (got %s)", interval)
			}
			if !cfg.Target.IsSSH() {
				return fmt.Errorf("peer target is not configured; run dot peer init first")
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			logFile := cfg.LogFile
			if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
				return err
			}
			body := fmt.Sprintf(peerPlistTmpl, exe, int(interval.Seconds()), logFile, logFile)
			if err := os.WriteFile(plist, []byte(body), 0o644); err != nil {
				return err
			}
			label := "gui/" + strconv.Itoa(os.Getuid()) + "/com.dotfiles.peer"
			_, _ = runner.Run(ctx, "launchctl", "bootout", label)
			if _, err := runner.Run(ctx, "launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), plist); err != nil {
				return fmt.Errorf("loading %s: %w", plist, err)
			}
			p.Success("peer sync scheduled every %s", interval)
			p.KV("plist", plist)
			p.KV("log", logFile)
			return nil
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 15*time.Minute, "how often to sync with the peer")
	cmd.Flags().BoolVar(&off, "off", false, "remove the scheduled job")
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
			_, cfg, _, err := peerBootstrap(c)
			if err != nil {
				return err
			}
			runner := probeRunner()
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
	var pushOnly, pullOnly, skipHome bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Exchange workspace and host paths with the peer (both directions)",
		Args:  cobra.NoArgs,
		Long: `Pull the peer's changes, then push this machine's.

Pull first on purpose: it is the direction that can only add, so a conflict is
recorded before this machine's version goes out. The pull never deletes
anything locally.

When propagation.delete is on, deletions are detected before the pull runs and
applied to the peer before the push. Both halves of that order matter: the pull
would otherwise restore the deleted file, and the push ends by rewriting the
baseline the detection depends on.

Exits 0 when the peer is unreachable. That is what makes this safe to schedule
on a laptop.`,
		RunE: func(c *cobra.Command, _ []string) error {
			p := printerFrom(c)
			_, cfg, runner, err := peerBootstrap(c)
			if err != nil {
				return err
			}
			if !cfg.Target.IsSSH() {
				return fmt.Errorf("peer target is not an ssh target; run: dot peer init --host <user@host>")
			}
			ctx := context.Background()

			// One peer run at a time. The scheduled job and a manual run would
			// otherwise overlap and drive concurrent rsync writes into the same
			// tree and the same conflict directory.
			release, err := syncer.AcquireLock(cfg.LockDir)
			if err != nil {
				return fmt.Errorf("another peer sync is already running: %w", err)
			}
			defer release()

			probe := probeRunner()
			if err := syncer.CheckSSH(ctx, probe, cfg.Target.Host); err != nil {
				p.Warn("peer %s unreachable; nothing to do", cfg.Target.Host)
				return nil
			}
			rp, err := syncer.RemoteRsyncPath(ctx, probe, cfg.Target.Host)
			if err != nil {
				return err
			}
			cfg.RemoteRsyncPath = rp

			dryRun, _ := c.Flags().GetBool("dry-run")

			// Deletions are detected before the pull and applied before the
			// push, and the order is load-bearing in both directions. The pull
			// uses --update, so it would restore anything deleted here and
			// erase the evidence. The push ends with RefreshBaseline, which
			// walks the local tree and retires these keys, so a delete pass
			// that ran after it and failed would lose the tombstone and let the
			// peer's copy return on the next pull.
			//
			// Empty unless the profile is SSH with propagation.delete on.
			tombstones, err := syncer.ComputeTombstones(cfg)
			if err != nil {
				return err
			}
			cfg.Tombstones = tombstones
			conflict := syncer.NewConflictDir()

			if !pushOnly {
				p.Section("pull from peer")
				if err := reportPartial(p, syncer.PullDirect(ctx, runner, cfg, dryRun)); err != nil {
					return err
				}
			}
			if !pullOnly {
				if len(tombstones) > 0 {
					p.Section("propagate deletions")
					if err := syncer.PropagateDeletes(ctx, runner, cfg, conflict, tombstones, dryRun); err != nil {
						return err
					}
				}
				p.Section("push to peer")
				if err := reportPartial(p, syncer.Push(ctx, runner, cfg, dryRun)); err != nil {
					return err
				}
			}

			// Host paths are a separate pass because they live outside the
			// workspace and are addressed by an explicit list rather than the
			// workspace filter chain. Without them the workspace does not run on
			// the peer: 16 of its 19 submodule remotes are SSH.
			if !skipHome {
				p.Section("host paths")
				if err := reportPartial(p, peerHomeSync(ctx, runner, p, cfg, dryRun, pushOnly, pullOnly)); err != nil {
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
	cmd.Flags().BoolVar(&skipHome, "skip-home", false, "workspace only; skip the host-path pass")
	return cmd
}

// peerHomeSync exchanges the explicitly listed host paths.
//
// --ignore-missing-args matters: the list is shared between machines and a few
// entries legitimately do not exist on every one. A missing path must not abort
// a transfer that has already moved gigabytes.
func peerHomeSync(ctx context.Context, runner *exec.Runner, p *Printer, cfg *syncer.Config, dryRun, pushOnly, pullOnly bool) error {
	list := peerHomePathsFile(cfg.LocalPaths)
	if _, err := os.Stat(list); err != nil {
		p.Warn("no host-path list at %s; skipping", list)
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// --update and --backup are not optional here. Without them this pass
	// overwrites host config unconditionally in both directions, and the first
	// real run did exactly that: it replaced this machine's
	// ~/.ssh/known_hosts with the peer's copy, deleting the host key entry for
	// the very channel the sync runs over. The next ssh failed with "Host key
	// verification failed".
	//
	// The exclusions are machine-local trust and runtime state. known_hosts is
	// per-machine by nature - merging it is meaningless and losing it is
	// self-inflicted denial of service. Agent sockets are not files worth moving.
	conflict := NewHomeConflictDir()
	base := []string{"-aHAX", "--numeric-ids", "-r", "--human-readable", "--stats",
		"--ignore-missing-args", "--chmod=Du+w",
		"--update",
		"--backup", "--backup-dir=" + conflict,
		"--exclude=known_hosts", "--exclude=known_hosts.old", "--exclude=known_hosts2",
		"--exclude=agent", "--exclude=agent/**", "--exclude=*.sock",
		"--exclude=.DS_Store",
		"--files-from=" + list}
	if cfg.RemoteRsyncPath != "" {
		base = append(base, "--rsync-path="+cfg.RemoteRsyncPath)
	}
	if dryRun {
		base = append(base, "--dry-run")
	}
	base = append(base, "-e", "ssh")
	remote := cfg.Target.Host + ":"

	// Pull first, same reasoning as the workspace pass: the additive direction
	// records a conflict before this machine's version goes out.
	if !pushOnly {
		args := append(append([]string{}, base...), remote, home+"/")
		if err := runPeerRsync(ctx, runner, cfg, args); err != nil {
			return err
		}
	}
	if !pullOnly {
		args := append(append([]string{}, base...), home+"/", remote)
		if err := runPeerRsync(ctx, runner, cfg, args); err != nil {
			return err
		}
	}
	return nil
}

// NewHomeConflictDir names the quarantine directory for the host-path pass.
// Destination-relative, so each side keeps its own displaced copies.
func NewHomeConflictDir() string {
	return ".dot-peer-conflicts/" + time.Now().UTC().Format("2006-01-02T15-04-05Z")
}

func runPeerRsync(ctx context.Context, runner *exec.Runner, cfg *syncer.Config, args []string) error {
	if cfg.Verbose {
		return syncer.ClassifyRsyncError(runner.RunAttached(ctx, "rsync", args...))
	}
	_, err := runner.Run(ctx, "rsync", args...)
	return syncer.ClassifyRsyncError(err)
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
	// exec.Runner dereferences its logger, so nil panics on the first Info call.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := exec.NewRunner(dryRun, logger)
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
