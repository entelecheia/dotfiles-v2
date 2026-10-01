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

// writeRsyncThen returns an rsync stand-in that runs the real rsync, then the
// shell command after (a live process acting on the tree before the baseline
// refresh runs, #224), and exits with rsync's status.
func writeRsyncThen(t *testing.T, after string) string {
	t.Helper()
	real, err := osexec.LookPath("rsync")
	if err != nil {
		t.Skip("rsync not installed")
	}
	script := filepath.Join(t.TempDir(), "rsync")
	body := "#!/bin/sh\n\"" + real + "\" \"$@\"\nrc=$?\n" + after + "\nexit $rc\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// isOpenrsync reports whether the rsync on PATH is Apple's openrsync, which
// deletes nothing when --backup is set.
func isOpenrsync() bool {
	out, _ := osexec.Command("rsync", "--version").Output()
	return strings.Contains(string(out), "openrsync")
}

func TestPush_RecordsAWrittenFileWhoseLocalTwinVanished(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	f.writeLocal("notes/keep.md", "keep")
	f.writeLocal("renders/work-1/frame_0001.jpg", "frame")
	f.cfg.RsyncPath = writeRsyncThen(t, `rm -f "`+filepath.Join(f.local, "renders/work-1/frame_0001.jpg")+`"`)

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

	_, err = PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeForce})
	if isOpenrsync() {
		// openrsync deletes nothing when --backup is set; the run says so.
		if err == nil || !strings.Contains(err.Error(), "planned deletion(s) left in the mirror") {
			t.Fatalf("PushCommand under openrsync = %v, want the unapplied deletion", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("PushCommand: %v", err)
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

func TestPushCommand_MovesMirrorLeftoversOutOfTheMirror(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	// The workspace renamed "notice./" to "notice/"; the mirror still has the
	// old copy, which the baseline recorded while both sides had it.
	f.writeLocal("spoc/notice/a.pdf", "a")
	f.seedBaseline("spoc/notice./a.pdf", "a", f.writeMirror("spoc/notice./a.pdf", "a"))
	f.writeMirror("spoc/notice./.DS_Store", "finder")
	f.seedBaseline("old./sub/c.md", "c", f.writeMirror("old./sub/c.md", "c"))
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
	f.seedBaseline("old./c.md", "c", f.writeMirror("old./c.md", "c"))
	if _, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeClean}); err == nil ||
		!strings.Contains(err.Error(), "exceed max_delete") {
		t.Errorf("leftovers over max_delete: err = %v", err)
	}
}

// A mirror-only unsupported name without baseline proof may be a cloud file
// (Google Drive stores "Acme Inc.", Dropbox likely does too): listed, never
// moved. So is a proven name whose copy changed, or an online-only stub.
func TestPushCommand_ListsUnprovenMirrorOnlyUnsupportedNames(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	f.writeLocal("notes/keep.md", "keep")
	f.writeMirror("clients/Acme Inc./contract.pdf", "signed")
	f.seedBaseline("edited./x.pdf", "old", f.writeMirror("edited./x.pdf", "edited in the cloud"))

	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"clients/Acme Inc./contract.pdf", "edited./x.pdf"}
	if len(plan.Leftovers) != 0 || !slices.Equal(plan.MirrorUnsupported, want) {
		t.Fatalf("Leftovers = %v, MirrorUnsupported = %v", plan.Leftovers, plan.MirrorUnsupported)
	}
	if _, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeClean}); err != nil {
		t.Fatalf("PushCommand: %v", err)
	}
	for _, rel := range want {
		if _, err := os.Stat(filepath.Join(f.mirror, rel)); err != nil {
			t.Errorf("%s left the mirror: %v", rel, err)
		}
	}

	stub := filepath.Join(f.mirror, "stub./y.pdf")
	makePlaceholder(t, stub)
	f.seedBaseline("stub./y.pdf", "", time.Time{})
	if plan, err = PlanPush(f.cfg); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plan.Leftovers, "stub./y.pdf") || !slices.Contains(plan.MirrorUnsupported, "stub./y.pdf") {
		t.Errorf("an online-only stub was classed as a leftover: %v / %v", plan.Leftovers, plan.MirrorUnsupported)
	}
}

// A file an earlier push wrote is unchanged in this run, so rsync does not
// list it; when its local copy vanishes during this run, the baseline keeps
// the proof and the next push deletes it instead of refusing it (#224).
func TestPush_CarriesAProvenEntryWhoseLocalCopyVanishedDuringThePush(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	f.writeLocal("notes/keep.md", "keep")
	f.writeLocal("renders/work-1/frame_0001.jpg", "one")
	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("push 1: %v", err)
	}
	f.writeLocal("renders/work-1/frame_0002.jpg", "two")
	f.cfg.RsyncPath = writeRsyncThen(t, `rm -rf "`+filepath.Join(f.local, "renders/work-1")+`"`)
	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("push 2: %v", err)
	}
	f.cfg.RsyncPath = ""
	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Deletes, []string{"renders/work-1/frame_0001.jpg", "renders/work-1/frame_0002.jpg"}) || len(plan.Conflicts) != 0 {
		t.Errorf("Deletes = %v, Conflicts = %+v; want both frames deleted, no conflict", plan.Deletes, plan.Conflicts)
	}

	// With delete propagation off the entry is not carried: a pull must not
	// restore a file the workspace deleted long ago.
	f.cfg.Propagation.Delete = false
	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("push 3: %v", err)
	}
	baseline, _ := LoadBaselineManifest(f.cfg.LocalPaths.BaselineFile)
	if _, ok := baseline["renders/work-1/frame_0001.jpg"]; ok {
		t.Error("a vanished file's entry was carried with delete propagation off")
	}
}

