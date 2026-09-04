package syncer

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// makePlaceholder writes the 0-byte stub a cloud provider leaves behind when
// it evicts a file. Filesystems that reject the marker skip the calling test;
// the negative cases below still run everywhere.
func makePlaceholder(t *testing.T, abs string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(abs, dropboxPlaceholderAttr, []byte{}, 0); err != nil {
		t.Skipf("cloud placeholder xattr unsupported here: %v", err)
	}
}

func TestDehydratedFileRequiresAMarkerNotJustEmptiness(t *testing.T) {
	dir := t.TempDir()
	content := filepath.Join(dir, "content.md")
	if err := os.WriteFile(content, []byte("real bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{content, empty} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if dehydratedFile(path, info) {
			t.Errorf("%s reported as a cloud placeholder", filepath.Base(path))
		}
	}

	stub := filepath.Join(dir, "stub.md")
	makePlaceholder(t, stub)
	info, err := os.Lstat(stub)
	if err != nil {
		t.Fatal(err)
	}
	if !dehydratedFile(stub, info) {
		t.Error("marked placeholder not detected")
	}
}

// The mirror is a derived copy, so an evicted twin is a stub to overwrite,
// not a divergence to refuse. Before this, a mirror the provider had emptied
// produced a conflict per file and the push refused outright.
func TestPlanPushUpdatesAnEvictedMirrorTwin(t *testing.T) {
	f := newIntakeFixture(t)
	f.writeLocal("docs/report.md", "the real content")
	makePlaceholder(t, filepath.Join(f.mirror, "docs/report.md"))

	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	if len(plan.Conflicts) != 0 {
		t.Fatalf("Conflicts = %+v, want none", plan.Conflicts)
	}
	if len(plan.Updates) != 1 || plan.Updates[0] != "docs/report.md" {
		t.Fatalf("Updates = %v, want [docs/report.md]", plan.Updates)
	}
	if plan.Placeholders != 1 {
		t.Errorf("Placeholders = %d, want 1", plan.Placeholders)
	}
}

// Provenance is a separate question from hydration: the mirror-only path
// still has nothing proving it came from local, so it must keep refusing.
func TestPlanPushStillRefusesToDeleteAMirrorOnlyPlaceholder(t *testing.T) {
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	makePlaceholder(t, filepath.Join(f.mirror, "docs/orphan.md"))

	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	if len(plan.Deletes) != 0 {
		t.Fatalf("Deletes = %v, want none", plan.Deletes)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].RelPath != "docs/orphan.md" {
		t.Fatalf("Conflicts = %+v, want docs/orphan.md", plan.Conflicts)
	}
	if plan.Conflicts[0].Reason != "mirror-only cloud placeholder is not in baseline" {
		t.Errorf("Reason = %q, want the placeholder wording", plan.Conflicts[0].Reason)
	}
}

// A baseline rebuilt from an evicted mirror used to record a 0-byte stub,
// which turned every later local edit into divergence from the baseline.
func TestRefreshBaselineKeepsPlaceholderStubsOut(t *testing.T) {
	f := newIntakeFixture(t)
	body := "pushed earlier"
	mtime := f.writeMirror("docs/kept.md", body)
	f.writeLocal("docs/kept.md", body)
	f.seedBaseline("docs/kept.md", body, mtime)
	makePlaceholder(t, filepath.Join(f.mirror, "docs/kept.md"))

	f.writeLocal("docs/fresh.md", "never pushed")
	makePlaceholder(t, filepath.Join(f.mirror, "docs/fresh.md"))

	if err := RefreshBaseline(f.cfg, FingerprintFast); err != nil {
		t.Fatalf("RefreshBaseline: %v", err)
	}
	baseline, err := LoadBaselineManifest(f.cfg.LocalPaths.BaselineFile)
	if err != nil {
		t.Fatal(err)
	}
	kept, ok := baseline["docs/kept.md"]
	if !ok {
		t.Fatal("baseline dropped a proven path because its mirror copy was evicted")
	}
	if kept.Size != int64(len(body)) {
		t.Errorf("baseline size = %d, want the carried-forward %d", kept.Size, len(body))
	}
	if _, ok := baseline["docs/fresh.md"]; ok {
		t.Error("baseline invented an entry for a placeholder it had never seen hydrated")
	}
}
