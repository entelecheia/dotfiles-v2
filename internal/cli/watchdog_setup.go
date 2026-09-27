package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"runtime"
	"strconv"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
	"github.com/entelecheia/dotfiles-v2/internal/watchdog"
)

// dot watchdog setup / status / notify / log / uninstall.

func newWatchdogSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "setup",
		Short:        "Install the watchdog reaper LaunchAgent (macOS)",
		Args:         cobra.NoArgs,
		RunE:         func(cmd *cobra.Command, args []string) error { return runWatchdogSetupForGOOS(cmd, args, runtime.GOOS) },
		SilenceUsage: true,
	}
}

func runWatchdogSetupForGOOS(cmd *cobra.Command, _ []string, goos string) error {
	if goos != "darwin" {
		return fmt.Errorf("dot watchdog setup is macOS-only")
	}
	if homeOverride, _ := cmd.Flags().GetString("home"); homeOverride != "" {
		return fmt.Errorf("--home is not supported; the watchdog LaunchAgent manages this Mac's user domain")
	}
	p := printerFrom(cmd)
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	wcfg, err := loadWatchdogConfig(cmd)
	if err != nil {
		return err
	}
	if !wcfg.Enabled {
		return fmt.Errorf("watchdog is disabled in the active profile; set watchdog.enabled: true or pass --config with a host config that enables it")
	}
	settings, err := watchdog.ResolveReaper(wcfg.Reaper)
	if err != nil {
		return err
	}

	p.Header("dot watchdog setup")
	p.KV("Mode", settings.Mode)
	p.KV("Interval", settings.Interval.String())
	p.KV("CPU threshold", fmt.Sprintf("%.0f%% for %s", settings.CPUThreshold, settings.Sustain))
	ok, err := ui.Confirm("Install the reaper LaunchAgent "+watchdog.ReapLabel+"?", yes)
	if err != nil {
		return err
	}
	if !ok {
		p.Line("Aborted.")
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	mgr := watchdog.NewManager(watchdogRunner(dryRun), home)
	if dryRun {
		p.Line("[dry-run] would write %s", mgr.SnapshotPath())
		p.Line("[dry-run] would install and load %s (every %s)", mgr.PlistPath(), settings.Interval)
		return nil
	}
	dotPath, err := osexec.LookPath("dot")
	if err != nil {
		return fmt.Errorf("cannot find dot binary in PATH; run `make install` first")
	}
	snapshot, err := yaml.Marshal(wcfg)
	if err != nil {
		return fmt.Errorf("serializing watchdog config: %w", err)
	}
	if err := mgr.Install(context.Background(), dotPath, settings.Interval, snapshot); err != nil {
		return err
	}
	p.Success("✓ dot watchdog setup complete (mode: %s)", settings.Mode)
	p.Line("Check with: dot watchdog status · Logs: dot watchdog log")
	return nil
}

func newWatchdogStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "status",
		Short:        "Show watchdog config, LaunchAgent, and sample state",
		Args:         cobra.NoArgs,
		RunE:         runWatchdogStatus,
		SilenceUsage: true,
	}
}

func runWatchdogStatus(cmd *cobra.Command, _ []string) error {
	p := printerFrom(cmd)
	mgr := watchdog.NewManager(watchdogRunner(false), homeFor(cmd))
	p.Header("dot watchdog status")
	switch wcfg, err := loadWatchdogSnapshot(mgr); {
	case err == nil:
		settings, rerr := watchdog.ResolveReaper(wcfg.Reaper)
		if rerr != nil {
			return rerr
		}
		p.KV("Config", mgr.SnapshotPath())
		p.KV("Enabled", "yes")
		p.KV("Mode", settings.Mode)
		p.KV("CPU threshold", fmt.Sprintf("%.0f%% sustained %s", settings.CPUThreshold, settings.Sustain))
	case errors.Is(err, ErrNoWatchdogSnapshot):
		p.KV("Config", "no snapshot — run `dot watchdog setup`")
	default:
		// A snapshot that exists but cannot be read or parsed is a damaged
		// scheduled configuration; it must not masquerade as "not installed".
		p.KV("Config", "unreadable: "+err.Error())
	}
	if runtime.GOOS == "darwin" {
		st := mgr.Probe(context.Background())
		p.KV("Plist", filePresence(mgr.PlistPath()))
		p.KV("LaunchAgent", boolStr(st.Loaded))
	} else {
		p.KV("LaunchAgent", "(macOS only)")
	}
	if samples, err := watchdog.LoadSamples(mgr.SamplesPath()); err == nil {
		p.KV("Tracked processes", strconv.Itoa(len(samples)))
	}
	p.KV("Log", filePresence(mgr.LogPath()))
	return nil
}

func newWatchdogNotifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "notify <level> <message>",
		Short:        "Send a watchdog alert (macOS notification and/or ntfy)",
		Args:         cobra.ExactArgs(2),
		RunE:         runWatchdogNotify,
		SilenceUsage: true,
	}
}

func runWatchdogNotify(cmd *cobra.Command, args []string) error {
	mgr := watchdog.NewManager(watchdogRunner(false), homeFor(cmd))
	var ncfg config.WatchdogNotifyConfig
	switch wcfg, err := loadWatchdogSnapshot(mgr); {
	case err == nil:
		ncfg = wcfg.Notify
	case errors.Is(err, ErrNoWatchdogSnapshot):
		// Only a genuinely missing install falls back to the live profile
		// config; a damaged snapshot fails instead of notifying from stale
		// profile values the scheduled reaper no longer matches.
		wcfg, err := loadWatchdogConfig(cmd)
		if err != nil {
			return err
		}
		ncfg = wcfg.Notify
	default:
		return err
	}
	notifier := watchdog.NewNotifier(watchdog.ResolveNotify(ncfg), watchdogRunner(false), runtime.GOOS)
	if err := notifier.Notify(cmd.Context(), args[0], args[1]); err != nil {
		return err
	}
	p := printerFrom(cmd)
	p.Success("✓ notification sent (%s)", args[0])
	return nil
}

func newWatchdogLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "log [N]",
		Short:        "Tail the watchdog JSON-lines event log",
		Args:         cobra.MaximumNArgs(1),
		RunE:         runWatchdogLog,
		SilenceUsage: true,
	}
}

func runWatchdogLog(cmd *cobra.Command, args []string) error {
	n := 50
	if len(args) > 0 {
		parsed, err := strconv.Atoi(args[0])
		if err != nil || parsed <= 0 {
			return fmt.Errorf("log line count must be a positive integer")
		}
		n = parsed
	}
	mgr := watchdog.NewManager(watchdogRunner(false), homeFor(cmd))
	p := printerFrom(cmd)
	lines, err := watchdog.TailLog(mgr.LogPath(), n)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		p.Line("No log file found at %s", mgr.LogPath())
		return nil
	}
	for _, line := range lines {
		p.Line("%s", line)
	}
	return nil
}

func newWatchdogUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the watchdog reaper LaunchAgent (macOS)",
		Long: `Unload and remove the reaper LaunchAgent. Removing the state directory
and the log is an interactive-only prompt that defaults to No; --yes never
auto-confirms it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWatchdogUninstallForGOOS(cmd, args, runtime.GOOS)
		},
		SilenceUsage: true,
	}
}

func runWatchdogUninstallForGOOS(cmd *cobra.Command, _ []string, goos string) error {
	if goos != "darwin" {
		return fmt.Errorf("dot watchdog uninstall is macOS-only")
	}
	if homeOverride, _ := cmd.Flags().GetString("home"); homeOverride != "" {
		return fmt.Errorf("--home is not supported; the watchdog LaunchAgent manages this Mac's user domain")
	}
	p := printerFrom(cmd)
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	mgr := watchdog.NewManager(watchdogRunner(dryRun), home)
	if dryRun {
		p.Line("[dry-run] would unload and remove %s", mgr.PlistPath())
		return nil
	}
	if err := mgr.Uninstall(context.Background()); err != nil {
		return err
	}
	p.Line("Removed LaunchAgent plist (if present).")
	removeState, err := ui.ConfirmBool(
		fmt.Sprintf("Remove watchdog state and logs (%s, %s)?", mgr.StateDir(), mgr.LogPath()), false, yes)
	if err != nil {
		return err
	}
	if removeState {
		if err := mgr.Runner.RemoveAll(mgr.StateDir()); err != nil {
			return fmt.Errorf("removing state dir: %w", err)
		}
		if mgr.Runner.FileExists(mgr.LogPath()) {
			if err := mgr.Runner.Remove(mgr.LogPath()); err != nil {
				return fmt.Errorf("removing log: %w", err)
			}
		}
		p.Line("Removed watchdog state and logs.")
	}
	p.Success("✓ dot watchdog uninstalled")
	return nil
}
