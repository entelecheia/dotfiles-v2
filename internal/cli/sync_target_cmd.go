package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/appsettings"
	"github.com/entelecheia/dotfiles-v2/internal/syncer"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
	"github.com/spf13/cobra"
)

// dot sync target, mirror, init and owner: the per-workspace store's
// destination, layout and writer.

func newSyncTargetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "target [spec]",
		Short: "Show or set the sync target (local mirror dir or SSH remote)",
		Long: `With no argument, prints the resolved sync target.

With a spec, sets the target in this workspace's local config
(<workspace>/.dotfiles/sync/config.yaml) so it takes effect immediately.
Accepted forms:

  local:~/Dropbox/work       local directory (a cloud client's folder)
  ssh:user@host:~/work       rsync over SSH
  ~/Dropbox/work             bare path — shorthand for local:

Local targets are also recorded in the global user state so future
workspaces inherit them.`,
		Args:         cobra.MaximumNArgs(1),
		RunE:         runSyncTarget,
		SilenceUsage: true,
	}
}

func newSyncMirrorCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "mirror [path]",
		Short:        "Show or set the local mirror path",
		Deprecated:   "use 'dot sync target' instead.",
		Args:         cobra.MaximumNArgs(1),
		RunE:         runSyncTarget,
		SilenceUsage: true,
	}
}

func runSyncTarget(cmd *cobra.Command, args []string) error {
	p := printerFrom(cmd)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	home := homeFromCmd(cmd) // honors the persistent --home override

	var target syncer.Target
	if len(args) == 1 {
		t, err := syncer.ParseTarget(args[0])
		if err != nil {
			return err
		}
		if t.Kind == syncer.TargetLocal {
			t.Path = appsettings.ExpandHome(t.Path, home)
		}
		target = t
	}

	// Print + dry-run are read-only: use the read-only bootstrap so neither
	// creates the per-workspace .dotfiles/sync layout or touches
	// .gitignore on first use.
	if len(args) == 0 || dryRun {
		bs, err := syncer.Bootstrap(syncBootstrapOptions(cmd, true))
		if err != nil {
			return err
		}
		if len(args) == 0 {
			p.KV("Target", bs.Config.Target.String())
			return nil
		}
		p.Line("[dry-run] would set target to %s (local config%s)", target.String(),
			map[bool]string{true: " + global state", false: ""}[target.Kind == syncer.TargetLocal])
		return nil
	}

	homeOverride, _ := cmd.Flags().GetString("home")

	// Local config governs the current workspace (global state is ignored
	// once it exists), so write it for immediate effect — but only for the
	// current user. Under --home the admin isn't in the target user's
	// workspace, so the local config (always current-workspace) doesn't
	// apply; only the home-aware global state below is meaningful there.
	if homeOverride == "" {
		bs, err := syncer.Bootstrap(syncBootstrapOptions(cmd, false))
		if err != nil {
			return err
		}
		if err := syncer.SetLocalTarget(bs.Config, target); err != nil {
			return err
		}
	}

	// Global state, home-aware: local targets are inherited by future
	// workspaces. SSH targets stay workspace-local.
	if target.Kind == syncer.TargetLocal {
		state, err := loadStateForCmd(cmd)
		if err != nil {
			return fmt.Errorf("load global state: %w", err)
		}
		state.Modules.Gsync.MirrorPath = target.Path
		if err := persistUserState(cmd, state); err != nil {
			p.Warn("could not update global state: %v", err)
		}
	}

	p.Line("%s", ui.StyleSuccess.Render("✓ sync target set"))
	p.KV("Target", target.String())
	return nil
}

func newSyncInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize <workspace>/.dotfiles/sync/ from current state",
		Long: `One-time onboarding for the per-workspace store. Creates
<workspace>/.dotfiles/sync/ with config.yaml, include.txt, exclude.txt,
ignore.txt, manifests, log dir; appends '/.dotfiles/' to <workspace>/.gitignore
so the store is never committed; and creates <workspace>/inbox/gdrive/ if
missing.

Idempotent — re-running on a populated store leaves operator edits intact and
just heals any missing pieces.`,
		RunE:         runSyncInit,
		SilenceUsage: true,
	}
}

