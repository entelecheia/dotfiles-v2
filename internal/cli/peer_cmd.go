package cli

import (
	"context"
	"encoding/json"
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
#   .codex/config.toml         machine-local: Codex rewrites it continuously
#                              (per-project trust, hook state, plugin flags),
#                              and its MCP server definitions hash-key the
#                              Keychain-stored MCP OAuth credentials — copying
#                              a peer's config orphans them on both machines
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
			release, err := syncer.AcquireLock(cfg.LockDir)
			if err != nil {
				return fmt.Errorf("another sync is already running: %w", err)
			}
			defer release()

			plan, err := peerPlanForRun(ctx, probe, cfg)
			if err != nil {
				return err
			}
			if err := syncer.ValidatePeerPlanSafety(cfg, plan); err != nil {
				return err
			}

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

// peerPlanForRun builds the coordinator plan from one local inventory, one
// read-only remote inventory, and the last committed common baseline. The
// same helper is used by `peer sync` and `peer diff`; a displayed divergence
// therefore cannot disagree with the transaction that follows it.
func peerPlanForRun(ctx context.Context, runner *exec.Runner, cfg *syncer.Config) (*syncer.PeerPlan, error) {
	if cfg == nil || cfg.LocalPaths == nil {
		return nil, fmt.Errorf("peer plan: local paths unresolved")
	}
	baseline, err := syncer.LoadBaselineManifest(cfg.LocalPaths.BaselineFile)
	if err != nil {
		return nil, fmt.Errorf("peer plan: loading baseline: %w", err)
	}
	if err := syncer.ValidatePeerBaselineLocalTypes(cfg, baseline); err != nil {
		return nil, err
	}
	if err := syncer.PreparePeerPlanFilters(cfg); err != nil {
		return nil, fmt.Errorf("peer plan: preparing filters: %w", err)
	}
	local, err := syncer.InventoryPeer(cfg)
	if err != nil {
		return nil, err
	}
	remote, err := peerRemoteInventory(ctx, runner, cfg, baseline)
	if err != nil {
		return nil, err
	}
	return syncer.PlanPeerReconcile(baseline, local, remote)
}

// peerRemoteInventory uses an empty dry-run destination as a portable remote
// listing. `rsync --list-only` ignores --out-format on Apple's rsync, while a
// dry-run against an empty destination emits every remote file through the
// same rsync implementation used by the real transfer. No remote state is
// changed and paths omitted from the listing are unambiguously absent.
func peerRemoteInventory(ctx context.Context, runner *exec.Runner, cfg *syncer.Config, baseline map[string]syncer.Fingerprint) (syncer.PeerSnapshot, error) {
	if cfg == nil || !cfg.Target.IsSSH() {
		return nil, fmt.Errorf("peer inventory: target is not SSH")
	}
	root, err := os.MkdirTemp("", "dot-peer-inventory-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)

	args := []string{"-r", "--dry-run", "--no-links", "--out-format=@@%l\t%M\t%n"}
	args = append(args, syncer.PeerFilterArgs(cfg)...)
	if cfg.RemoteRsyncPath != "" {
		args = append(args, "--rsync-path="+cfg.RemoteRsyncPath)
	}
	args = append(args, "-e", "ssh", cfg.Target.RsyncDest(), root+"/")
	res, runErr := runner.Run(ctx, "rsync", args...)
	if runErr != nil {
		return nil, fmt.Errorf("peer inventory: %w", runErr)
	}

	remoteLoc, err := peerRemoteLocation(ctx, runner, cfg)
	if err != nil {
		return nil, err
	}
	requireNFD := syncer.NFDMigrationMarked(cfg.LocalPaths.WorkspaceRoot)
	return parsePeerRemoteInventory(res.Stdout, remoteLoc, baseline, requireNFD)
}

const remotePeerStatusCommand = `set -eu
dot_bin="$HOME/.local/bin/dot"
if [ ! -x "$dot_bin" ]; then
  echo "peer dot binary is missing: $dot_bin" >&2
  exit 127
fi
exec "$dot_bin" peer status --json`

const remotePeerNormalizeCommand = `set -eu
dot_bin="$HOME/.local/bin/dot"
if [ ! -x "$dot_bin" ]; then
  echo "peer dot binary is missing: $dot_bin" >&2
  exit 127
fi
exec "$dot_bin" sync names normalize --profile=peer --yes`

// checkRemotePeerOwner makes the single-coordinator invariant bilateral. A
// local owner guard alone is insufficient: two independently initialized Macs
// can each name themselves and both scheduled jobs would pass their own guard.
func checkRemotePeerOwner(ctx context.Context, runner *exec.Runner, cfg *syncer.Config) error {
	if cfg == nil || !cfg.Target.IsSSH() {
		return fmt.Errorf("peer coordinator check: target is not SSH")
	}
	if strings.TrimSpace(cfg.Owner) == "" {
		return fmt.Errorf("peer coordinator check: local peer owner is empty; set one with `dot sync owner --profile=peer --set <coordinator>`")
	}
	res, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", cfg.Target.Host, remotePeerStatusCommand)
	if err != nil {
		return fmt.Errorf("peer coordinator check: reading remote peer status: %w", err)
	}
	return validateRemotePeerStatus(cfg, res.Stdout)
}

func validateRemotePeerStatus(cfg *syncer.Config, raw string) error {
	var status peerStatusJSON
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return fmt.Errorf("peer coordinator check: invalid remote status JSON: %w", err)
	}
	if status.SchemaVersion != peerStatusSchemaVersion || status.Kind != "peer" || !status.Profile.Configured {
		return fmt.Errorf("peer coordinator check: remote peer profile is not configured with the supported schema")
	}
	wantOwner := syncer.NormalizeHostname(cfg.Owner)
	gotOwner := syncer.NormalizeHostname(status.Profile.Owner)
	if wantOwner == "" || gotOwner != wantOwner {
		return fmt.Errorf(
			"peer coordinator check: both profiles must name the same owner (local %q, remote %q); set the remote profile to %q and keep its scheduler off",
			cfg.Owner, status.Profile.Owner, cfg.Owner)
	}
	remoteWorkspace := filepath.Clean(status.Profile.WorkspacePath)
	wantRemoteWorkspace := filepath.Clean(cfg.Target.Path)
	remoteTarget := filepath.Clean(status.Profile.Target.Path)
	wantRemoteTarget := filepath.Clean(strings.TrimRight(cfg.LocalPath, "/"))
	if remoteWorkspace != wantRemoteWorkspace || remoteTarget != wantRemoteTarget {
		return fmt.Errorf(
			"peer coordinator check: remote profile does not point back to this workspace (remote workspace %q target %q; expected %q -> %q)",
			status.Profile.WorkspacePath, status.Profile.Target.Path, wantRemoteWorkspace, wantRemoteTarget)
	}
	return nil
}

func normalizeRemotePeerNames(ctx context.Context, runner *exec.Runner, cfg *syncer.Config) error {
	if _, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", cfg.Target.Host, remotePeerNormalizeCommand); err != nil {
		return fmt.Errorf("normalizing peer workspace names: %w", err)
	}
	return nil
}

