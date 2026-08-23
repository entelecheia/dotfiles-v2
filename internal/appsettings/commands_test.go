package appsettings

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// TestStampLastBackup_ReportsWhetherTheWriteLanded pins BUG-11. The caller
// (internal/cli/apps_backup.go) records state.Modules.MacApps.LastBackup on
// the strength of the returned flag alone, so a flag that says "stamped"
// after a failed write makes user state claim a backup with nothing on disk
// behind it.
//
// The unwritable-root row is the non-vacuous one: before the fix it returned
// true alongside the error.
func TestStampLastBackup_ReportsWhetherTheWriteLanded(t *testing.T) {
	for _, row := range []struct {
		name        string
		dryRun      bool
		failed      int
		createRoot  bool
		wantStamped bool
		wantErr     bool
	}{
		{name: "successful write stamps", createRoot: true, wantStamped: true},
		{name: "unwritable root does not stamp", wantErr: true},
		{name: "dry run does not stamp", dryRun: true, createRoot: true},
		{name: "failed paths do not stamp", failed: 1, createRoot: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			home := t.TempDir()
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
			eng := &Engine{
				Runner:   exec.NewRunner(row.dryRun, logger),
				HomeDir:  home,
				Root:     filepath.Join(home, "bk"),
				Hostname: "h",
				Manifest: &Manifest{},
			}
			if row.createRoot {
				if err := os.MkdirAll(eng.HostRoot(), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			stamped, err := eng.StampLastBackup(&Summary{Failed: row.failed, Files: 2}, []string{"a"})
			if row.wantErr && err == nil {
				t.Fatal("writing into a host root that does not exist should fail")
			}
			if !row.wantErr && err != nil {
				t.Fatalf("StampLastBackup: %v", err)
			}
			if stamped != row.wantStamped {
				t.Errorf("stamped = %v, want %v — the caller records a last-backup in user state on this flag alone (BUG-11)", stamped, row.wantStamped)
			}

			// The flag and the disk must agree in both directions.
			got, readErr := eng.ReadLastBackupStamp()
			if readErr != nil {
				t.Fatalf("ReadLastBackupStamp: %v", readErr)
			}
			if row.wantStamped && got == nil {
				t.Error("reported stamped, but no stamp is on disk")
			}
			if !row.wantStamped && got != nil {
				t.Errorf("reported not stamped, but a stamp is on disk: %+v", got)
			}
		})
	}
}