func runSyncInit(cmd *cobra.Command, _ []string) error {
	bs, err := syncer.Bootstrap(syncBootstrapOptions(cmd, false))
	if err != nil {
		return err
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	p := printerFrom(cmd)
	res, err := syncer.InitStore(bs.Config, dryRun)
	if err != nil {
		return err
	}
	if res.DryRun {
		if res.LegacyStoreDir != "" {
			p.Line("dry-run: would rename %s to %s", res.LegacyStoreDir, res.StoreDir)
		}
		p.Line("dry-run: would create %s", res.StoreDir)
		for _, f := range []string{res.ConfigFile, res.IncludeFile, res.IgnoreFile} {
			p.Line("dry-run: would create %s", f)
		}
		p.Line("dry-run: would create %s", res.InboxDir)
		p.Line("dry-run: would append the managed block to %s", res.WorkspaceIgnore)
		return nil
	}

	p.Header("gsync workspace initialized")
	p.KV("Store", res.StoreDir)
	p.KV("Workspace", res.Workspace)
	p.KV("Mirror", res.Mirror)
	p.KV("Propagation", res.Propagation.String())
	p.KV("Filter mode", res.FilterMode.String())
	p.KV("Inbox staging", res.InboxDir)
	p.Blank()
	p.Line("Edit %s to customize behavior; %s for include patterns; %s for additional ignore patterns.", res.ConfigFile, res.IncludeFile, res.IgnoreFile)
	p.Line("Run 'dot sync setup' to verify rsync and keep automatic sync disabled unless intervals are passed.")
	return nil
}

// newSyncOwnerCmd shows or moves the writer of a profile.
//
// The mirror is a single-writer channel: each machine keeps its own baseline and
// the mirror profile runs with delete propagation on, so two pushers take turns
// undoing each other. Ownership is therefore an explicit, recorded decision
// rather than something inferred at run time.
func newSyncOwnerCmd() *cobra.Command {
	var setSelf bool
	var setTo string
	var clearOwner bool
	var rename, localOnly bool
	cmd := &cobra.Command{
		Use:   "owner [--rename <old> <new>]",
		Short: "Show or set which machine may push this profile",
		Long: `Show or set which machine may push this profile.

Renaming a Mac: run dot sync owner --rename <old> <new> on that Mac. It
rewrites the owner in every profile of this workspace owned by <old> (mirror
and peer alike), then does the same on the peer over ssh unless --local-only.
Only a profile whose current owner is <old> is renamed. The old name stays
as an alias, so the guard and the peer's owner check keep matching while
either Mac still answers to it; a generic name such as "Mac" is not kept.
The coordinator retires its aliases at the first complete peer sync that
finds the peer recording the new owner, once this Mac answers to the new
name; the peer's copies stay until its owner next changes. At equal epochs
the peer fence refuses a peer that passes its own owner guard. When the peer
cannot be reached, the rename runs only on a Mac that still answers to <old>
(or with --local-only). The epoch, targets and baselines are untouched, so no
run plans a deletion. It refuses when this Mac answers to neither name, when
the peer (or the peer target's host) answers to either one, and, once this
Mac no longer answers to <old>, unless it runs the peer scheduler and the
peer, asked then, runs none (without a peer, or a peer that cannot answer,
only --local-only renames it): moving ownership between the Macs is --set or
dot peer handover. --local-only skips these checks; it is the step the
command runs on the peer. A retry after a peer failure goes on to the peer.
--dry-run shows the change without writing anything.

Keep the peer target's ssh alias through a rename: the target is part of the
baseline identity (baseline.peer-target), and editing target: in the peer
config resets the baseline. Point the old alias at the new host name in
~/.ssh/config instead.`,
		Args: func(c *cobra.Command, args []string) error {
			if rename {
				return cobra.ExactArgs(2)(c, args)
			}
			return cobra.NoArgs(c, args)
		},
		RunE: func(c *cobra.Command, args []string) error {
			if rename {
				return runSyncOwnerRename(c, args[0], args[1], localOnly)
			}
			if localOnly {
				return fmt.Errorf("--local-only only applies to --rename")
			}
			return runSyncOwner(c, syncer.OwnerOptions{Clear: clearOwner, SetSelf: setSelf, SetTo: setTo})
		},
	}
	cmd.Flags().BoolVar(&setSelf, "set-self", false, "claim ownership for this machine")
	cmd.Flags().StringVar(&setTo, "set", "", "set ownership to a specific machine name")
	cmd.Flags().BoolVar(&clearOwner, "clear", false, "remove the ownership restriction")
	cmd.Flags().BoolVar(&rename, "rename", false, "record a machine rename <old> <new> in every profile, here and on the peer")
	cmd.Flags().BoolVar(&localOnly, "local-only", false, "with --rename, leave the peer alone")
	cmd.MarkFlagsMutuallyExclusive("rename", "set", "set-self", "clear")
	return cmd
}

// runSyncOwnerRename migrates every profile owned by oldName, then the peer's
// profiles over ssh. It runs on the machine being renamed, and never moves
// ownership to or from the peer: that is --set or a handover, with an epoch.
// A peer that cannot be reached or updated is reported with the command to
// run there; the alias keeps the pair working meanwhile.
func runSyncOwnerRename(cmd *cobra.Command, oldName, newName string, localOnly bool) error {
	p := printerFrom(cmd)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	// Read-only: the rename needs only the workspace root, and a refused
	// rename must not migrate any store on the way.
	bs, err := syncer.Bootstrap(syncBootstrapOptions(cmd, true))
	if err != nil {
		return err
	}
	names := syncer.MachineNames()
	manual := "dot sync owner --rename " + shellArg(oldName) + " " + shellArg(newName) + " --local-only"

	var peer *syncer.BootstrapResult
	if !localOnly {
		answersOld := answersTo(names, oldName)
		if !answersOld && !answersTo(names, newName) {
			return fmt.Errorf("this machine answers to %s, neither %q nor %q; run --rename on the machine being renamed", strings.Join(names, ", "), oldName, newName)
		}
		var perr error
		peer, perr = peerBootstrapReadOnly(cmd)
		switch {
		case perr != nil:
			p.Warn("peer profile could not be read (%v); the peer will not be migrated", perr)
			peer = nil
		case !peer.Config.Target.IsSSH():
			peer = nil
		}
		checked := false
		var peerView *syncer.PeerView
		if peer != nil {
			// The target host names the other Mac. A user@ prefix is not part
			// of the host.
			host := peer.Config.Target.Host
			if i := strings.LastIndex(host, "@"); i >= 0 {
				host = host[i+1:]
			}
			if answersTo([]string{host}, oldName) || answersTo([]string{host}, newName) {
				return fmt.Errorf("the peer target %q is %q or %q: a rename cannot move ownership between the Macs; use dot sync owner --set or dot peer handover", peer.Config.Target.Host, oldName, newName)
			}
			view, err := syncer.PeerOwnerView(cmd.Context(), probeRunner(), peer.Config)
			switch {
			case err != nil:
				p.Warn("peer %s could not be read (%v); it cannot be checked or migrated now", peer.Config.Target.Host, err)
				peer = nil
			case answersTo(view.MachineNames, oldName) || answersTo(view.MachineNames, newName):
				return fmt.Errorf("the peer answers to %s, which includes %q or %q: a rename cannot move ownership between the Macs; use dot sync owner --set or dot peer handover", strings.Join(view.MachineNames, ", "), oldName, newName)
			default:
				checked, peerView = true, view
			}
		}
		// Once this Mac no longer answers to <old>, nothing in the names
		// shows that it, and not the other Mac, is the owner being renamed:
		// after both host renames neither answers to <old>. Both sides must
		// then agree which Mac runs the owner's peer scheduler: this one does
		// and the peer, asked just now, does not. Anything less (no peer
		// profile, a peer store that does not load, a peer that cannot
		// answer, a stale plist on both) refuses; --local-only on the Mac
		// being renamed stays the explicit override.
		if !answersOld {
			root := strings.TrimRight(bs.Config.LocalPath, "/")
			switch {
			case !peerStoreExists(root):
				return fmt.Errorf("this machine no longer answers to %q and there is no peer to confirm which Mac is the owner; run --rename while the owner still answers to %q, or with --local-only on the owner", oldName, oldName)
			case !checked:
				return fmt.Errorf("the peer could not confirm that it is not %q or %q, and this machine no longer answers to %q; wake the peer and retry, or run with --local-only on the Mac being renamed", oldName, newName, oldName)
			case !runsPeerScheduler(bs):
				return fmt.Errorf("this machine no longer answers to %q and does not run the peer scheduler, so it cannot be shown to be the owner being renamed; run --rename on the coordinator (the Mac with the peer scheduler), where --local-only is also allowed", oldName)
			case peerView.Scheduler != syncer.SchedulerNotInstalled.String():
				return fmt.Errorf("both Macs claim the owner's peer scheduler (the peer reports %q), so neither can be shown to be the owner being renamed; remove the stale one with dot peer setup --off there, or run with --local-only on the owner", peerView.Scheduler)
			}
		}
	}

	root := strings.TrimRight(bs.Config.LocalPath, "/")
	res, err := syncer.RenameOwner(root, oldName, newName, dryRun)
	if err != nil {
		return err
	}
	verb := "owner"
	if dryRun {
		verb = "dry-run: would set owner"
	}
	for _, profile := range res.Profiles {
		p.Success("profile %q: %s %q (was %q, kept as an alias)", profile, verb, newName, oldName)
	}
	for _, profile := range res.Already {
		p.Success("profile %q: already renamed to %q", profile, newName)
	}
	p.KV("this machine", strings.Join(names, ", "))
	if localOnly {
		return nil
	}
	if peer == nil {
		p.Line("  On the other Mac, when reachable: %s", manual)
		return nil
	}
	host := peer.Config.Target.Host
	if dryRun {
		p.Line("dry-run: would run on %s: %s", host, manual)
		return nil
	}
	if err := syncer.RenamePeerOwner(cmd.Context(), probeRunner(), peer.Config, oldName, newName); err != nil {
		p.Warn("peer %s not migrated: %v", host, err)
		p.Line("  Run there: %s", manual)
		return fmt.Errorf("renamed here, but the peer %s was not migrated", host)
	}
	p.Success("peer %s: profiles owned by %q renamed to %q", host, oldName, newName)
	return nil
}

// peerStoreExists reports a peer profile store, loadable or not: a store
// that fails to load must not read as "no peer".
func peerStoreExists(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".dotfiles", syncer.PeerProfile, "config.yaml"))
	return err == nil
}

