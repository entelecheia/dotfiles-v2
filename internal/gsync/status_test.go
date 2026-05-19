package gsync

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
