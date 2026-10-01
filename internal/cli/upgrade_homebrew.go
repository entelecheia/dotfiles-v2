package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/resourceguard"
)

// homebrewDot is a dot binary that Homebrew installed at
// <prefix>/Cellar/<formula>/<version>/bin/dot.
type homebrewDot struct{ prefix, formula string }

// homebrewDotFor reports whether the resolved executable sits in a Homebrew
// Cellar. Homebrew always links prefix/bin/dot into the Cellar, so the
// resolved path carries /Cellar/ on every prefix layout (/opt/homebrew,
// /usr/local, linuxbrew) (#233).
func homebrewDotFor(execPath string) (homebrewDot, bool) {
	p := filepath.ToSlash(execPath)
	i := strings.Index(p, "/Cellar/")
	if i < 0 {
		return homebrewDot{}, false
	}
	formula, _, _ := strings.Cut(p[i+len("/Cellar/"):], "/")
	return homebrewDot{prefix: filepath.FromSlash(p[:i]), formula: formula}, formula != ""
}

func (h homebrewDot) brew() string { return filepath.Join(h.prefix, "bin", "brew") }

// upgradeRun runs a command for the Homebrew update path and returns its
// stdout; show also streams the output to the terminal. Tests replace it and
// upgradeAcquire to script brew and launchctl.
var upgradeRun = func(ctx context.Context, show bool, name string, args ...string) (string, error) {
	r := exec.NewRunner(false, slog.New(slog.DiscardHandler))
	run := r.RunQuery
	if show {
		run = r.RunTee
	}
	res, err := run(ctx, name, args...)
	if res == nil {
		return "", err
	}
	return res.Stdout, err
}

var upgradeAcquire = resourceguard.Acquire

type brewFormula struct {
	FullName string `json:"full_name"`
	Pinned   bool   `json:"pinned"`
	Versions struct {
		Stable string `json:"stable"`
	} `json:"versions"`
}

// upgradeHomebrew upgrades a Homebrew-installed dot through the brew that
// owns it, then reloads the LaunchAgents that run it (#235). dot never writes
// into the Cellar itself.
func upgradeHomebrew(ctx context.Context, p *Printer, h homebrewDot, current, latest string, dryRun bool) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("this dot binary belongs to Homebrew, which does not run as root; run dot update as the user who owns %s", h.prefix)
	}
	if !dryRun {
		release, err := upgradeAcquire(ctx, resourceguard.Options{Purpose: "dot update", ScopeKey: "tooling"})
		if err != nil {
			return fmt.Errorf("the Homebrew upgrade needs the maintenance slot: %w; retry later", err)
		}
		defer release()
	}
	brew := func(args ...string) error {
		line := "brew " + strings.Join(args, " ")
		if dryRun {
			p.Line("[dry-run] would run: %s", line)
			return nil
		}
		p.Line("\nRunning %s...", line)
		if _, err := upgradeRun(ctx, true, h.brew(), args...); err != nil {
			return fmt.Errorf("%s: %w", line, err)
		}
		return nil
	}

	info, err := brewFormulaInfo(ctx, h)
	if err != nil {
		return err
	}
	if info.Pinned {
		return fmt.Errorf("the %s formula is pinned; run `brew unpin %s` to let dot update upgrade it", info.FullName, h.formula)
	}
	if compareSemver(info.Versions.Stable, latest) < 0 {
		// Homebrew's auto-update skips a tap fetched within
		// HOMEBREW_AUTO_UPDATE_SECS, so a fresh release is not visible yet.
		if err := brew("update"); err != nil {
			return err
		}
		if !dryRun {
			if info, err = brewFormulaInfo(ctx, h); err != nil {
				return err
			}
			if compareSemver(info.Versions.Stable, latest) < 0 {
				return fmt.Errorf("the tap offers %s %s, not %s yet; retry once the release reaches it", info.FullName, info.Versions.Stable, latest)
			}
		}
	}
	if err := brew("upgrade", info.FullName); err != nil {
		return err
	}
	if !dryRun {
		dot := filepath.Join(h.prefix, "bin", "dot")
		out, err := upgradeRun(ctx, false, dot, "--version")
		if err != nil {
			return fmt.Errorf("checking %s after brew upgrade: %w", dot, err)
		}
		if !strings.Contains(out, "dot version "+latest) {
			return fmt.Errorf("brew upgrade returned, but %s reports %q, not %s", dot, strings.TrimSpace(out), latest)
		}
		p.Line("Upgraded: %s → %s (Homebrew)", current, latest)
	}
	return reloadDotLaunchAgents(ctx, p, h, dryRun)
}

