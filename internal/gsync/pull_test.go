package gsync

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedBaselineStrict records rel in baseline.manifest with a strict
// (sha-bearing) fingerprint taken from the mirror copy.
func seedBaselineStrict(f *intakeFixture, rel string) Fingerprint {
	f.t.Helper()
	fp, err := FingerprintFile(filepath.Join(f.mirror, rel), FingerprintStrict)
	if err != nil {
		f.t.Fatal(err)
	}
	existing, _ := LoadBaselineManifest(f.cfg.LocalPaths.BaselineFile)
	existing[rel] = fp
	if err := SaveBaselineManifest(f.cfg.LocalPaths.BaselineFile, existing); err != nil {
		f.t.Fatal(err)
	}
	return fp
}

func chtimes(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineMatchTiered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	strictFP, err := FingerprintFile(path, FingerprintStrict)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("fast match skips hashing", func(t *testing.T) {
		base := Fingerprint{Size: info.Size(), Mtime: info.ModTime().UTC(), Sha: strictFP.Sha}
		match, fp, err := baselineMatchTiered(base, path, info, false)
		if err != nil {
			t.Fatal(err)
		}
		if !match {
			t.Error("want match on identical size+mtime")
		}
		if fp.Sha != "" {
			t.Error("fast tier must not hash")
		}
	})

	t.Run("fast mismatch escalates against sha-bearing baseline", func(t *testing.T) {
		base := Fingerprint{Size: info.Size() + 1, Mtime: info.ModTime().UTC(), Sha: strictFP.Sha}
		match, fp, err := baselineMatchTiered(base, path, info, false)
		if err != nil {
			t.Fatal(err)
		}
		if !match {
			t.Error("sha-equal file must match despite fast drift")
		}
		if fp.Sha == "" {
			t.Error("escalation should return a strict fingerprint")
		}
	})

	t.Run("fast mismatch is final for sha-less baseline", func(t *testing.T) {
		base := Fingerprint{Size: info.Size() + 1, Mtime: info.ModTime().UTC()}
		match, fp, err := baselineMatchTiered(base, path, info, false)
		if err != nil {
			t.Fatal(err)
		}
		if match {
			t.Error("want mismatch — no stronger signal exists")
		}
		if fp.Sha != "" {
			t.Error("sha-less baseline must not trigger hashing")
		}
	})

	t.Run("strict mode always hashes", func(t *testing.T) {
		base := Fingerprint{Size: info.Size(), Mtime: info.ModTime().UTC(), Sha: strictFP.Sha}
		match, fp, err := baselineMatchTiered(base, path, info, true)
		if err != nil {
			t.Fatal(err)
		}
		if !match || fp.Sha == "" {
			t.Errorf("strict: match=%v sha=%q, want hashed match", match, fp.Sha)
		}
	})
}

func TestPullTracked_EscalatesOnMtimeDriftContentSame(t *testing.T) {
	f := newIntakeFixture(t)
	rel := "assets/logo.png"
	body := "same-bytes"
	f.writeMirror(rel, body)
	base := seedBaselineStrict(f, rel)
	f.writeLocal(rel, body)
	chtimes(t, filepath.Join(f.local, rel), base.Mtime)

	// Same content on the mirror, mtime pushed 2s forward (sameMtime
	// compares at second resolution).
	drifted := base.Mtime.Add(2 * time.Second)
	chtimes(t, filepath.Join(f.mirror, rel), drifted)

	res, err := PullTracked(f.cfg, PullOptions{})
	if err != nil {
		t.Fatalf("PullTracked: %v", err)
	}
	if len(res.SkippedBase) != 1 || len(res.Pulled) != 0 || len(res.Conflicts) != 0 {
		t.Fatalf("want pure SkippedBase, got %+v", res)
	}

	// The baseline mtime must self-heal so the next pull takes the fast
	// path instead of re-hashing forever.
	baseline, err := LoadBaselineManifest(f.cfg.LocalPaths.BaselineFile)
	if err != nil {
		t.Fatal(err)
	}
	got := baseline[rel]
	if !sameMtime(got.Mtime, drifted) {
		t.Errorf("baseline mtime = %v, want refreshed to %v", got.Mtime, drifted)
	}
	if got.Sha == "" {
		t.Error("refreshed baseline entry must stay strict")
	}

	res2, err := PullTracked(f.cfg, PullOptions{})
	if err != nil {
		t.Fatalf("second PullTracked: %v", err)
	}
	if len(res2.SkippedBase) != 1 || len(res2.Pulled) != 0 {
		t.Errorf("second run should fast-skip, got %+v", res2)
	}
}

func TestPullTracked_FastTradeoffAndStrictOverride(t *testing.T) {
	setup := func(t *testing.T) (*intakeFixture, string) {
		f := newIntakeFixture(t)
		rel := "assets/data.bin"
		f.writeMirror(rel, "v1-bytes")
		base := seedBaselineStrict(f, rel)
		f.writeLocal(rel, "v1-bytes")
		chtimes(t, filepath.Join(f.local, rel), base.Mtime)
		// Same-length content change with the mtime reset to the baseline
		// value — invisible to the fast tier by construction.
		if err := os.WriteFile(filepath.Join(f.mirror, rel), []byte("v2-BYTES"), 0o644); err != nil {
			t.Fatal(err)
		}
		chtimes(t, filepath.Join(f.mirror, rel), base.Mtime)
		return f, rel
	}

	t.Run("default fast tier misses size+mtime-preserving change", func(t *testing.T) {
		f, _ := setup(t)
		res, err := PullTracked(f.cfg, PullOptions{})
		if err != nil {
			t.Fatalf("PullTracked: %v", err)
		}
		if len(res.Pulled) != 0 || len(res.SkippedBase) != 1 {
			t.Errorf("fast tier should skip (documented tradeoff), got %+v", res)
		}
	})

	t.Run("--strict catches it", func(t *testing.T) {
		f, rel := setup(t)
		res, err := PullTracked(f.cfg, PullOptions{Strict: true})
		if err != nil {
			t.Fatalf("PullTracked: %v", err)
		}
		if len(res.Pulled) != 1 || res.Pulled[0] != rel {
			t.Fatalf("strict run should pull, got %+v", res)
		}
		got, err := os.ReadFile(filepath.Join(f.local, rel))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "v2-BYTES" {
			t.Errorf("local = %q, want strict-pulled mirror content", got)
		}
	})
}

