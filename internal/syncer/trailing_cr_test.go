package syncer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// macOS Finder creates `Icon\r` in any folder with a custom icon, and a
// trailing CR is a legal name byte on APFS and Linux alike. Trimming it makes
// every downstream stage address a file that does not exist: rsync stats the
// shortened name, fails, and exits 23, which aborts the whole transaction.
const iconCR = "Icon\r"

func TestNormalizeRelKeepsTrailingCarriageReturn(t *testing.T) {
	for _, rel := range []string{iconCR, "dir/" + iconCR, "trailing space "} {
		if got := normalizeRel(rel); got != rel {
			t.Errorf("normalizeRel(%q) = %q, want it unchanged", rel, got)
		}
	}
}

// The default excludes ship `Icon?` precisely for this file, but that pattern
// needs the CR to match. Trimming produced the phantom "Icon", which no rule
// excluded and no filesystem could stat.
func TestPlanPushExcludesTheIconFileInsteadOfPlanningAPhantom(t *testing.T) {
	f := newIntakeFixture(t)
	f.writeLocal("bundle.library/"+iconCR, "")

	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	for _, rel := range plan.Creates {
		if strings.HasPrefix(rel, "bundle.library/Icon") {
			t.Fatalf("Creates carries %q; the Icon? exclude should have caught it", rel)
		}
	}
}

// The Icon case is one instance of the general rule: a name's bytes reach the
// transfer intact, whatever trailing whitespace they end in.
func TestPlanPushKeepsTrailingCarriageReturnOnAPayloadFile(t *testing.T) {
	f := newIntakeFixture(t)
	want := "bundle.library/notes.md" + "\r"
	f.writeLocal(want, "payload")

	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	found := false
	for _, rel := range plan.Creates {
		if rel == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("Creates = %q, want it to carry %q byte for byte", plan.Creates, want)
	}
}

func TestPeerPlanListWritesTheNameVerbatim(t *testing.T) {
	f := newIntakeFixture(t)
	rel := "bundle.library/" + iconCR

	path, err := peerPlanList(f.cfg, []string{rel})
	if err != nil {
		t.Fatalf("peerPlanList: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records := strings.Split(strings.TrimSuffix(string(body), "\x00"), "\x00")
	if len(records) != 1 || records[0] != rel {
		t.Fatalf("plan records = %q, want [%q]", records, rel)
	}
}

// The baseline is the provenance record every delete decision reads, so a
// name it cannot round-trip becomes a path that looks mirror-only forever.
func TestBaselineRoundTripsTrailingCarriageReturn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.manifest")
	rel := "bundle.library/" + iconCR
	if err := SaveBaselineManifest(path, map[string]Fingerprint{rel: {Size: 0}}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBaselineManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[rel]; !ok {
		t.Fatalf("baseline keys = %q, want %q", keysOf(got), rel)
	}
}

func keysOf(m map[string]Fingerprint) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