func brewFormulaInfo(ctx context.Context, h homebrewDot) (brewFormula, error) {
	out, err := upgradeRun(ctx, false, h.brew(), "info", "--json=v2", "--formula", h.formula)
	if err != nil {
		return brewFormula{}, fmt.Errorf("reading Homebrew metadata for %s: %w", h.formula, err)
	}
	var info struct {
		Formulae []brewFormula `json:"formulae"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil || len(info.Formulae) != 1 || info.Formulae[0].FullName == "" {
		return brewFormula{}, fmt.Errorf("parsing Homebrew metadata for %s: %v", h.formula, err)
	}
	return info.Formulae[0], nil
}

// reloadDotLaunchAgents boots out and bootstraps every loaded dot LaunchAgent
// whose program resolves into this formula's Cellar. After brew replaces the
// binary, launchd fails the job's next spawn (`spawn failed`, `needs LWCR
// update`) until it is loaded again (#233, #235). A job that is not loaded,
// such as a paused sync, stays unloaded, and the sync pause gate is not
// touched. bootout stops a run in progress with SIGTERM, which dot handles as
// an interrupt; RunAtLoad jobs then run once at once.
func reloadDotLaunchAgents(ctx context.Context, p *Printer, h homebrewDot, dryRun bool) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding LaunchAgents: %w", err)
	}
	plists, err := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "com.dotfiles.*.plist"))
	if err != nil {
		return err
	}
	cellar := filepath.Join(h.prefix, "Cellar", h.formula) + string(filepath.Separator)
	domain := guiTarget()
	var failed []string
	for _, plist := range plists {
		label := strings.TrimSuffix(filepath.Base(plist), ".plist")
		out, err := upgradeRun(ctx, false, "launchctl", "print", domain+"/"+label)
		if err != nil {
			continue // not loaded
		}
		resolved, err := filepath.EvalSymlinks(launchdProgram(out))
		if err != nil || !strings.HasPrefix(resolved, cellar) {
			continue
		}
		if dryRun {
			p.Line("[dry-run] would reload %s", label)
			continue
		}
		if err := reloadLaunchAgent(ctx, domain, label, plist); err != nil {
			p.Warn("reloading %s: %v; reload it with `launchctl bootout %s/%s; launchctl bootstrap %s %s`", label, err, domain, label, domain, plist)
			failed = append(failed, label)
			continue
		}
		p.Line("Reloaded %s", label)
	}
	if len(failed) > 0 {
		return fmt.Errorf("upgraded, but could not reload %s", strings.Join(failed, ", "))
	}
	return nil
}

// launchdProgram reads the job's top-level `program = ` line from
// `launchctl print`; nested blocks are indented further.
func launchdProgram(dump string) string {
	for _, line := range strings.Split(dump, "\n") {
		if v, ok := strings.CutPrefix(line, "\tprogram = "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// upgradePoll paces the wait for a booted-out job to leave the domain.
var upgradePoll = 200 * time.Millisecond

func reloadLaunchAgent(ctx context.Context, domain, label, plist string) error {
	target := domain + "/" + label
	// bootout can report an error while the job is still exiting; whether
	// the job left the domain is what decides.
	_, _ = upgradeRun(ctx, false, "launchctl", "bootout", target)
	gone := false
	for range 100 { // the job's exit timeout is at most 20s by default
		if _, err := upgradeRun(ctx, false, "launchctl", "print", target); err != nil {
			gone = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(upgradePoll):
		}
	}
	if !gone {
		return fmt.Errorf("still loaded after bootout")
	}
	var err error
	for range 3 {
		if _, err = upgradeRun(ctx, false, "launchctl", "bootstrap", domain, plist); err == nil {
			return nil
		}
		if _, printErr := upgradeRun(ctx, false, "launchctl", "print", target); printErr == nil {
			return nil // loaded despite the reported error
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(upgradePoll):
		}
	}
	return err
}