// runsPeerScheduler reports the peer scheduler's plist on this machine.
// Alone it proves nothing (a --set leaves the old coordinator's plist); the
// rename also asks the peer.
func runsPeerScheduler(bs *syncer.BootstrapResult) bool {
	_, err := os.Stat(filepath.Join(bs.Config.HomeDir(), "Library", "LaunchAgents", "com.dotfiles.peer.plist"))
	return err == nil
}

func answersTo(names []string, name string) bool {
	want := syncer.NormalizeHostname(name)
	for _, n := range names {
		if syncer.NormalizeHostname(n) == want {
			return true
		}
	}
	return false
}

// shellArg quotes an argument for a printed command line when it needs it.
func shellArg(s string) string {
	unsafe := func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.'
	}
	if s != "" && strings.IndexFunc(s, unsafe) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func runSyncOwner(cmd *cobra.Command, opts syncer.OwnerOptions) error {
	p := printerFrom(cmd)
	bs, err := syncer.Bootstrap(syncBootstrapOptions(cmd, false))
	if err != nil {
		return err
	}
	cfg := bs.Config
	if cfg.LocalPaths == nil {
		return fmt.Errorf("profile store unresolved")
	}
	names := syncer.MachineNames()

	if !opts.SetSelf && opts.SetTo == "" && !opts.Clear {
		p.KV("profile", cfg.Profile)
		if strings.TrimSpace(cfg.Owner) == "" {
			p.KV("owner", "(unset - any machine may push)")
		} else {
			p.KV("owner", cfg.Owner)
		}
		if len(cfg.OwnerAliases) > 0 {
			p.KV("aliases", strings.Join(cfg.OwnerAliases, ", "))
		}
		p.KV("this machine", strings.Join(names, ", "))
		if err := syncer.CheckOwner(cfg); err != nil {
			p.Warn("this machine may NOT push this profile")
		} else {
			p.Success("this machine may push this profile")
		}
		return nil
	}

	opts.Config = cfg
	owner, err := syncer.SetOwner(opts)
	if err != nil {
		return err
	}
	if owner == "" {
		p.Success("owner cleared for profile %q (any machine may push)", cfg.Profile)
	} else {
		p.Success("owner of profile %q is now %q", cfg.Profile, owner)
	}
	return nil
}