func parsePeerRemoteInventory(stdout string, remoteLoc *time.Location, baseline map[string]syncer.Fingerprint, requireNFD bool) (syncer.PeerSnapshot, error) {
	out := syncer.PeerSnapshot{}
	for _, raw := range strings.Split(stdout, "\n") {
		if raw == "" {
			continue
		}
		if !strings.HasPrefix(raw, "@@") {
			const skippedPrefix = "skipping non-regular file "
			if strings.HasPrefix(raw, skippedPrefix) {
				skipped, err := strconv.Unquote(strings.TrimPrefix(raw, skippedPrefix))
				if err != nil {
					return nil, fmt.Errorf("peer inventory: malformed non-regular warning %q", raw)
				}
				rel, err := cleanPeerInventoryRel(skipped, false)
				if err != nil {
					return nil, err
				}
				if _, tracked := baseline[rel]; tracked {
					return nil, fmt.Errorf("peer inventory: baseline path %q is non-regular on the peer", rel)
				}
				// Unknown symlinks and sockets were never payloads. --no-links
				// deliberately omits them, so they cannot be edits or deletions.
				continue
			}
			// Stdout is reserved for the machine-readable --out-format. Treat a
			// continuation from a newline-bearing filename, or any other surprise,
			// as an incomplete inventory and fail closed.
			return nil, fmt.Errorf("peer inventory: unexpected rsync output %q", raw)
		}
		parts := strings.SplitN(strings.TrimPrefix(raw, "@@"), "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("peer inventory: malformed rsync record %q", raw)
		}
		rawRel := parts[2]
		isDir := strings.HasSuffix(rawRel, "/")
		rel, err := cleanPeerInventoryRel(rawRel, isDir)
		if err != nil {
			return nil, err
		}
		if requireNFD && rel != "" && !syncer.NFDPathNormalized(rel) {
			return nil, fmt.Errorf("peer inventory: path %q is not NFD-normalized; normalize the peer before retrying", rel)
		}
		// The dry-run listing includes directory traversal records. Inventory is
		// file-only, so skip them before trimming the rsync directory marker.
		// Keeping this check before TrimSuffix is important: treating a directory
		// as a zero-byte file would manufacture a peer change for every subtree.
		if isDir || rel == "" {
			continue
		}
		size, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("peer inventory: malformed size %q: %w", parts[0], err)
		}
		if size < 0 {
			return nil, fmt.Errorf("peer inventory: malformed negative size %q", parts[0])
		}
		mtime, err := parsePeerRsyncTime(parts[1], remoteLoc)
		if err != nil {
			return nil, fmt.Errorf("peer inventory: malformed mtime for %q: %w", rel, err)
		}
		if _, duplicate := out[rel]; duplicate {
			return nil, fmt.Errorf("peer inventory: duplicate path %q", rel)
		}
		out[filepath.ToSlash(rel)] = syncer.PeerFile{
			Present: true,
			FP:      syncer.Fingerprint{Size: size, Mtime: mtime},
		}
	}
	return out, nil
}

