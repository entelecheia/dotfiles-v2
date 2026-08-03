package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

func TestBuildSyncStatusJSONReportsStableSchemaAndJobs(t *testing.T) {
	root := t.TempDir()
	paths := syncer.ResolveLocalPaths(root)
	if err := os.MkdirAll(paths.StoreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("propagation:\n  create: true\n  update: true\n  delete: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lastPush := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	cfg := &syncer.Config{
		Profile:     syncer.DefaultProfile,
		LocalPath:   root + "/",
		Target:      syncer.Target{Kind: syncer.TargetLocal, Path: t.TempDir()},
		LocalPaths:  paths,
		Propagation: syncer.DefaultPropagationPolicy(),
		Interval:    600,
		PushMode:    syncer.ModeClean,
		PullMode:    syncer.ModeClean,
	}
	status := &syncer.Status{
		Profile:        syncer.DefaultProfile,
		LocalPath:      root,
		StoreDir:       paths.StoreDir,
		Target:         cfg.Target,
		LocalExists:    true,
		MirrorExists:   true,
		Propagation:    cfg.Propagation,
		Interval:       600,
		PushMode:       syncer.ModeClean,
		PullMode:       syncer.ModeClean,
		SchedulerState: syncer.SchedulerRunning,
		LastPush:       lastPush,
	}
	scheduler := &syncer.Scheduler{Paths: &syncer.Paths{
		LaunchdPlist: filepath.Join(root, "com.dotfiles.sync.plist"),
	}}
	document := buildSyncStatusJSON(cfg, status, scheduler)
	if document.SchemaVersion != 1 || document.Kind != "mirror" || !document.Configured {
		t.Fatalf("unexpected status document: %+v", document)
	}
	if len(document.Jobs) != 2 || document.Jobs[0].IntervalSeconds != 600 {
		t.Fatalf("unexpected jobs: %+v", document.Jobs)
	}
	if document.Jobs[0].LastRunAt == nil || *document.Jobs[0].LastRunAt != lastPush.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected last run: %+v", document.Jobs[0].LastRunAt)
	}
	cfg.Profile = "archive"
	if got := buildSyncStatusJSON(cfg, status, scheduler).Kind; got != "mirror" {
		t.Fatalf("non-peer profile kind = %q, want mirror", got)
	}
	cfg.Profile = syncer.PeerProfile
	if got := buildSyncStatusJSON(cfg, status, scheduler).Kind; got != "peer-profile" {
		t.Fatalf("peer profile kind = %q, want peer-profile", got)
	}
}

func TestActivePatternCountIgnoresCommentsAndBlanks(t *testing.T) {
	content := "# header\n\n*.md\n  # comment\n/.secrets/**\n"
	if got := activePatternCount(content); got != 2 {
		t.Fatalf("activePatternCount = %d, want 2", got)
	}
}

func TestAddsActivePatternChecksPatternIdentityNotCount(t *testing.T) {
	current := "# approved\n/.secrets/narrow.env\n"
	if !addsActivePattern(current, "/**\n") {
		t.Fatal("same-count replacement with a broader new pattern must require acknowledgement")
	}
	if addsActivePattern(current, "\n/.secrets/narrow.env\n# reordered\n") {
		t.Fatal("reordering the same active patterns must not require acknowledgement")
	}
}

func TestWritePatternFileAtomicReplacesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "allow.txt")
	if err := writePatternFileAtomic(path, []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	if err := writePatternFileAtomic(path, []byte("two\n")); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "two\n" {
		t.Fatalf("content = %q", body)
	}
}

func TestTailSyncLogKeepsNewestLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := tailSyncLog(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got != "two\nthree" {
		t.Fatalf("tail = %q", got)
	}
}

func TestTailSyncLogSurfacesReadErrors(t *testing.T) {
	if _, err := tailSyncLog(t.TempDir(), 2); err == nil {
		t.Fatal("reading a directory as a log must return an error")
	}
}
