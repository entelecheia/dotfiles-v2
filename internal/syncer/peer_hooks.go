package syncer

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// PeerHooks are per-machine actions around a coordinator role change (#184).
// The inactive Mac must run no scheduled jobs that write the workspace (Maru
// jobs write .maru, inbox state and digests, and collide on every sync), yet
// handover, takeover and demotion only ever moved the dot peer scheduler.
//
// on_activate runs when this machine installs its peer scheduler (dot peer
// setup, the step handover runs on the new coordinator and takeover names
// next); on_deactivate runs when it gives the role up (dot peer setup --off,
// handover on the old coordinator, demotion at the fence). Actions:
//
//	launchd-bootout <label-glob>    disable and boot out gui jobs, e.g. com.maru.job.*
//	launchd-bootstrap <label-glob>  enable and bootstrap ~/Library/LaunchAgents/<glob>.plist not yet loaded
//	app-quit <App>                  quit a running app
//	app-open <App>                  open an app in the background
//
// A bootout alone lasts until the next login, when launchd loads every
// plist in ~/Library/LaunchAgents again; the disable (launchd's override
// database) keeps the jobs off across a reboot until on_activate enables
// them. com.dotfiles.peer is never matched: it is dot's own scheduler.
//
// Failures are reported and logged; they never abort a sync or a switch.
type PeerHooks struct {
	OnActivate   []string `yaml:"on_activate,omitempty"`
	OnDeactivate []string `yaml:"on_deactivate,omitempty"`
}

// Hook phases.
const (
	HookOnActivate   = "on_activate"
	HookOnDeactivate = "on_deactivate"
)

// HookResult is one hook action's outcome. Detail says what it did (or would
// do, under DryRun).
type HookResult struct {
	Phase  string
	Action string
	DryRun bool
	Detail string
	Err    error
}

// runPeerHooks runs the phase's actions in order and appends each outcome to
// the peer log. A preview lists what each action would do and changes nothing.
func runPeerHooks(ctx context.Context, runner *exec.Runner, cfg *Config, phase string, dryRun bool) []HookResult {
	actions := cfg.Hooks.OnActivate
	if phase == HookOnDeactivate {
		actions = cfg.Hooks.OnDeactivate
	}
	var results []HookResult
	for _, action := range actions {
		res := HookResult{Phase: phase, Action: action, DryRun: dryRun}
		res.Detail, res.Err = runPeerHook(ctx, runner, cfg, action, dryRun)
		results = append(results, res)
		if !dryRun && cfg.LogFile != "" {
			exit := 0
			if res.Err != nil {
				exit = 1
			}
			if err := ensureLogDir(cfg.LogFile); err == nil {
				AppendLog(cfg.LogFile, "hook "+phase+" "+action+" ("+hookOutcome(res)+")", exit)
			}
		}
	}
	return results
}

func hookOutcome(res HookResult) string {
	if res.Err != nil {
		return res.Err.Error()
	}
	return res.Detail
}

