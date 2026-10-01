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
		"@@written >f+++++++++ lit\\#134#123.md",                     // a literal \#123 in the name
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
	if _, err := os.Stat(filepath.Join(f.mirror, "renders/work-1/frame_0001.jpg")); !os.IsNotExist(err) {
		t.Errorf("frame still in the mirror after the next push (err=%v)", err)
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
	// old copy from before the name filter.
	f.writeLocal("spoc/notice/a.pdf", "a")
	f.writeMirror("spoc/notice./a.pdf", "a")
	f.writeMirror("spoc/notice./.DS_Store", "finder")
	// A workspace name Dropbox cannot store stays excluded and its mirror
	// copy is not touched.
	f.writeLocal("keep./b.md", "b")
	f.writeMirror("keep./b.md", "b")

	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Leftovers, []string{"spoc/notice./a.pdf"}) || !slices.Equal(plan.Unsupported, []string{"keep./b.md"}) {
		t.Fatalf("Leftovers = %v, Unsupported = %v", plan.Leftovers, plan.Unsupported)
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
	if _, err := os.Stat(filepath.Join(f.mirror, "spoc/notice.")); !os.IsNotExist(err) {
		t.Errorf("old folder still in the mirror (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(f.mirror, "spoc/notice/a.pdf")); err != nil {
		t.Errorf("renamed copy missing from the mirror: %v", err)
	}
	if moved.Candidates != 1 || !strings.HasPrefix(moved.Path, filepath.Join(f.local, ".sync-conflicts")) {
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
