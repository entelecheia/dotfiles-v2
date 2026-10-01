package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStatusCommandsReportRefusedConflictDirectory(t *testing.T) {
	_, root := goldenSyncFixture(t)
	link := filepath.Join(root, ".sync-conflicts")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"sync", "peer"} {
		t.Run(command, func(t *testing.T) {
			out, stderr, err := runDotForTest(command, "status", "--json")
			if err != nil {
				t.Fatalf("status: %v, %s", err, stderr)
			}
			var document map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &document); err != nil {
				t.Fatal(err)
			}
			if command == "peer" {
				var profile map[string]json.RawMessage
				if err := json.Unmarshal(document["profile"], &profile); err != nil {
					t.Fatal(err)
				}
				document = profile
			}
			var message string
			if err := json.Unmarshal(document["conflictsError"], &message); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(message, ".sync-conflicts") || !strings.Contains(message, "not a plain directory") {
				t.Fatalf("conflictsError = %q", message)
			}
			out, stderr, err = runDotForTest(command, "status")
			if err != nil || !strings.Contains(out+stderr, "conflict backups not listed") {
				t.Fatalf("text: %v, %s%s", err, out, stderr)
			}
			if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("status changed conflict symlink: %v", err)
			}
		})
	}
}

func TestSyncStatusReportsSpawnFailed(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd status is macOS-specific")
	}
	home, _ := goldenSyncFixture(t)
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.dotfiles.sync.plist")
	writeCLITestFile(t, plist, "persisted plist")
	dump := filepath.Join(t.TempDir(), "print.out")
	writeCLITestFile(t, dump, "state = spawn failed\nlast exit code = 78: EX_CONFIG\n")
	bin := t.TempDir()
	stub := filepath.Join(bin, "launchctl")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n[ \"$1\" = print ] || exit 99\n/bin/cat \"$DOTFILES_TEST_LAUNCHCTL_DUMP\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOTFILES_TEST_LAUNCHCTL_DUMP", dump)
	out, stderr, err := runDotForTest("sync", "status", "--json")
	if err != nil {
		t.Fatalf("status: %v, %s", err, stderr)
	}
	var document syncStatusJSON
	if err := json.Unmarshal([]byte(out), &document); err != nil {
		t.Fatal(err)
	}
	job := document.Jobs[0]
	if !strings.Contains(job.State, "spawn failed") || job.LastExitCode == nil || *job.LastExitCode != 78 {
		t.Fatalf("job = %+v", job)
	}
	out, stderr, err = runDotForTest("sync", "status")
	for _, wanted := range []string{"spawn failed", "dot sync pause && dot sync resume", "Push last exit", "78"} {
		if err != nil || !strings.Contains(out+stderr, wanted) {
			t.Fatalf("missing %q: %v, %s%s", wanted, err, out, stderr)
		}
	}
	if contents, err := os.ReadFile(plist); err != nil || string(contents) != "persisted plist" {
		t.Fatalf("status changed plist: %v", err)
	}
}