func cleanPeerInventoryRel(raw string, isDir bool) (string, error) {
	if strings.ContainsAny(raw, "\x00\r\n\t") {
		return "", fmt.Errorf("peer inventory: unsafe path %q", raw)
	}
	rel := strings.TrimPrefix(raw, "./")
	if isDir {
		rel = strings.TrimSuffix(rel, "/")
	}
	if rel == "" || rel == "." {
		return "", nil
	}
	if filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("peer inventory: unsafe path %q", raw)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != filepath.ToSlash(rel) {
		return "", fmt.Errorf("peer inventory: unsafe path %q", raw)
	}
	return clean, nil
}

func peerRemoteLocation(ctx context.Context, runner *exec.Runner, cfg *syncer.Config) (*time.Location, error) {
	res, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", cfg.Target.Host, "date +%z")
	if err != nil {
		return nil, fmt.Errorf("peer inventory: reading remote timezone: %w", err)
	}
	raw := strings.TrimSpace(res.Stdout)
	if len(raw) != 5 || (raw[0] != '+' && raw[0] != '-') {
		return nil, fmt.Errorf("peer inventory: unsupported remote timezone %q", raw)
	}
	hours, err := strconv.Atoi(raw[1:3])
	if err != nil {
		return nil, fmt.Errorf("peer inventory: unsupported remote timezone %q", raw)
	}
	minutes, err := strconv.Atoi(raw[3:5])
	if err != nil || hours > 23 || minutes > 59 {
		return nil, fmt.Errorf("peer inventory: unsupported remote timezone %q", raw)
	}
	offset := (hours*60 + minutes) * 60
	if raw[0] == '-' {
		offset = -offset
	}
	return time.FixedZone("peer", offset), nil
}

func parsePeerRsyncTime(raw string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range []string{"2006/01/02-15:04:05", "2006/01/02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(raw), loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported rsync time %q", raw)
}

