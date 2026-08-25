package syncer

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// Threading --home into Bootstrap moved the PROFILE onto the target home and
// left these three engine sites resolving the process home, so one command
// operated on two homes at once. Each row asserts the argv, the path or the
// rendered unit a wrong home would land in, not merely that a home was passed.

// homeFlagSandbox returns an invoking home (installed as the process HOME) and
// a separate target home, with a Config whose Home names the target.
func homeFlagSandbox(t *testing.T) (invoker, target string, cfg *Config) {
	t.Helper()
	invoker = t.TempDir()
	target = t.TempDir()
	t.Setenv("HOME", invoker)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(invoker, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(invoker, ".cache"))

	localPath := filepath.Join(target, "workspace", "work")
	cfg = &Config{
		Profile:    PeerProfile,
		Home:       target,
		LocalPath:  localPath,
		Target:     Target{Kind: TargetSSH, Host: "coordinator.example", Path: "/remote/workspace/work"},
		LogFile:    filepath.Join(localPath, ".dotfiles", "peer", "log", "peer.log"),
		LocalPaths: ResolveLocalPathsForProfile(localPath, PeerProfile),
	}
	return invoker, target, cfg
}

// TestPeerHomeSync_TransfersAgainstTheTargetHome is F1. peerHomeSync rsyncs the
// host-path list in both directions with the home as source and destination;
// resolving that home from the process meant `dot peer sync --home <other>`
// pulled the peer's .ssh and shell secrets over the INVOKING user's, which is
// how the first real run deleted this machine's known_hosts entry.
func TestPeerHomeSync_TransfersAgainstTheTargetHome(t *testing.T) {
	invoker, target, cfg := homeFlagSandbox(t)

	if err := os.MkdirAll(cfg.LocalPaths.StoreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PeerHomePathsFile(cfg.LocalPaths), []byte(".ssh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A stub rsync that records its argv: the transfer IS the argv, so this is
	// the only place a wrong home is observable.
	binDir := t.TempDir()
	argvLog := filepath.Join(binDir, "argv.log")
	writeStub(t, filepath.Join(binDir, "rsync"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+argvLog+"\n")
	t.Setenv("PATH", binDir)

	if err := peerHomeSync(context.Background(), peerScheduleRunner(false), cfg, nil, false, false, false); err != nil {
		t.Fatalf("peerHomeSync: %v", err)
	}

	logged, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("the stub rsync was never invoked: %v", err)
	}
	argv := string(logged)
	if strings.Count(strings.TrimSpace(argv), "\n") != 1 {
		t.Fatalf("want one pull and one push invocation, got:\n%s", argv)
	}
	// The endpoints, not the whole argv: --files-from names a path under the
	// target home too, so a substring search would pass vacuously.
	remote := cfg.Target.Host + ":"
	if !strings.Contains(argv, remote+" "+target+"/") {
		t.Errorf("the pull did not land in the target home:\n%s", argv)
	}
	if !strings.Contains(argv, target+"/ "+remote) {
		t.Errorf("the push did not come from the target home:\n%s", argv)
	}
	if strings.Contains(argv, remote+" "+invoker+"/") || strings.Contains(argv, invoker+"/ "+remote) {
		t.Errorf("peer home sync read or wrote the invoking user's home:\n%s", argv)
	}
}

// TestPeerHomeSync_WithoutOverrideUsesProcessHome is the non-vacuity row: an
// empty Config.Home must still resolve the process home, or the fix would be a
// hardcoded sandbox.
func TestPeerHomeSync_WithoutOverrideUsesProcessHome(t *testing.T) {
	invoker, _, cfg := homeFlagSandbox(t)
	cfg.Home = ""

	if err := os.MkdirAll(cfg.LocalPaths.StoreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PeerHomePathsFile(cfg.LocalPaths), []byte(".ssh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	argvLog := filepath.Join(binDir, "argv.log")
	writeStub(t, filepath.Join(binDir, "rsync"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+argvLog+"\n")
	t.Setenv("PATH", binDir)

	if err := peerHomeSync(context.Background(), peerScheduleRunner(false), cfg, nil, false, false, false); err != nil {
		t.Fatalf("peerHomeSync: %v", err)
	}
	logged, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("the stub rsync was never invoked: %v", err)
	}
	remote := cfg.Target.Host + ":"
	if !strings.Contains(string(logged), remote+" "+invoker+"/") {
		t.Errorf("without an override the transfer left the process home:\n%s", string(logged))
	}
}

// TestResolveScheduler_PathsFollowTheTargetHome is F2's first half: the unit
// file itself was written under the invoking user's LaunchAgents/systemd
// directory even when the profile it schedules lives in another home.
func TestResolveScheduler_PathsFollowTheTargetHome(t *testing.T) {
	invoker, target, cfg := homeFlagSandbox(t)

	_, paths, err := ResolveScheduler(cfg, peerScheduleRunner(true))
	if err != nil {
		t.Fatalf("ResolveScheduler: %v", err)
	}
	for _, unit := range []string{paths.LaunchdPlist, paths.SystemdService, paths.SystemdTimer, paths.LogFile} {
		if !strings.HasPrefix(unit, target+string(os.PathSeparator)) {
			t.Errorf("scheduler artifact outside the target home: %s", unit)
		}
		if strings.HasPrefix(unit, invoker+string(os.PathSeparator)) {
			t.Errorf("scheduler artifact under the invoking user's home: %s", unit)
		}
	}

	// Non-vacuity: no override, and the same call resolves the process home.
	cfg.Home = ""
	_, paths, err = ResolveScheduler(cfg, peerScheduleRunner(true))
	if err != nil {
		t.Fatalf("ResolveScheduler: %v", err)
	}
	if !strings.HasPrefix(paths.LaunchdPlist, invoker+string(os.PathSeparator)) {
		t.Errorf("without an override the scheduler artifact left the process home: %s", paths.LaunchdPlist)
	}
}

// TestRenderedSchedulerUnitCarriesHome is F2's second half, and the half that
// outlives the command: the unit runs `dot sync ...` on a timer. Without the
// override in the rendered ProgramArguments, every future scheduled execution
// operates on the invoking user's workspace.
func TestRenderedSchedulerUnitCarriesHome(t *testing.T) {
	_, target, cfg := homeFlagSandbox(t)
	cfg.Profile = DefaultProfile
	cfg.Interval = 600

	sched := NewScheduler(peerScheduleRunner(true), &Paths{}, cfg, template.NewEngine())
	for _, tmpl := range []string{"sync/com.dotfiles.sync.plist.tmpl", "sync/dotfiles-sync.service.tmpl"} {
		data := sched.templateDataFor(SchedulerKindPush)
		body, err := sched.Engine.Render(tmpl, data)
		if err != nil {
			t.Fatalf("rendering %s: %v", tmpl, err)
		}
		if !strings.Contains(string(body), "--home="+target) {
			t.Errorf("%s renders a unit that runs without the override:\n%s", tmpl, string(body))
		}

		// Non-vacuity: no override, no flag. A unit that always carried one
		// would pin the running user's home into every machine's scheduler.
		cfg.Home = ""
		body, err = sched.Engine.Render(tmpl, sched.templateDataFor(SchedulerKindPush))
		if err != nil {
			t.Fatalf("rendering %s: %v", tmpl, err)
		}
		if strings.Contains(string(body), "--home") {
			t.Errorf("%s renders --home with no override set:\n%s", tmpl, string(body))
		}
		cfg.Home = target
	}

	// The plist must recover one exact argv item rather than merely contain a
	// recognizable substring. Markup and whitespace are literal path data.
	specialHome := target + "/space & <tag> 'quote' \"double\" \\ % $ 유니코드"
	cfg.Home = specialHome
	data := sched.templateDataFor(SchedulerKindPush)
	body, err := sched.Engine.Render("sync/com.dotfiles.sync.plist.tmpl", data)
	if err != nil {
		t.Fatalf("rendering special plist: %v", err)
	}
	var plist plistProgramArguments
	if err := xml.Unmarshal(body, &plist); err != nil {
		t.Fatalf("special plist must parse: %v\n%s", err, body)
	}
	if got := matchingHomeArguments(plist.ProgramArguments); !reflect.DeepEqual(got, []string{"--home=" + specialHome}) {
		t.Fatalf("special plist home arguments = %#v, want %#v", got, []string{"--home=" + specialHome})
	}
}

// TestPeerSchedule_WritesUnderTheTargetHome covers the sibling site Codex did
// not name: PeerSchedule resolves its own plist path and renders its own unit
// body, so it carries both halves of F2 for the peer job.
func TestPeerSchedule_WritesUnderTheTargetHome(t *testing.T) {
	cfg, _ := peerScheduleSandbox(t)
	invoker, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	cfg.Home = target

	res, err := PeerSchedule(context.Background(), PeerScheduleOptions{
		Config:   cfg,
		Runner:   peerScheduleRunner(false),
		Probe:    peerScheduleRunner(false),
		Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("PeerSchedule: %v", err)
	}
	if !strings.HasPrefix(res.Plist, target+string(os.PathSeparator)) {
		t.Errorf("peer plist written outside the target home: %s", res.Plist)
	}
	if strings.HasPrefix(res.Plist, invoker+string(os.PathSeparator)) {
		t.Errorf("peer plist written under the invoking user's home: %s", res.Plist)
	}
	body, err := os.ReadFile(res.Plist)
	if err != nil {
		t.Fatalf("reading the written plist: %v", err)
	}
	if !strings.Contains(string(body), "--home="+target) {
		t.Errorf("the scheduled peer job runs without the override:\n%s", string(body))
	}
}
