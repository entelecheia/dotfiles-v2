package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/watchdog"
)

func newWatchdogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watchdog",
		Short: "Reap runaway processes and alert on host health",
		Long: `Watchdog guards this host against runaway orphaned processes (reaper),
with connectivity heal, power hardening, and external monitors landing in
later phases. The reaper runs from a user LaunchAgent installed by
'dot watchdog setup'; it is disabled by default and opted in per host via
the watchdog section of the profile config.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		SilenceUsage: true,
	}
	cmd.AddCommand(
		newWatchdogSetupCmd(),
		newWatchdogStatusCmd(),
		newWatchdogReapCmd(),
		newWatchdogNotifyCmd(),
		newWatchdogLogCmd(),
		newWatchdogUninstallCmd(),
	)
	return cmd
}

func newWatchdogReapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reap",
		Short: "Run one reaper pass (the LaunchAgent invokes this)",
		Long: `Sample the process table once, fold it into the cross-run history, and
act on every process that matches the watchdog config, is not allowlisted,
and has held CPU at or above the threshold for at least the sustain window.
dry-run mode only logs and notifies; enforce mode sends SIGTERM, waits the
kill grace, then SIGKILLs.`,
		Args:         cobra.NoArgs,
		RunE:         runWatchdogReap,
		SilenceUsage: true,
	}
}

func runWatchdogReap(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	scheduled := os.Getenv(watchdog.ScheduledRunEnv) == "1"
	runner := watchdogRunner(false)
	mgr := watchdog.NewManager(runner, homeFor(cmd))
	wcfg, err := loadWatchdogSnapshot(mgr)
	if err != nil {
		return err
	}
	settings, err := watchdog.ResolveReaper(wcfg.Reaper)
	if err != nil {
		return err
	}
	out, err := runner.RunQuery(ctx, "ps", "-Ao", watchdog.PSArgs)
	if err != nil {
		return fmt.Errorf("sampling processes: %w", err)
	}
	procs, err := watchdog.ParsePS(out.Stdout)
	if err != nil {
		return err
	}
	prev, err := watchdog.LoadSamples(mgr.SamplesPath())
	if err != nil {
		return err
	}
	now := time.Now()
	candidates, next := watchdog.Evaluate(procs, prev, settings, now, os.ExpandEnv)
	if err := watchdog.SaveSamples(mgr.SamplesPath(), next); err != nil {
		return err
	}
	notifier := watchdog.NewNotifier(watchdog.ResolveNotify(wcfg.Notify), runner, runtime.GOOS)
	for _, c := range candidates {
		action, aerr := actOnCandidate(settings, c, now, mgr.LogPath(), notifier)
		if aerr != nil {
			return aerr
		}
		if !scheduled {
			p := printerFrom(cmd)
			p.Line("%s pid %d (%.0f%% CPU, %s): %s", action, c.Process.PID, c.Process.CPU, c.Reason, c.Process.Args)
		}
	}
	return nil
}

// actOnCandidate records the candidate and applies the mode's action. The
// log write comes first in both modes, so even a failed kill leaves a record.
func actOnCandidate(settings watchdog.ReaperSettings, c watchdog.Candidate, now time.Time, logPath string, notifier *watchdog.Notifier) (string, error) {
	event := watchdog.Event{
		Time:  now,
		Event: "candidate",
		PID:   c.Process.PID,
		CPU:   c.Process.CPU,
		Args:  c.Process.Args,
		Msg:   c.Reason,
	}
	if settings.Mode == watchdog.ModeDryRun {
		event.Level = "warn"
		event.Action = watchdog.ModeDryRun
		if err := watchdog.AppendEvent(logPath, event); err != nil {
			return "", err
		}
		msg := fmt.Sprintf("runaway process pid %d at %.0f%% CPU since %s (dry-run, not killed)", c.Process.PID, c.Process.CPU, c.OverSince.Format(time.RFC3339))
		if err := notifier.Notify(context.Background(), "warn", msg); err != nil {
			return "", err
		}
		return "[dry-run] would kill", nil
	}
	event.Level = "critical"
	if err := watchdog.AppendEvent(logPath, event); err != nil {
		return "", err
	}
	outcome, err := watchdog.Enforce(watchdog.SystemKiller{}, c.Process.PID, settings.KillGrace, time.Sleep)
	if err != nil {
		_ = watchdog.AppendEvent(logPath, watchdog.Event{Time: now, Level: "critical", Event: "error", PID: c.Process.PID, Msg: err.Error()})
		return "", err
	}
	_ = watchdog.AppendEvent(logPath, watchdog.Event{Time: now, Level: "critical", Event: "reap", PID: c.Process.PID, CPU: c.Process.CPU, Args: c.Process.Args, Action: outcome})
	msg := fmt.Sprintf("reaped runaway process pid %d at %.0f%% CPU (%s)", c.Process.PID, c.Process.CPU, outcome)
	if err := notifier.Notify(context.Background(), "critical", msg); err != nil {
		return "", err
	}
	return "killed (" + outcome + ")", nil
}

// loadWatchdogConfig resolves the watchdog section from the active profile
// (--profile/--config flags, else the saved profile), the way apply does.
func loadWatchdogConfig(cmd *cobra.Command) (config.WatchdogConfig, error) {
	profileName, _ := cmd.Flags().GetString("profile")
	configPath, _ := cmd.Flags().GetString("config")
	if profileName == "" && configPath == "" {
		state, err := config.LoadState()
		if err != nil {
			return config.WatchdogConfig{}, fmt.Errorf("loading state: %w", err)
		}
		profileName = state.Profile
	}
	cfg, err := config.Load(profileName, configPath, nil)
	if err != nil {
		return config.WatchdogConfig{}, fmt.Errorf("loading config: %w", err)
	}
	return cfg.Watchdog, nil
}

// loadWatchdogSnapshot reads the resolved config the setup wrote for the
// scheduled reaper, with an actionable error when setup never ran.
func loadWatchdogSnapshot(mgr *watchdog.Manager) (config.WatchdogConfig, error) {
	data, err := os.ReadFile(mgr.SnapshotPath())
	if err != nil {
		return config.WatchdogConfig{}, fmt.Errorf("no watchdog config snapshot at %s; run `dot watchdog setup` first", mgr.SnapshotPath())
	}
	var wcfg config.WatchdogConfig
	if err := yaml.Unmarshal(data, &wcfg); err != nil {
		return config.WatchdogConfig{}, fmt.Errorf("parsing watchdog snapshot %s: %w", mgr.SnapshotPath(), err)
	}
	return wcfg, nil
}

func watchdogRunner(dryRun bool) *exec.Runner {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return exec.NewRunner(dryRun, logger)
}