func runPeerHook(ctx context.Context, runner *exec.Runner, cfg *Config, action string, dryRun bool) (string, error) {
	verb, arg, _ := strings.Cut(strings.TrimSpace(action), " ")
	arg = strings.TrimSpace(arg)
	switch verb {
	case "launchd-bootout", "launchd-bootstrap", "app-quit", "app-open":
	default:
		return "", fmt.Errorf("unknown hook action %q (want launchd-bootout, launchd-bootstrap, app-quit or app-open)", verb)
	}
	if arg == "" || strings.ContainsAny(arg, "\"\\\n") {
		return "", fmt.Errorf("hook action %q needs one argument without quotes or backslashes", action)
	}
	if strings.HasPrefix(verb, "launchd-") {
		// A malformed glob would otherwise match nothing and report success
		// while the jobs keep running; a label never holds a slash, and the
		// bootstrap plists come from ~/Library/LaunchAgents only.
		if _, err := path.Match(arg, ""); err != nil {
			return "", fmt.Errorf("bad label glob %q: %w", arg, err)
		}
		if strings.Contains(arg, "/") {
			return "", fmt.Errorf("label glob %q holds a slash", arg)
		}
		if strings.IndexAny(arg, "*?[") == 0 {
			// A bare * would match every gui agent, com.apple.* included.
			return "", fmt.Errorf("label glob %q must start with a literal prefix, e.g. com.maru.job.*", arg)
		}
	}
	if runtime.GOOS != "darwin" {
		return "skipped: needs macOS", nil
	}
	if schedulerRequiresTargetUserServiceDomain(cfg) {
		// launchctl and the app actions would act in the caller's session,
		// not the --home user's.
		return "skipped: --home targets another user's session", nil
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	switch verb {
	case "launchd-bootout":
		loaded, err := loadedLaunchdLabels(ctx, runner, domain)
		if err != nil {
			return "", err
		}
		// Disable what is loaded and what the next login would load.
		plists, err := agentPlists(cfg, arg)
		if err != nil {
			return "", err
		}
		var matched, disable []string
		for _, label := range loaded {
			// The peer scheduler is dot's own; booting it out from inside a
			// demotion would end the run before its plist is removed.
			if ok, _ := path.Match(arg, label); ok && label != peerSchedulerLabel {
				matched = append(matched, label)
			}
		}
		disable = append(disable, matched...)
		for _, label := range baseNames(plists) {
			if !slices.Contains(disable, label) {
				disable = append(disable, label)
			}
		}
		sort.Strings(disable)
		if dryRun {
			return plural(len(disable), "job") + " to disable, " + plural(len(matched), "loaded job") + " to boot out" + listSuffix(disable), nil
		}
		var failed []string
		for _, label := range disable {
			if _, err := runner.Run(ctx, "launchctl", "disable", domain+"/"+label); err != nil {
				failed = append(failed, label+" ("+firstLine(err.Error())+")")
			}
		}
		for _, label := range matched {
			if _, err := runner.Run(ctx, "launchctl", "bootout", domain+"/"+label); err != nil {
				failed = append(failed, label+" ("+firstLine(err.Error())+")")
			}
		}
		if len(failed) > 0 {
			return "", fmt.Errorf("disabling or booting out %s failed", strings.Join(failed, ", "))
		}
		return plural(len(disable), "job") + " disabled, " + plural(len(matched), "loaded job") + " booted out" + listSuffix(disable), nil
	case "launchd-bootstrap":
		plists, err := agentPlists(cfg, arg)
		if err != nil {
			return "", err
		}
		loaded, err := loadedLaunchdLabels(ctx, runner, domain)
		if err != nil {
			return "", err
		}
		isLoaded := map[string]bool{}
		for _, label := range loaded {
			isLoaded[label] = true
		}
		var todo []string
		for _, plist := range plists {
			if label := strings.TrimSuffix(filepath.Base(plist), ".plist"); !isLoaded[label] {
				todo = append(todo, plist)
			}
		}
		if dryRun {
			return plural(len(plists), "job") + " to enable, " + plural(len(todo), "job") + " to bootstrap" + listSuffix(baseNames(todo)), nil
		}
		var failed []string
		// A disabled job refuses to bootstrap; enable every match first so a
		// loaded one also survives the next login.
		for _, label := range baseNames(plists) {
			if _, err := runner.Run(ctx, "launchctl", "enable", domain+"/"+label); err != nil {
				failed = append(failed, label+" ("+firstLine(err.Error())+")")
			}
		}
		for _, plist := range todo {
			if _, err := runner.Run(ctx, "launchctl", "bootstrap", domain, plist); err != nil {
				failed = append(failed, filepath.Base(plist)+" ("+firstLine(err.Error())+")")
			}
		}
		if len(failed) > 0 {
			return "", fmt.Errorf("enabling or bootstrapping %s failed", strings.Join(failed, ", "))
		}
		return plural(len(todo), "job") + " bootstrapped" + listSuffix(baseNames(todo)), nil
	case "app-quit":
		if dryRun {
			return "would quit " + arg + " if running", nil
		}
		// An Apple Event: dot needs Automation consent for the app once
		// (README). The timeout bounds an app that never answers.
		script := `if application "` + arg + `" is running then
with timeout of 20 seconds
tell application "` + arg + `" to quit
end timeout
end if`
		if _, err := runner.Run(ctx, "osascript", "-e", script); err != nil {
			return "", fmt.Errorf("%s", firstLine(err.Error()))
		}
		return "quit " + arg, nil
	default: // app-open
		if dryRun {
			return "would open " + arg, nil
		}
		if _, err := runner.Run(ctx, "open", "-g", "-a", arg); err != nil {
			return "", fmt.Errorf("%s", firstLine(err.Error()))
		}
		return "opened " + arg, nil
	}
}

// loadedLaunchdLabels lists the services loaded in the gui domain. `launchctl
// list` answers for the caller's own domain, which over ssh is not the gui
// session the Maru agents run in; `print gui/<uid>` names it explicitly.
func loadedLaunchdLabels(ctx context.Context, runner *exec.Runner, domain string) ([]string, error) {
	res, err := runner.RunQuery(ctx, "launchctl", "print", domain)
	if err != nil {
		return nil, fmt.Errorf("launchctl print %s: %w", domain, err)
	}
	var labels []string
	in := false
	for _, line := range strings.Split(res.Stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !in && trimmed == "services = {":
			in = true
		case in && trimmed == "}":
			in = false
		case in:
			// "<pid> <last exit> <label>"
			if fields := strings.Fields(trimmed); len(fields) >= 3 {
				labels = append(labels, fields[len(fields)-1])
			}
		}
	}
	sort.Strings(labels)
	return labels, nil
}

// agentPlists lists ~/Library/LaunchAgents/<glob>.plist, dot's own
// scheduler excluded.
func agentPlists(cfg *Config, glob string) ([]string, error) {
	plists, err := filepath.Glob(filepath.Join(cfg.HomeDir(), "Library", "LaunchAgents", glob+".plist"))
	return slices.DeleteFunc(plists, func(p string) bool {
		return filepath.Base(p) == peerSchedulerLabel+".plist"
	}), err
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func listSuffix(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return ": " + strings.Join(items, ", ")
}

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = strings.TrimSuffix(filepath.Base(p), ".plist")
	}
	return out
}
