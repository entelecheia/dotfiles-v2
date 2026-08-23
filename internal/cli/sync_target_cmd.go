package cli

import (
	"fmt"
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
	p := printerFrom(cmd)
	res, err := syncer.InitStore(bs.Config)
	if err != nil {
		return err
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
	cmd := &cobra.Command{
		Use:   "owner",
		Short: "Show or set which machine may push this profile",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runSyncOwner(c, syncer.OwnerOptions{Clear: clearOwner, SetSelf: setSelf, SetTo: setTo})
		},
	}
	cmd.Flags().BoolVar(&setSelf, "set-self", false, "claim ownership for this machine")
	cmd.Flags().StringVar(&setTo, "set", "", "set ownership to a specific machine name")
	cmd.Flags().BoolVar(&clearOwner, "clear", false, "remove the ownership restriction")
	return cmd
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
