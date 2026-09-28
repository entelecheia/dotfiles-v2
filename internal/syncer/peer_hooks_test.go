package syncer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// installHookServiceStubs plants launchctl, osascript and open stubs that
// append their argv to record; `launchctl list` answers with the given
// loaded labels.
func installHookServiceStubs(t *testing.T, record string, loaded ...string) {
	t.Helper()
	bin := t.TempDir()
	// The shape of `launchctl print gui/<uid>`: a services block among
	// other sections.
	domain := "gui/" + strconv.Itoa(os.Getuid()) + " = {\n\ttype = gui\n\tservices = {\n"
	for _, label := range loaded {
		domain += "\t\t       0      - \t" + label + "\n"
	}
	domain += "\t}\n\tdisabled services = {\n\t\t\"com.not.loaded\" => disabled\n\t}\n}\n"
	writeStub(t, filepath.Join(bin, "launchctl"), "#!/bin/sh\n"+
		"if [ \"$1\" = print ]; then printf '"+strings.ReplaceAll(strings.ReplaceAll(domain, "\n", "\\n"), "\t", "\\t")+"'; exit 0; fi\n"+
		"echo \"launchctl $*\" >> '"+record+"'\n")
	for _, name := range []string{"osascript", "open"} {
		writeStub(t, filepath.Join(bin, name), "#!/bin/sh\necho \""+name+" $*\" >> '"+record+"'\n")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func readRecord(t *testing.T, record string) string {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunPeerHooks_Actions(t *testing.T) {
	home := t.TempDir()
	record := filepath.Join(t.TempDir(), "record.log")
	installHookServiceStubs(t, record, "com.maru.job.mail-digest.1", "com.maru.job.morning-brief.1", "com.other.agent", "com.maru.job.loaded")
	agents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"com.maru.job.fresh.plist", "com.maru.job.loaded.plist", "com.other.agent.plist"} {
		if err := os.WriteFile(filepath.Join(agents, name), []byte("<plist/>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logFile := filepath.Join(t.TempDir(), "log", "peer.log")
	cfg := &Config{Home: "", LogFile: logFile, Hooks: PeerHooks{
		OnDeactivate: []string{"launchd-bootout com.maru.job.*", "app-quit Maru", "bogus x"},
		OnActivate:   []string{"launchd-bootstrap com.maru.job.*", "app-open Maru"},
	}}
	t.Setenv("HOME", home)
	uid := "gui/" + strconv.Itoa(os.Getuid())

	preview := runPeerHooks(context.Background(), peerScheduleRunner(false), cfg, HookOnDeactivate, true)
	if len(preview) != 3 || readRecord(t, record) != "" {
		t.Fatalf("preview ran something: %+v / %q", preview, readRecord(t, record))
	}
	if preview[2].Err == nil || !strings.Contains(preview[2].Err.Error(), "unknown hook action") {
		t.Fatalf("unknown action accepted: %+v", preview[2])
	}
	if runtime.GOOS != "darwin" {
		if !strings.Contains(preview[0].Detail, "needs macOS") {
			t.Fatalf("non-macOS detail = %q", preview[0].Detail)
		}
		return
	}
	if !strings.Contains(preview[0].Detail, "4 jobs to disable, 3 loaded jobs to boot out") {
		t.Fatalf("preview detail = %q", preview[0].Detail)
	}

	off := runPeerHooks(context.Background(), peerScheduleRunner(false), cfg, HookOnDeactivate, false)
	on := runPeerHooks(context.Background(), peerScheduleRunner(false), cfg, HookOnActivate, false)
	got := readRecord(t, record)
	for _, want := range []string{
		// Disabled first, the not-loaded plist included: a reboot must not
		// load them again.
		"launchctl disable " + uid + "/com.maru.job.fresh\n",
		"launchctl disable " + uid + "/com.maru.job.mail-digest.1\n",
		"launchctl bootout " + uid + "/com.maru.job.mail-digest.1",
		"launchctl bootout " + uid + "/com.maru.job.morning-brief.1",
		"with timeout of 20 seconds\ntell application \"Maru\" to quit",
		"launchctl enable " + uid + "/com.maru.job.fresh\n",
		"launchctl enable " + uid + "/com.maru.job.loaded\n",
		"launchctl bootstrap " + uid + " " + filepath.Join(agents, "com.maru.job.fresh.plist"),
		"open -g -a Maru",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("record lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "launchctl enable "+uid+"/com.maru.job.fresh") > strings.Index(got, "launchctl bootstrap") {
		t.Errorf("bootstrap before enable:\n%s", got)
	}
	for _, never := range []string{"com.other.agent", "com.maru.job.loaded.plist", "com.dotfiles.peer"} {
		if strings.Contains(got, never) {
			t.Errorf("record touched %q:\n%s", never, got)
		}
	}
	if off[0].Err != nil || on[0].Err != nil || off[2].Err == nil {
		t.Fatalf("results: off %+v on %+v", off, on)
	}
	logged := string(gitStateFileBytes(t, logFile))
	if !strings.Contains(logged, "hook on_deactivate launchd-bootout com.maru.job.*") || !strings.Contains(logged, "hook on_activate app-open Maru") || !strings.Contains(logged, "exit=1") {
		t.Fatalf("peer log lacks hook results:\n%s", logged)
	}
}

// #184 AC1: handover runs on_deactivate here and installs the new
// coordinator's scheduler, whose setup runs its on_activate there.
func TestPeerHandover_RunsRoleHooks(t *testing.T) {
	sb := newPeerHandoverSandbox(t, peerStatusFields{epoch: 1, dotVersion: "9.9.9 (fake)"}, 1)
	installHookServiceStubs(t, sb.record, "com.maru.job.mail-digest.1")
	sb.installFakeRemoteDot(t)
	sb.plantPeerPlist(t)
	sb.cfg.Hooks = PeerHooks{OnDeactivate: []string{"launchd-bootout com.maru.job.*"}}

	res, err := PeerHandover(context.Background(), PeerHandoverOptions{Config: sb.cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false)})
	if err != nil {
		t.Fatalf("PeerHandover: %v", err)
	}
	if len(res.Hooks) != 1 || res.Hooks[0].Phase != HookOnDeactivate || res.Hooks[0].Err != nil {
		t.Fatalf("hooks = %+v", res.Hooks)
	}
	lines := strings.Join(sb.recordLines(t), "\n")
	if !strings.Contains(lines, "dot peer setup") {
		t.Fatalf("the new coordinator's setup (its on_activate) did not run:\n%s", lines)
	}
	if runtime.GOOS == "darwin" && !strings.Contains(lines, "launchctl bootout gui/"+strconv.Itoa(os.Getuid())+"/com.maru.job.mail-digest.1") {
		t.Fatalf("on_deactivate did not boot out the job:\n%s", lines)
	}
}

// The on_activate half of AC1: installing the scheduler runs on_activate.
func TestPeerSchedule_InstallRunsOnActivate(t *testing.T) {
	cfg, _ := peerScheduleSandbox(t)
	cfg.Hooks = PeerHooks{OnActivate: []string{"app-open Maru"}}
	res, err := PeerSchedule(context.Background(), PeerScheduleOptions{
		Config: cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false), Interval: 7 * 60e9,
	})
	if err != nil {
		t.Fatalf("PeerSchedule: %v", err)
	}
	if len(res.Hooks) != 1 || res.Hooks[0].Phase != HookOnActivate {
		t.Fatalf("hooks = %+v", res.Hooks)
	}
}

// #184 AC2: a Mac that demotes itself at the fence runs on_deactivate before
// its own scheduler goes (bootout of com.dotfiles.peer stays last).
func TestPeerSyncDemotion_RunsOnDeactivateFirst(t *testing.T) {
	sb := newPeerHandoverSandbox(t, peerStatusFields{owner: "mac-b", epoch: 5, dotVersion: "9.9.9 (fake)"}, 1)
	installHookServiceStubs(t, sb.record, "com.maru.job.mail-digest.1")
	sb.plantPeerPlist(t)
	sb.cfg.Hooks = PeerHooks{OnDeactivate: []string{"launchd-bootout com.maru.job.*"}}

	res, err := PeerSync(context.Background(), PeerSyncOptions{Config: sb.cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false), SkipHome: true})
	if err != nil {
		t.Fatalf("PeerSync: %v", err)
	}
	if !res.Demoted || len(res.Hooks) != 1 || res.Hooks[0].Phase != HookOnDeactivate {
		t.Fatalf("result = %+v", res)
	}
	if runtime.GOOS != "darwin" {
		return
	}
	lines := sb.recordLines(t)
	uid := strconv.Itoa(os.Getuid())
	if len(lines) != 3 || lines[0] != "launchctl disable gui/"+uid+"/com.maru.job.mail-digest.1" || lines[1] != "launchctl bootout gui/"+uid+"/com.maru.job.mail-digest.1" || lines[2] != "launchctl bootout gui/"+uid+"/com.dotfiles.peer" {
		t.Fatalf("service actions = %v, want the hook first and the peer scheduler last", lines)
	}
}

func TestRunPeerHooks_BadGlobAndTargetUserHome(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record.log")
	installHookServiceStubs(t, record, "com.maru.job.a")
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{Hooks: PeerHooks{OnDeactivate: []string{"launchd-bootout com.maru.job.[", "app-quit Maru", "launchd-bootout *"}}}
	res := runPeerHooks(context.Background(), peerScheduleRunner(false), cfg, HookOnDeactivate, false)
	if res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "bad label glob") {
		t.Fatalf("malformed glob reported as %+v", res[0])
	}
	if res[2].Err == nil || !strings.Contains(res[2].Err.Error(), "literal prefix") || strings.Contains(readRecord(t, record), "launchctl") {
		t.Fatalf("a bare * glob ran: %+v", res[2])
	}
	if runtime.GOOS != "darwin" {
		return
	}
	// --home names another user: no action may run in the caller's session.
	_ = os.Remove(record)
	cfg.Home = t.TempDir()
	res = runPeerHooks(context.Background(), peerScheduleRunner(false), cfg, HookOnDeactivate, false)
	if !strings.Contains(res[1].Detail, "another user's session") || readRecord(t, record) != "" {
		t.Fatalf("target-user app hook ran: %+v / %q", res[1], readRecord(t, record))
	}
}

// A handover relays the new coordinator's on_activate outcomes, failures
// included, from its setup output.
func TestPeerHandover_RelaysRemoteHookResults(t *testing.T) {
	sb := newPeerHandoverSandbox(t, peerStatusFields{epoch: 1, dotVersion: "9.9.9 (fake)"}, 1)
	sb.installRecordingLaunchctl(t)
	sb.installFakeRemoteDot(t)
	sb.plantPeerPlist(t)
	dot := filepath.Join(sb.home, ".local", "bin", "dot")
	if err := os.Rename(dot, dot+".real"); err != nil {
		t.Fatal(err)
	}
	writeStub(t, dot, "#!/bin/sh\n"+
		"if [ \"$1 $2\" = 'peer setup' ]; then\n"+
		"  echo 'hook on_activate app-open Maru: opened Maru'\n"+
		"  echo 'hook on_activate launchd-bootstrap com.maru.job.*: bootstrapping x failed' >&2\n"+
		"fi\n"+
		"exec '"+dot+".real' \"$@\"\n")
	res, err := PeerHandover(context.Background(), PeerHandoverOptions{Config: sb.cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false)})
	if err != nil {
		t.Fatalf("PeerHandover: %v", err)
	}
	if !strings.Contains(strings.Join(res.Steps, "\n"), "peer hook on_activate app-open Maru: opened Maru") {
		t.Fatalf("steps lack the remote hook success: %v", res.Steps)
	}
	if len(res.RemoteHookFailures) != 1 || !strings.Contains(res.RemoteHookFailures[0], "bootstrapping x failed") {
		t.Fatalf("remote hook failures = %v", res.RemoteHookFailures)
	}
}

func TestLoadedLaunchdLabelsReadsTheGuiServices(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record.log")
	installHookServiceStubs(t, record, "com.maru.job.a", "com.dotfiles.peer")
	labels, err := loadedLaunchdLabels(context.Background(), peerScheduleRunner(false), "gui/"+strconv.Itoa(os.Getuid()))
	if err != nil || strings.Join(labels, ",") != "com.dotfiles.peer,com.maru.job.a" {
		t.Fatalf("labels = %v, %v", labels, err)
	}
	if runtime.GOOS != "darwin" {
		return
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	agents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"com.maru.job.a.plist", "com.dotfiles.peer.plist"} {
		if err := os.WriteFile(filepath.Join(agents, name), []byte("<plist/>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Neither action may touch dot's own scheduler, loaded or not.
	cfg := &Config{Hooks: PeerHooks{OnDeactivate: []string{"launchd-bootout com.*", "launchd-bootstrap ../x", "launchd-bootstrap com.*"}}}
	res := runPeerHooks(context.Background(), peerScheduleRunner(false), cfg, HookOnDeactivate, false)
	if strings.Contains(readRecord(t, record), "com.dotfiles.peer") || !strings.Contains(res[0].Detail, "1 loaded job booted out") {
		t.Fatalf("the peer scheduler was touched: %+v / %q", res, readRecord(t, record))
	}
	if res[1].Err == nil || !strings.Contains(res[1].Err.Error(), "slash") {
		t.Fatalf("a path escape was accepted: %+v", res[1])
	}
}

// The peer took over while this Mac was away: the handover's own sync
// demotes it, and the on_deactivate results reach the caller with the error.
func TestPeerHandover_DemotedDuringSyncReportsHooks(t *testing.T) {
	sb := newPeerHandoverSandbox(t, peerStatusFields{owner: "mac-b", epoch: 5, dotVersion: "9.9.9 (fake)"}, 1)
	installHookServiceStubs(t, sb.record, "com.maru.job.mail-digest.1")
	sb.plantPeerPlist(t)
	sb.cfg.Hooks = PeerHooks{OnDeactivate: []string{"launchd-bootout com.maru.job.*"}}
	res, err := PeerHandover(context.Background(), PeerHandoverOptions{Config: sb.cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false)})
	if err == nil || !strings.Contains(err.Error(), "demoted") {
		t.Fatalf("err = %v", err)
	}
	if res == nil || len(res.Hooks) != 1 || res.Hooks[0].Phase != HookOnDeactivate {
		t.Fatalf("hooks lost: %+v", res)
	}
}
