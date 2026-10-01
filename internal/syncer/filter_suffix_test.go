package syncer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The Go filter twin matches an unanchored slash pattern against the end of
// the path, as rsync does: foo/*.log excludes a/foo/x.log too (#233).
func TestExcludePattern_UnanchoredSlashPatternMatchesPathEnd(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		rel     string
		want    bool
	}{
		{"foo/*.log", "a/foo/x.log", true},   // end-of-path wildcard match
		{"foo/*.log", "foo/x.log", true},     // whole path still matches
		{"foo/*.log", "a/b/foo/x.log", true}, // deeper end-of-path match
		{"foo/*.log", "a/foo/x.txt", false},  // wildcard does not reach
		{"foo/*.log", "a/xfoo/x.log", false}, // component-aligned only
		{"/foo/*.log", "a/foo/x.log", false}, // anchored stays anchored
		{"/foo/*.log", "foo/x.log", true},
		{"foo/bar.log", "a/foo/bar.log", true}, // literal end-of-path (pre-#233 behavior)
	} {
		p := excludePattern{raw: tc.pattern}
		if got := p.matches(tc.rel, false); got != tc.want {
			t.Errorf("excludePattern{%q}.matches(%q) = %v, want %v", tc.pattern, tc.rel, got, tc.want)
		}
	}
}

// appendExcludes adds operator patterns to the fixture's exclude file, which
// both the Go filter twin and the rsync --exclude-from chain read.
func (f *intakeFixture) appendExcludes(patterns ...string) {
	f.t.Helper()
	body, err := os.ReadFile(f.cfg.ExcludesFile)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, p := range patterns {
		body = append(body, []byte(p+"\n")...)
	}
	if err := os.WriteFile(f.cfg.ExcludesFile, body, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// Real rsync, exclude mode: with foo/*.log in exclude.txt the plan lists no
// create rsync will not send, and the transfer writes exactly the plan (#233).
func TestPush_PlanMatchesRsyncForUnanchoredSlashPattern(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.appendExcludes("foo/*.log")
	for _, rel := range []string{"a/foo/x.log", "foo/y.log", "a/foo/keep.md", "keep.md"} {
		f.writeLocal(rel, rel)
	}

	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's workspace layout seeds a root .gitignore, which syncs.
	want := []string{".gitignore", "a/foo/keep.md", "keep.md"}
	if !slices.Equal(plan.Creates, want) {
		t.Fatalf("Creates = %v, want %v (the end-of-path matches must stay out)", plan.Creates, want)
	}
	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("Push: %v", err)
	}
	for _, rel := range want {
		if _, err := os.Stat(filepath.Join(f.mirror, rel)); err != nil {
			t.Errorf("%s planned but not sent: %v", rel, err)
		}
	}
	for _, rel := range []string{"a/foo/x.log", "foo/y.log"} {
		if _, err := os.Stat(filepath.Join(f.mirror, rel)); !os.IsNotExist(err) {
			t.Errorf("rsync sent %s past the end-of-path pattern (err=%v)", rel, err)
		}
	}
}

// Real rsync: a fetch naming a/foo/x.log reports it excluded and rsync sends
// nothing, so the file stays absent from the workspace (#233).
func TestFetch_UnanchoredSlashPatternIsExcludedLikeRsync(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.appendExcludes("foo/*.log")
	f.writeMirror("a/foo/x.log", "x")
	f.writeMirror("a/foo/keep.md", "keep")

	res, err := Fetch(context.Background(), f.runner, f.cfg, []string{"a/foo/x.log", "a/foo/keep.md"}, false)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !slices.Equal(res.Excluded, []string{"a/foo/x.log"}) || !slices.Equal(res.Fetched, []string{"a/foo/keep.md"}) {
		t.Fatalf("Fetch result = %+v, want x.log excluded and keep.md fetched", res)
	}
	if _, err := os.Stat(filepath.Join(f.local, "a/foo/keep.md")); err != nil {
		t.Errorf("keep.md was not fetched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.local, "a/foo/x.log")); !os.IsNotExist(err) {
		t.Errorf("rsync fetched a/foo/x.log past the end-of-path pattern (err=%v)", err)
	}
}
