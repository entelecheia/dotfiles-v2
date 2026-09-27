package cli

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"os/user"
	"runtime"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
	"github.com/entelecheia/dotfiles-v2/internal/watchdog"
)

// dot watchdog monit — the P3 setup/status/uninstall steps for monit
// service-health supervision and the Screen Sharing heal helper.

// setupMonitStep ensures the monit binary, renders the monitrc, and loads the
// user LaunchAgent, then installs the root Screen Sharing heal helper when
// the resolved config heals screensharing. dotPath is the resolved `dot`
// binary the monitrc's notify execs invoke.
func setupMonitStep(p *Printer, mgr *watchdog.Manager, wcfg config.WatchdogConfig, dotPath string, yes bool) error {
	settings, err := watchdog.ResolveMonit(wcfg.Monit, runtime.NumCPU())
	if err != nil {
		return err
	}
	ctx := context.Background()
	if !mgr.Runner.CommandExists("monit") {
		install, err := ui.Confirm("monit is not installed; install it with `brew install monit`?", yes)
		if err != nil {
			return err
		}
		if !install {
			return fmt.Errorf("monit is required by watchdog.monit; install it (`brew install monit`) and rerun `dot watchdog setup`")
		}
		if err := mgr.Runner.RunInteractive(ctx, "brew", "install", "monit"); err != nil {
			return fmt.Errorf("installing monit: %w", err)
		}
	}
	monitPath, err := osexec.LookPath("monit")
	if err != nil {
		return fmt.Errorf("monit still not in PATH after the install step: %w", err)
	}
	ok, err := ui.Confirm("Install the monit LaunchAgent "+watchdog.MonitLabel+" (load/CPU alerts to dot watchdog notify)?", yes)
	if err != nil {
		return err
	}
	if !ok {
		p.Line("Skipped monit agent.")
		return nil
	}
	if err := mgr.InstallMonit(ctx, monitPath, watchdog.RenderMonitrc(dotPath, mgr.MonitLogPath(), settings)); err != nil {
		return err
	}
	p.Line("  ✓ monit agent installed (load > %g, cpu user > %g%%, system > %g%%, %d cycles)",
		settings.Load1Threshold, settings.CPUUserThreshold, settings.CPUSystemThreshold, settings.Cycles)
	if settings.ScreenSharing {
		if err := setupScreenSharingHealStep(p, mgr, yes); err != nil {
			return err
		}
	}
	return nil
}

// setupScreenSharingHealStep installs the root-owned heal helper and its
// passwordless sudoers grant, with the same prime-then-install discipline as
// the WARP daemon step.
func setupScreenSharingHealStep(p *Printer, mgr *watchdog.Manager, yes bool) error {
	ok, err := ui.Confirm(fmt.Sprintf("Install the Screen Sharing heal helper %s and its passwordless sudo grant (root)?", watchdog.ScreenSharingHealPath), yes)
	if err != nil {
		return err
	}
	if !ok {
		p.Line("Skipped Screen Sharing heal helper.")
		return nil
	}
	ctx := context.Background()
	if err := mgr.Runner.RunInteractive(ctx, "sudo", "-v"); err != nil {
		return fmt.Errorf("sudo is required for the root helper and sudoers grant: %w", err)
	}
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolving the installing user for the sudoers grant: %w", err)
	}
	if err := mgr.InstallScreenSharingHeal(ctx, current.Username); err != nil {
		return err
	}
	p.Line("  ✓ Screen Sharing heal helper installed (sudoers grant %s)", watchdog.ScreenSharingSudoersPath)
	return nil
}

// printMonitStatusRows adds the P3 rows to `dot watchdog status`: the monit
// binary, the agent's plist/loaded state, and monitrc drift against the
// resolved snapshot config. wcfg is nil when no snapshot exists.
func printMonitStatusRows(p *Printer, mgr *watchdog.Manager, wcfg *config.WatchdogConfig) {
	if runtime.GOOS != "darwin" {
		p.KV("Monit", "(macOS only)")
		return
	}
	if monitPath, err := osexec.LookPath("monit"); err == nil {
		p.KV("Monit", monitPath)
	} else {
		p.KV("Monit", "not installed")
	}
	st := mgr.ProbeMonit(context.Background())
	p.KV("Monit agent", filePresence(mgr.MonitPlistPath())+" loaded="+boolStr(st.Loaded))
	p.KV("Monitrc", monitrcStatus(mgr, wcfg))
}

// monitrcStatus compares the on-disk monitrc with what the snapshot would
// render today. A drifted control file means monit runs stale thresholds.
func monitrcStatus(mgr *watchdog.Manager, wcfg *config.WatchdogConfig) string {
	data, err := os.ReadFile(mgr.MonitrcPath())
	if os.IsNotExist(err) {
		return "missing"
	}
	if err != nil {
		return "unreadable: " + err.Error()
	}
	if wcfg == nil || !wcfg.Monit.Enabled {
		return "present"
	}
	settings, err := watchdog.ResolveMonit(wcfg.Monit, runtime.NumCPU())
	if err != nil {
		return "present (monit config invalid: " + err.Error() + ")"
	}
	dotPath, err := osexec.LookPath("dot")
	if err != nil {
		return "present (dot not in PATH; drift unknown)"
	}
	if string(data) == watchdog.RenderMonitrc(dotPath, mgr.MonitLogPath(), settings) {
		return "in sync"
	}
	return "DRIFTED — rerun `dot watchdog setup`"
}

// removeMonitStep unloads the agent and removes the plist and monitrc when
// either exists, mirroring the reaper removal (no extra prompt).
func removeMonitStep(p *Printer, mgr *watchdog.Manager) error {
	if !mgr.Runner.FileExists(mgr.MonitPlistPath()) && !mgr.Runner.FileExists(mgr.MonitrcPath()) {
		return nil
	}
	if err := mgr.UninstallMonit(context.Background()); err != nil {
		return err
	}
	p.Line("Removed monit agent and monitrc.")
	return nil
}

// removeScreenSharingHealStep offers the root helper/sudoers removal under
// the same interactive-only, default-No discipline as the power restore:
// --yes never auto-confirms a root-owned change.
func removeScreenSharingHealStep(p *Printer, mgr *watchdog.Manager, yes bool) error {
	if !mgr.Runner.FileExists(watchdog.ScreenSharingHealPath) && !mgr.Runner.FileExists(watchdog.ScreenSharingSudoersPath) {
		return nil
	}
	ok, err := ui.ConfirmBool("Remove the root Screen Sharing heal helper and its sudoers grant?", false, yes)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	ctx := context.Background()
	if err := mgr.Runner.RunInteractive(ctx, "sudo", "-v"); err != nil {
		return fmt.Errorf("sudo is required to remove the root helper and sudoers grant: %w", err)
	}
	if err := mgr.UninstallScreenSharingHeal(ctx); err != nil {
		return err
	}
	p.Line("Removed Screen Sharing heal helper and sudoers grant.")
	return nil
}