func TestPullTracked_AdoptsEqualSidesDespiteMtimeDrift(t *testing.T) {
	f := newIntakeFixture(t)
	rel := "shared/deck.pptx"
	v1Mtime := f.writeMirror(rel, "v1")
	f.seedBaseline(rel, "v1", v1Mtime) // sha-less baseline

	// Both sides independently received the same v2 with different mtimes.
	if err := os.WriteFile(filepath.Join(f.mirror, rel), []byte("v2-everywhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	chtimes(t, filepath.Join(f.mirror, rel), v1Mtime.Add(2*time.Second))
	f.writeLocal(rel, "v2-everywhere")
	chtimes(t, filepath.Join(f.local, rel), v1Mtime.Add(4*time.Second))

	res, err := PullTracked(f.cfg, PullOptions{})
	if err != nil {
		t.Fatalf("PullTracked: %v", err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("sha-equal sides must not conflict: %+v", res.Conflicts)
	}
	if len(res.SkippedBase) != 1 || res.SkippedBase[0] != rel {
		t.Fatalf("want adopt via SkippedBase, got %+v", res)
	}
	baseline, err := LoadBaselineManifest(f.cfg.LocalPaths.BaselineFile)
	if err != nil {
		t.Fatal(err)
	}
	if baseline[rel].Sha == "" {
		t.Error("adopted baseline entry must be strict")
	}
}

func TestNeedsBaselineUpdate_RefreshesStaleMtime(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tests := []struct {
		name    string
		base    Fingerprint
		current Fingerprint
		want    bool
	}{
		{
			name:    "identical strict entries",
			base:    Fingerprint{Size: 2, Mtime: now, Sha: "aa"},
			current: Fingerprint{Size: 2, Mtime: now, Sha: "aa"},
			want:    false,
		},
		{
			name:    "sha upgrade for sha-less baseline",
			base:    Fingerprint{Size: 2, Mtime: now},
			current: Fingerprint{Size: 2, Mtime: now, Sha: "aa"},
			want:    true,
		},
		{
			name:    "content change",
			base:    Fingerprint{Size: 2, Mtime: now, Sha: "aa"},
			current: Fingerprint{Size: 2, Mtime: now, Sha: "bb"},
			want:    true,
		},
		{
			name:    "sha equal but mtime drifted",
			base:    Fingerprint{Size: 2, Mtime: now, Sha: "aa"},
			current: Fingerprint{Size: 2, Mtime: now.Add(5 * time.Second), Sha: "aa"},
			want:    true,
		},
		{
			name:    "fast-only entries identical",
			base:    Fingerprint{Size: 2, Mtime: now},
			current: Fingerprint{Size: 2, Mtime: now},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsBaselineUpdate(tt.base, tt.current); got != tt.want {
				t.Errorf("needsBaselineUpdate = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPullTracked_DetectsLocalEditPreservingSizeMtime(t *testing.T) {
	f := newIntakeFixture(t)
	rel := "assets/data.bin"
	f.writeMirror(rel, "v1-bytes")
	base := seedBaselineStrict(f, rel)

	// Local edit that preserves size AND mtime — invisible to the fast
	// tier. The mirror also changed, so the local file is about to be
	// overwritten; the pre-overwrite hash must surface this as a conflict
	// instead of silently destroying the local edit.
	f.writeLocal(rel, "v1-EDITS") // same length as v1-bytes
	chtimes(t, filepath.Join(f.local, rel), base.Mtime)
	if err := os.WriteFile(filepath.Join(f.mirror, rel), []byte("v2-from-drive"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := PullTracked(f.cfg, PullOptions{})
	if err != nil {
		t.Fatalf("PullTracked: %v", err)
	}
	if len(res.Pulled) != 0 {
		t.Fatalf("local edit silently overwritten: Pulled = %v", res.Pulled)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].RelPath != rel {
		t.Fatalf("Conflicts = %+v, want %s", res.Conflicts, rel)
	}
	got, err := os.ReadFile(filepath.Join(f.local, rel))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v1-EDITS" {
		t.Errorf("local content = %q, want preserved edit", got)
	}
}

func TestPullTracked_RestoreUpgradesShaLessBaseline(t *testing.T) {
	f := newIntakeFixture(t)
	rel := "assets/image.bin"
	mtime := f.writeMirror(rel, "binary-payload")
	f.seedBaseline(rel, "binary-payload", mtime) // legacy sha-less entry

	res, err := PullTracked(f.cfg, PullOptions{})
	if err != nil {
		t.Fatalf("PullTracked: %v", err)
	}
	if len(res.Restored) != 1 || res.Restored[0] != rel {
		t.Fatalf("Restored = %v, want %s", res.Restored, rel)
	}
	baseline, err := LoadBaselineManifest(f.cfg.LocalPaths.BaselineFile)
	if err != nil {
		t.Fatal(err)
	}
	if baseline[rel].Sha == "" {
		t.Error("restore must upgrade a sha-less baseline entry to strict")
	}
}
