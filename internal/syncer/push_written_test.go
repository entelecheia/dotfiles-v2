package syncer

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestWrittenFiles(t *testing.T) {
	out := strings.Join([]string{
		"@@written .d..t...... ./",
		"@@written >f+++++++++ notes/new.md",
		"@@written >f.st...... notes/changed file.md",
		"         1.20K 100%    0.00kB/s    0:00:00 (xfr#1, to-chk=0/3)\r",
		"@@written cd+++++++++ notes/",
		"@@written *deleting   notes/old.md",
		"*deleting old.txt",
		"@@written >f+++++++ \\#355\\#225\\#234\\#352\\#270\\#200.txt", // openrsync escapes non-ASCII
		"@@written >f+++++++++ lit\\#134#123.md",                       // a literal \#123 in the name
		"Number of files: 3 (reg: 2, dir: 1)",
		"",
	}, "\n")
	got := writtenFiles(out)
	want := []string{"notes/new.md", "notes/changed file.md", "한글.txt", `lit\#123.md`}
	if len(got) != len(want) {
		t.Errorf("writtenFiles = %v, want %v", got, want)
	}
	for _, rel := range want {
		if !got[rel] {
			t.Errorf("writtenFiles missing %q (got %v)", rel, got)
		}
	}
}

// writeRsyncThatRemoves returns an rsync stand-in that runs the real rsync and
// then deletes victim: a live process removing a file the push just copied,
// before the baseline refresh runs (#224).
func writeRsyncThatRemoves(t *testing.T, victim string) string {
	t.Helper()
	real, err := osexec.LookPath("rsync")
	if err != nil {
		t.Skip("rsync not installed")
	}
	script := filepath.Join(t.TempDir(), "rsync")
	body := "#!/bin/sh\n\"" + real + "\" \"$@\"\nrc=$?\nrm -f \"" + victim + "\"\nexit $rc\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestPush_RecordsAWrittenFileWhoseLocalTwinVanished(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	f.writeLocal("notes/keep.md", "keep")
	f.writeLocal("renders/work-1/frame_0001.jpg", "frame")
	f.cfg.RsyncPath = writeRsyncThatRemoves(t, filepath.Join(f.local, "renders/work-1/frame_0001.jpg"))

	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("Push: %v", err)
	}
	baseline, err := LoadBaselineManifest(f.cfg.LocalPaths.BaselineFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := baseline["renders/work-1/frame_0001.jpg"]; !ok {
		t.Fatalf("baseline lacks the frame the push wrote: %v", baseline)
	}

	// A file that arrives in the mirror on its own is still mirror-origin.
	f.writeMirror("from-dropbox.md", "added in the cloud")
	f.cfg.RsyncPath = ""
	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Deletes, []string{"renders/work-1/frame_0001.jpg"}) {
		t.Errorf("Deletes = %v, want the vanished frame", plan.Deletes)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].RelPath != "from-dropbox.md" ||
		plan.Conflicts[0].Reason != "mirror-only file is not in baseline" {
		t.Errorf("Conflicts = %+v, want only from-dropbox.md as mirror-origin", plan.Conflicts)
	}

	if _, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeForce}); err != nil {
		t.Fatalf("PushCommand: %v", err)
	}
	if out, _ := osexec.Command("rsync", "--version").Output(); strings.Contains(string(out), "openrsync") {
		// openrsync deletes nothing when --backup is set; a pre-existing gap
		// every mirror delete shares, tracked apart from #224.
		t.Skip("openrsync ignores --delete-after with --backup")
	}
	if _, err := os.Stat(filepath.Join(f.mirror, "renders/work-1/frame_0001.jpg")); !os.IsNotExist(err) {
		t.Errorf("frame still in the mirror after the next push (err=%v)", err)
	}
}

func TestPushCommand_RecordsAFailedRsyncByExitAndStderr(t *testing.T) {
	f := newIntakeFixture(t)
	f.writeLocal("notes/new.md", "new")
	f.cfg.RsyncPath = filepath.Join(t.TempDir(), "rsync")
	script := "#!/bin/sh\necho 'rsync: connection unexpectedly closed' >&2\necho 'second line' >&2\nexit 12\n"
	if err := os.WriteFile(f.cfg.RsyncPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeForce}); err == nil {
		t.Fatal("PushCommand succeeded with a failing rsync")
	}
	st, err := LoadLocalState(f.cfg.LocalPaths)
	if err != nil {
		t.Fatal(err)
	}
	if want := "push failed: rsync exit 12: rsync: connection unexpectedly closed"; st.LastPushError != want {
		t.Errorf("LastPushError = %q, want %q", st.LastPushError, want)
	}
}