func intersectPeerPaths(a, b []string) []string {
	want := make(map[string]bool, len(b))
	for _, rel := range b {
		want[rel] = true
	}
	out := make([]string, 0, len(a))
	for _, rel := range a {
		if want[rel] {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
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
			if err := syncer.CheckOwner(cfg); err != nil {
				return fmt.Errorf("refusing peer scheduler on a non-coordinator: %w", err)
			}
			if interval < time.Minute {
				return fmt.Errorf("--interval must be at least 1m (got %s)", interval)
			}
			if !cfg.Target.IsSSH() {
				return fmt.Errorf("peer target is not configured; run dot peer init first")
			}
			probe := probeRunner()
			if err := syncer.CheckSSH(ctx, probe, cfg.Target.Host); err != nil {
				return fmt.Errorf("checking peer coordinator before scheduler setup: %w", err)
			}
			if err := checkRemotePeerOwner(ctx, probe, cfg); err != nil {
				return err
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
			_, cfg, runner, err := peerBootstrap(c)
			if err != nil {
				return err
			}
			if !cfg.Target.IsSSH() {
				return fmt.Errorf("peer target is not an ssh target; run: dot peer init --host <user@host>")
			}
			// The profile owner is the coordinator. This guard is intentionally
			// before any probe or transfer: a second machine must not perform a
			// half-run and then leave a different baseline behind.
			if err := syncer.CheckOwner(cfg); err != nil {
				return err
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
			if err := checkRemotePeerOwner(ctx, probe, cfg); err != nil {
				return err
			}

			dryRun, _ := c.Flags().GetBool("dry-run")
			if !dryRun {
				// Normalize opted-in NFD names before tombstones, inventory, plans,
				// and rsync filters materialize. The helper is marker-gated, so an
				// unmarked workspace remains unchanged until its explicit migration.
				if err := syncer.NormalizeWorkspaceNamesBeforePush(cfg); err != nil {
					return fmt.Errorf("normalizing workspace names: %w", err)
				}
				if syncer.NFDMigrationMarked(cfg.LocalPaths.WorkspaceRoot) {
					if err := normalizeRemotePeerNames(ctx, runner, cfg); err != nil {
						return err
					}
				}
			}

			// Compute deletion evidence before any pull mutates the coordinator
			// tree, then build the same three-way plan used by `peer diff`.
			tombstones, err := syncer.ComputeTombstones(cfg)
			if err != nil {
				return err
			}
			cfg.Tombstones = tombstones
			plan, err := peerPlanForRun(ctx, probe, cfg)
			if err != nil {
				return err
			}
			if err := syncer.ValidatePeerPlanSafety(cfg, plan); err != nil {
				return err
			}
			conflict := syncer.NewConflictDir()
			complete := true
			baselineReady, err := syncer.PeerBaselineReady(cfg)
			if err != nil {
				return err
			}
			// A target marker authorizes destructive transitions. An initial
			// additive bootstrap remains allowed, but an unproven local/remote
			// deletion must stay pending and must not retire the baseline.
			deletesAuthorized := baselineReady

			if !pushOnly {
				p.Section("pull from peer")
				pullErr := syncer.PullPeerPlan(ctx, runner, cfg, plan, dryRun)
				if err := reportPartial(p, pullErr); err != nil {
					return err
				}
				if cfg.Propagation.Delete && deletesAuthorized && len(plan.DeleteLocal) > 0 {
					if err := syncer.DeletePeerLocal(cfg, conflict, plan.DeleteLocal, dryRun); err != nil {
						return err
					}
				} else if len(plan.DeleteLocal) > 0 {
					complete = false
					p.Warn("remote-only deletes held: peer baseline provenance is not established")
				}
			}
			if !pullOnly {
				deleteSet := intersectPeerPaths(tombstones, plan.DeleteRemote)
				if !cfg.Propagation.Delete {
					deleteSet = nil
				}
				if len(deleteSet) > 0 && deletesAuthorized {
					p.Section("propagate deletions")
					if err := syncer.PropagateDeletes(ctx, runner, cfg, conflict, deleteSet, dryRun); err != nil {
						return err
					}
				} else if len(plan.DeleteRemote) > 0 {
					complete = false
					p.Warn("local deletes held: peer baseline provenance is not established")
				}
				p.Section("push to peer")
				pushErr := syncer.PushPeerPlan(ctx, runner, cfg, plan, conflict, dryRun)
				if err := reportPartial(p, pushErr); err != nil {
					return err
				}
			}

			// Host paths are a separate pass because they live outside the
			// workspace and are addressed by an explicit list rather than the
			// workspace filter chain. Without them the workspace does not run on
			// the peer: 16 of its 19 submodule remotes are SSH.
			if !skipHome {
				p.Section("host paths")
				homeErr := peerHomeSync(ctx, runner, p, cfg, dryRun, pushOnly, pullOnly)
				if err := reportPartial(p, homeErr); err != nil {
					return err
				}
			}
			// A first successful additive run establishes provenance. A target
			// marker is not required for that bootstrap, but any held deletion is
			// enough to keep the baseline unchanged until a later verified run.
			canCommitBaseline := baselineReady ||
				(len(plan.DeleteLocal) == 0 && len(plan.DeleteRemote) == 0)
			if complete && !dryRun && !pullOnly {
				if err := syncer.AppendPeerConflictAudit(cfg, plan); err != nil {
					return err
				}
			}
			if complete && !dryRun && !pushOnly && !pullOnly && canCommitBaseline {
				if err := syncer.CommitPeerBaseline(cfg); err != nil {
					return err
				}
			}
			p.Blank()
			if complete {
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
	// --update keeps a peer's newer host config from being overwritten by a
	// stale coordinator copy. Ordinary host-path updates are deliberately not
	// backed up: they are not workspace conflicts, and an unconditional backup
	// on every run made `.dot-peer-conflicts` grow with routine state churn. A
	// future verified host-baseline conflict can opt into a scoped backup.
	// Without --update this pass overwrites host config unconditionally, and the
	// first real run did exactly that: it replaced this machine's
	// ~/.ssh/known_hosts with the peer's copy, deleting the host key entry for
	// the very channel the sync runs over. The next ssh failed with "Host key
	// verification failed".
	//
	// The exclusions are machine-local trust and runtime state. known_hosts is
	// per-machine by nature - merging it is meaningless and losing it is
	// self-inflicted denial of service. Agent sockets are not files worth moving.
	// .codex/config.toml is excluded in code, not just from the seed template:
	// home-paths.txt is seed-once, so lists written before the entry was removed
	// still carry it, and Codex hash-keys its Keychain MCP OAuth credentials to
	// this file's server definitions - copying a peer's copy orphans them.
	base := []string{"-aHAX", "--numeric-ids", "-r", "--human-readable", "--stats",
		"--ignore-missing-args", "--chmod=Du+w",
		"--update",
		"--exclude=known_hosts", "--exclude=known_hosts.old", "--exclude=known_hosts2",
		"--exclude=agent", "--exclude=agent/**", "--exclude=*.sock",
		"--exclude=/.codex/config.toml",
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

func runPeerRsync(ctx context.Context, runner *exec.Runner, cfg *syncer.Config, args []string) error {
	if cfg.Verbose {
		return syncer.ClassifyRsyncError(runner.RunAttached(ctx, "rsync", args...))
	}
	_, err := runner.Run(ctx, "rsync", args...)
	return syncer.ClassifyRsyncError(err)
}

// reportPartial annotates rsync's partial-transfer outcome, then returns it as
// a hard transaction failure. Exit 23/24 can mean a scoped conflict or delete
// did not finish; later passes and baseline publication must stop immediately.
func reportPartial(p *Printer, err error) error {
	if err == nil {
		return nil
	}
	if syncer.IsPartialTransfer(err) {
		p.Warn("partial transfer: %v", err)
		p.Line("  Peer transaction stopped; baseline unchanged. Re-run to retry it.")
		return fmt.Errorf("peer sync incomplete: %w", err)
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
