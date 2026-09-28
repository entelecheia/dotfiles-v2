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
	list := "PID\tStatus\tLabel\n"
	for _, label := range loaded {
		list += "-\t0\t" + label + "\n"
	}
	writeStub(t, filepath.Join(bin, "launchctl"), "#!/bin/sh\n"+
		"if [ \"$1\" = list ]; then printf '"+strings.ReplaceAll(list, "\n", "\\n")+"'; exit 0; fi\n"+
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
	if !strings.Contains(preview[0].Detail, "3 jobs to boot out") {
		t.Fatalf("preview detail = %q", preview[0].Detail)
	}

	off := runPeerHooks(context.Background(), peerScheduleRunner(false), cfg, HookOnDeactivate, false)
	on := runPeerHooks(context.Background(), peerScheduleRunner(false), cfg, HookOnActivate, false)
	got := readRecord(t, record)
	for _, want := range []string{
		"launchctl bootout " + uid + "/com.maru.job.mail-digest.1",
		"launchctl bootout " + uid + "/com.maru.job.morning-brief.1",
		`osascript -e if application "Maru" is running then tell application "Maru" to quit`,
		"launchctl bootstrap " + uid + " " + filepath.Join(agents, "com.maru.job.fresh.plist"),
		"open -g -a Maru",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("record lacks %q:\n%s", want, got)
		}
	}
	for _, never := range []string{"com.other.agent", "com.maru.job.loaded.plist"} {
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
	if len(lines) != 2 || lines[0] != "launchctl bootout gui/"+uid+"/com.maru.job.mail-digest.1" || lines[1] != "launchctl bootout gui/"+uid+"/com.dotfiles.peer" {
		t.Fatalf("service actions = %v, want the hook first and the peer scheduler last", lines)
	}
}