func TestPushCommand_RecordsARefusalUntilAPushCompletes(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	f.writeLocal("notes/keep.md", "keep")
	f.writeMirror("from-dropbox.md", "added in the cloud")
	run := func(mode RunMode) error {
		_, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: mode})
		return err
	}

	if err := run(ModeClean); err == nil || !strings.Contains(err.Error(), "push refused: 1 conflict") {
		t.Fatalf("clean push = %v, want a refusal", err)
	}
	st, err := LoadLocalState(f.cfg.LocalPaths)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.LastPushError, "push refused: 1 conflict") || st.LastPushErrorSince.IsZero() || st.LastPushAttempt.IsZero() {
		t.Fatalf("state after a refusal = %+v", st)
	}
	since := st.LastPushErrorSince
	time.Sleep(10 * time.Millisecond)
	_ = run(ModeClean)
	if st, _ = LoadLocalState(f.cfg.LocalPaths); !st.LastPushErrorSince.Equal(since) || !st.LastPushAttempt.After(since) {
		t.Errorf("a second refusal moved the streak start or kept the attempt: %+v", st)
	}

	if err := run(ModeForce); err != nil {
		t.Fatalf("force push: %v", err)
	}
	if st, _ = LoadLocalState(f.cfg.LocalPaths); st.LastPushError != "" || !st.LastPushErrorSince.IsZero() || st.LastPush.IsZero() {
		t.Errorf("state after a completed push = %+v", st)
	}
}