func TestPushCommand_RecordsARsyncThatDidNotStart(t *testing.T) {
	f := newIntakeFixture(t)
	f.writeLocal("notes/new.md", "new")
	f.cfg.RsyncPath = filepath.Join(t.TempDir(), "no-such-rsync")
	if _, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeForce}); err == nil {
		t.Fatal("PushCommand succeeded without an rsync")
	}
	st, _ := LoadLocalState(f.cfg.LocalPaths)
	if !strings.HasPrefix(st.LastPushError, "push failed: ") || !strings.Contains(st.LastPushError, "no such file or directory") {
		t.Errorf("LastPushError = %q, want the start failure", st.LastPushError)
	}
}

// rsync stops at --max-delete with exit 25 only after every transfer
// (--delete-after), so the files that run wrote are still recorded (#224).
func TestPushCommand_RecordsWrittenFilesWhenRsyncStopsAtMaxDelete(t *testing.T) {
	requireRsync(t)
	if isOpenrsync() {
		t.Skip("openrsync deletes nothing with --backup, so --max-delete never stops it")
	}
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	f.writeLocal("notes/a.md", "a")
	f.writeLocal("notes/b.md", "b")
	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("push 1: %v", err)
	}
	before, _ := LoadLocalState(f.cfg.LocalPaths)
	for _, rel := range []string{"notes/a.md", "notes/b.md"} {
		if err := os.Remove(filepath.Join(f.local, rel)); err != nil {
			t.Fatal(err)
		}
	}
	frame := "renders/work-1/frame_0001.jpg"
	f.writeLocal(frame, "frame")
	f.cfg.MaxDelete = 1
	f.cfg.RsyncPath = writeRsyncThen(t, `rm -f "`+filepath.Join(f.local, frame)+`"`)
	if _, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeClean}); err == nil {
		t.Fatal("a push past max_delete succeeded")
	}
	st, _ := LoadLocalState(f.cfg.LocalPaths)
	if !strings.HasPrefix(st.LastPushError, "push failed: rsync exit 25") || !st.LastPush.Equal(before.LastPush) {
		t.Errorf("state after exit 25 = %+v; want the error recorded and last_push unchanged", st)
	}

	f.cfg.RsyncPath = ""
	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 0 || len(plan.Deletes) != 2 || !slices.Contains(plan.Deletes, frame) {
		t.Errorf("Deletes = %v, Conflicts = %+v; want the frame and the undeleted note as deletes", plan.Deletes, plan.Conflicts)
	}
}

// rsync skips every deletion after an I/O error (exit 23) and openrsync
// deletes nothing with --backup. A push that leaves a planned deletion in the
// mirror is recorded as failed, and the next working push deletes it (#224).
func TestPushCommand_FailsWhenAPlannedDeletionStaysInTheMirror(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	f.writeLocal("notes/keep.md", "keep")
	f.writeLocal("notes/gone.md", "gone")
	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("push 1: %v", err)
	}
	if err := os.Remove(filepath.Join(f.local, "notes/gone.md")); err != nil {
		t.Fatal(err)
	}
	real, _ := osexec.LookPath("rsync")
	f.cfg.RsyncPath = filepath.Join(t.TempDir(), "rsync")
	dropDeletes := "#!/bin/sh\nfor a do\n  shift\n  [ \"$a\" = --delete-after ] || set -- \"$@\" \"$a\"\ndone\nexec \"" + real + "\" \"$@\"\n"
	if err := os.WriteFile(f.cfg.RsyncPath, []byte(dropDeletes), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeClean})
	if err == nil || !strings.Contains(err.Error(), "1 planned deletion(s) left in the mirror, first notes/gone.md") {
		t.Fatalf("PushCommand = %v, want the unapplied deletion", err)
	}
	if st, _ := LoadLocalState(f.cfg.LocalPaths); !strings.Contains(st.LastPushError, "planned deletion(s) left") {
		t.Errorf("LastPushError = %q", st.LastPushError)
	}

	if isOpenrsync() {
		return
	}
	f.cfg.RsyncPath = ""
	if _, err := PushCommand(context.Background(), PushOptions{Config: f.cfg, Runner: f.runner, Mode: ModeClean}); err != nil {
		t.Fatalf("next push: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.mirror, "notes/gone.md")); !os.IsNotExist(err) {
		t.Errorf("the deletion is still pending after a working push (err=%v)", err)
	}
}

// A leftover edited in the cloud while the push ran is no longer proven by the
// refreshed baseline, so the move leaves it in the mirror (#225).
func TestPushCommand_LeavesALeftoverEditedDuringThePush(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	f.writeLocal("old/c.md", "c")
	f.seedBaseline("old./c.md", "c", f.writeMirror("old./c.md", "c"))
	leftover := filepath.Join(f.mirror, "old./c.md")
	f.cfg.RsyncPath = writeRsyncThen(t, `echo "edited in the cloud" >> "`+leftover+`"`)
	moved := false
	_, err := PushCommand(context.Background(), PushOptions{
		Config: f.cfg, Runner: f.runner, Mode: ModeClean,
		Progress: func(e SyncEvent) { moved = moved || e.Kind == SyncEventLeftoversMoved },
	})
	if err != nil {
		t.Fatalf("PushCommand: %v", err)
	}
	if _, err := os.Stat(leftover); err != nil || moved {
		t.Errorf("the edited leftover left the mirror (moved=%v, err=%v)", moved, err)
	}
}
