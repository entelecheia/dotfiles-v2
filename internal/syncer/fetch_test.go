package syncer

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireRsync(t *testing.T) {
	t.Helper()
	if _, err := osexec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}
}

func TestFetch_FileAndDirectory(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.writeMirror("docs/report.pdf", "pdf-payload")
	f.writeMirror("assets/deck/a.png", "img-a")
	f.writeMirror("assets/deck/b.png", "img-b")

	res, err := Fetch(context.Background(), f.runner, f.cfg, []string{"docs/report.pdf", "assets/deck", "no/such/path"}, false)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(res.Fetched) != 2 {
		t.Fatalf("Fetched = %v, want 2 paths", res.Fetched)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "no/such/path" {
		t.Fatalf("Missing = %v", res.Missing)
	}
	for _, rel := range []string{"docs/report.pdf", "assets/deck/a.png", "assets/deck/b.png"} {
		if _, err := os.Stat(filepath.Join(f.local, rel)); err != nil {
			t.Errorf("fetched path missing locally: %s (%v)", rel, err)
		}
	}
}

func TestFetch_NeverOverwritesNewerLocal(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.writeMirror("data/table.xlsx", "old-mirror-version")
	f.writeLocal("data/table.xlsx", "newer-local-version")
	// Make the local copy strictly newer.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(f.local, "data/table.xlsx"), future, future); err != nil {
		t.Fatal(err)
	}

	if _, err := Fetch(context.Background(), f.runner, f.cfg, []string{"data/table.xlsx"}, false); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(f.local, "data/table.xlsx"))
	if string(got) != "newer-local-version" {
		t.Errorf("fetch overwrote newer local file: %q", got)
	}
}

func TestFetch_BlocksSecretsAndGit(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.writeMirror("proj/.env", "TOKEN=leak")
	f.writeMirror("proj/data.pdf", "fine")
	f.writeMirror("proj/.git", "gitdir: nope")

	if _, err := Fetch(context.Background(), f.runner, f.cfg, []string{"proj"}, false); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.local, "proj/data.pdf")); err != nil {
		t.Errorf("payload not fetched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.local, "proj/.env")); !os.IsNotExist(err) {
		t.Error("secret imported by fetch")
	}
	if _, err := os.Stat(filepath.Join(f.local, "proj/.git")); !os.IsNotExist(err) {
		t.Error(".git imported by fetch")
	}
}

func TestFetch_AllowedSecretComesThrough(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.AllowPatterns = []string{"/proj/.env"}
	f.writeMirror("proj/.env", "TOKEN=allowed")

	if _, err := Fetch(context.Background(), f.runner, f.cfg, []string{"proj"}, false); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(f.local, "proj/.env"))
	if err != nil {
		t.Fatalf("allowed secret not fetched: %v", err)
	}
	if string(got) != "TOKEN=allowed" {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestFetch_DryRunWritesNothing(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.writeMirror("docs/x.pdf", "payload")

	res, err := Fetch(context.Background(), f.runner, f.cfg, []string{"docs/x.pdf"}, true)
	if err != nil {
		t.Fatalf("Fetch dry-run: %v", err)
	}
	if len(res.Fetched) != 1 {
		t.Fatalf("Fetched = %v", res.Fetched)
	}
	if _, err := os.Stat(filepath.Join(f.local, "docs/x.pdf")); !os.IsNotExist(err) {
		t.Error("dry-run materialized the file")
	}
}

func TestFetch_SSHSourceSpelling(t *testing.T) {
	// Args-only check: verify the --relative source form for ssh targets
	// without invoking rsync.
	cfg := newTestConfig(t)
	cfg.Target = Target{Kind: TargetSSH, Host: "me@host", Path: "~/work"}
	src := strings.TrimRight(cfg.Target.RsyncDest(), "/")
	if src != "me@host:~/work" {
		t.Fatalf("ssh fetch source root = %q", src)
	}
	if got := src + "/./" + "a/b.pdf"; got != "me@host:~/work/./a/b.pdf" {
		t.Fatalf("ssh fetch source spec = %q", got)
	}
}