func TestStatus_PushStalled(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		st   Status
		want string
	}{
		{"quiet", Status{Interval: 300, SchedulerState: SchedulerRunning, LastPush: now.Add(-10 * time.Minute)}, ""},
		{"failing", Status{LastPushError: "push refused: 719 conflict(s)", LastPushErrorSince: now.Add(-49 * time.Hour)}, "pushes failing since"},
		{"stale", Status{Interval: 300, SchedulerState: SchedulerRunning, LastPush: now.Add(-16 * time.Minute)}, "no push completed for 16m0s"},
		{"stale but scheduler off", Status{Interval: 300, SchedulerState: SchedulerNotInstalled, LastPush: now.Add(-49 * time.Hour)}, ""},
		{"stale but paused", Status{Interval: 300, SchedulerState: SchedulerRunning, Paused: true, LastPush: now.Add(-49 * time.Hour)}, ""},
		{"stale while a run holds the lock", Status{Interval: 300, SchedulerState: SchedulerRunning, LockHeld: true, LastPush: now.Add(-49 * time.Hour)}, ""},
	} {
		got := tc.st.PushStalled(now)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: PushStalled = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// mirrorAt moves the fixture's mirror to root/<rel>, so a test can place it
// where a provider's folder would be.
func (f *intakeFixture) mirrorAt(rel string) {
	f.t.Helper()
	f.mirror = filepath.Join(f.root, rel)
	if err := os.MkdirAll(f.mirror, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.cfg.MirrorPath = f.mirror + "/"
	f.cfg.Target = Target{Kind: TargetLocal, Path: f.mirror + "/"}
}

func TestMirrorUnderDropbox(t *testing.T) {
	root := t.TempDir()
	for rel, want := range map[string]bool{
		"Library/CloudStorage/Dropbox/work":                 true,
		"Library/CloudStorage/Dropbox-Personal/work":        true,
		"Users/me/Dropbox/work":                             true,
		"home/me/Dropbox (Team)/work":                       true,
		"Library/CloudStorage/GoogleDrive-me/My Drive/work": false,
		"Library/CloudStorage/GoogleDrive-me/Dropbox/work":  false, // a folder named Dropbox inside Drive
		"gdrive-workspace/work":                             false,
	} {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := mirrorUnderDropbox(dir); got != want {
			t.Errorf("mirrorUnderDropbox(%s) = %v, want %v", rel, got, want)
		}
	}
	link := filepath.Join(root, "Dropbox-link")
	if err := os.Symlink(filepath.Join(root, "Library/CloudStorage/Dropbox"), link); err != nil {
		t.Fatal(err)
	}
	if !mirrorUnderDropbox(filepath.Join(link, "work")) {
		t.Error("a symlink into CloudStorage/Dropbox is not resolved")
	}
}

func TestPushCommand_MovesMirrorLeftoversOutOfTheMirror(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.mirrorAt("Library/CloudStorage/Dropbox/work")
	f.cfg.Propagation.Delete = true
	// The workspace renamed "notice./" to "notice/"; the mirror still has the
	// old copy from before the name filter.
	f.writeLocal("spoc/notice/a.pdf", "a")
	f.writeMirror("spoc/notice./a.pdf", "a")
	f.writeMirror("spoc/notice./.DS_Store", "finder")
	f.writeMirror("old./sub/c.md", "c")
	// A workspace name Dropbox cannot store stays excluded and its mirror
	// copy is not touched.
	f.writeLocal("keep./b.md", "b")
	f.writeMirror("keep./b.md", "b")

	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Leftovers, []string{"old./sub/c.md", "spoc/notice./a.pdf"}) || !slices.Equal(plan.Unsupported, []string{"keep./b.md"}) || !plan.MoveLeftovers {
		t.Fatalf("Leftovers = %v, Unsupported = %v, MoveLeftovers = %v", plan.Leftovers, plan.Unsupported, plan.MoveLeftovers)
	}

	var moved SyncEvent
	_, err = PushCommand(context.Background(), PushOptions{
		Config: f.cfg, Runner: f.runner, Mode: ModeClean,
		Progress: func(e SyncEvent) {
			if e.Kind == SyncEventLeftoversMoved {
				moved = e
			}
		},
	})
	if err != nil {
		t.Fatalf("PushCommand: %v", err)
	}
	for _, gone := range []string{"spoc/notice.", "old."} {
		if _, err := os.Stat(filepath.Join(f.mirror, gone)); !os.IsNotExist(err) {
			t.Errorf("old folder %s still in the mirror (err=%v)", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.mirror, "spoc/notice/a.pdf")); err != nil {
		t.Errorf("renamed copy missing from the mirror: %v", err)
	}
	if moved.Candidates != 2 || !strings.HasPrefix(moved.Path, filepath.Join(f.local, ".sync-conflicts")) {
		t.Fatalf("moved event = %+v", moved)
	}
	if body, err := os.ReadFile(filepath.Join(moved.Path, "spoc/notice./a.pdf")); err != nil || string(body) != "a" {
		t.Errorf("backup copy = %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(f.mirror, "keep./b.md")); err != nil {
		t.Errorf("a workspace unsupported name's mirror copy was touched: %v", err)
	}

	f.cfg.MaxDelete = 0
	f.writeMirror("old./c.md", "c")
	if _, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeClean}); err == nil ||
		!strings.Contains(err.Error(), "exceed max_delete") {
		t.Errorf("leftovers over max_delete: err = %v", err)
	}
}

// Google Drive stores names ending in a period, so on a non-Dropbox mirror a
// mirror-only "Acme Inc./" can be a collaborator's file: listed, never moved.
func TestPushCommand_LeavesUnsupportedNamesOnANonDropboxMirror(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.mirrorAt("Library/CloudStorage/GoogleDrive-me/My Drive/work")
	f.cfg.Propagation.Delete = true
	f.writeLocal("notes/keep.md", "keep")
	f.writeMirror("clients/Acme Inc./contract.pdf", "signed")

	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MoveLeftovers || !slices.Equal(plan.Leftovers, []string{"clients/Acme Inc./contract.pdf"}) {
		t.Fatalf("MoveLeftovers = %v, Leftovers = %v", plan.MoveLeftovers, plan.Leftovers)
	}
	if _, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeClean}); err != nil {
		t.Fatalf("PushCommand: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.mirror, "clients/Acme Inc./contract.pdf")); err != nil {
		t.Errorf("a cloud file with a period-ending folder left the mirror: %v", err)
	}
}
