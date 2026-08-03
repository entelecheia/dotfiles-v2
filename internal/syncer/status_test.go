package syncer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

func TestGetStatus_IgnoresStaleLock(t *testing.T) {
	f := newIntakeFixture(t)
	if err := os.MkdirAll(f.cfg.LockDir, 0o755); err != nil {
		t.Fatalf("seed lock dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.cfg.LockDir, "lock.pid"), []byte("99999999"), 0o644); err != nil {
		t.Fatalf("seed lock pid: %v", err)
	}

	st, err := GetStatus(context.Background(), f.runner, f.cfg, &config.UserState{}, nil)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if st.LockHeld {
		t.Fatal("stale lock reported as held")
	}
}

func TestGetStatus_SSHPeerCountsLocalConflictsOnce(t *testing.T) {
	f := newIntakeFixture(t)
	f.cfg.Profile = PeerProfile
	f.cfg.Target = Target{Kind: TargetSSH, Host: "peer", Path: "/work"}
	f.cfg.MirrorPath = ""
	conflict := filepath.Join(f.local, conflictsDirName, "2026-08-04T00-00-00Z")
	if err := os.MkdirAll(conflict, 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(f.local); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	st, err := GetStatus(context.Background(), f.runner, f.cfg, &config.UserState{}, nil)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if got := len(st.Conflicts); got != 1 {
		t.Fatalf("SSH peer conflicts = %d, want 1", got)
	}
}
