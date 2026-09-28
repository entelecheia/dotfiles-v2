package syncer

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
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
//	launchd-bootout <label-glob>    boot out loaded gui jobs, e.g. com.maru.job.*
//	launchd-bootstrap <label-glob>  bootstrap ~/Library/LaunchAgents/<glob>.plist not yet loaded
//	app-quit <App>                  quit a running app
//	app-open <App>                  open an app in the background
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
	if runtime.GOOS != "darwin" {
		return "skipped: needs macOS", nil
	}
	if strings.HasPrefix(verb, "launchd-") && schedulerRequiresTargetUserServiceDomain(cfg) {
		return "skipped: --home targets another user's launchd domain", nil
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	switch verb {
	case "launchd-bootout":
		loaded, err := loadedLaunchdLabels(ctx, runner)
		if err != nil {
			return "", err
		}
		var matched []string
		for _, label := range loaded {
			if ok, _ := path.Match(arg, label); ok {
				matched = append(matched, label)
			}
		}
		if dryRun {
			return plural(len(matched), "job") + " to boot out" + listSuffix(matched), nil
		}
		var failed []string
		for _, label := range matched {
			if _, err := runner.Run(ctx, "launchctl", "bootout", domain+"/"+label); err != nil {
				failed = append(failed, label)
			}
		}
		if len(failed) > 0 {
			return "", fmt.Errorf("booting out %s failed", strings.Join(failed, ", "))
		}
		return plural(len(matched), "job") + " booted out" + listSuffix(matched), nil
	case "launchd-bootstrap":
		plists, err := filepath.Glob(filepath.Join(cfg.HomeDir(), "Library", "LaunchAgents", arg+".plist"))
		if err != nil {
			return "", err
		}
		loaded, err := loadedLaunchdLabels(ctx, runner)
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
			return plural(len(todo), "job") + " to bootstrap" + listSuffix(baseNames(todo)), nil
		}
		var failed []string
		for _, plist := range todo {
			if _, err := runner.Run(ctx, "launchctl", "bootstrap", domain, plist); err != nil {
				failed = append(failed, filepath.Base(plist))
			}
		}
		if len(failed) > 0 {
			return "", fmt.Errorf("bootstrapping %s failed", strings.Join(failed, ", "))
		}
		return plural(len(todo), "job") + " bootstrapped" + listSuffix(baseNames(todo)), nil
	case "app-quit":
		if dryRun {
			return "would quit " + arg + " if running", nil
		}
		script := `if application "` + arg + `" is running then tell application "` + arg + `" to quit`
		if _, err := runner.Run(ctx, "osascript", "-e", script); err != nil {
			return "", err
		}
		return "quit " + arg, nil
	default: // app-open
		if dryRun {
			return "would open " + arg, nil
		}
		if _, err := runner.Run(ctx, "open", "-g", "-a", arg); err != nil {
			return "", err
		}
		return "opened " + arg, nil
	}
}

// loadedLaunchdLabels lists the labels `launchctl list` reports in this
// user's domain (third column).
func loadedLaunchdLabels(ctx context.Context, runner *exec.Runner) ([]string, error) {
	res, err := runner.RunQuery(ctx, "launchctl", "list")
	if err != nil {
		return nil, fmt.Errorf("launchctl list: %w", err)
	}
	var labels []string
	for i, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if i == 0 || len(fields) < 3 {
			continue // header, blank
		}
		labels = append(labels, fields[2])
	}
	sort.Strings(labels)
	return labels, nil
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
