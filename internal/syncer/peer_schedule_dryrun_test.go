package syncer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// peerScheduleSandbox builds the one situation PeerSchedule's validation chain
// accepts, which is what makes the no-plist row below non-vacuous: without it
// the chain refuses before reaching the write and the row would pass against a
// completely unguarded on-arm.
//
// The chain needs a reachable peer whose `dot peer status --json` points back at
// this workspace, so PATH is pointed at a directory holding a stub `ssh` that
// prints that document and a stub `launchctl` that does nothing. Both are
// absolute-shebang shell scripts, so emptying PATH does not break them.
func peerScheduleSandbox(t *testing.T) (*Config, string) {
	t.Helper()
	names := MachineNames()
	if len(names) == 0 {
		t.Skip("this host reports no machine name, so CheckOwner cannot be satisfied")
	}
	owner := names[0]

	home := t.TempDir()
	t.Setenv("HOME", home)
	localPath := filepath.Join(home, "workspace", "work")
	cfg := &Config{
		Profile:   PeerProfile,
		Owner:     owner,
		LocalPath: localPath,
		Target:    Target{Kind: TargetSSH, Host: "coordinator.example", Path: "/remote/workspace/work"},
		LogFile:   filepath.Join(localPath, ".dotfiles", "peer", "log", "peer.log"),
	}

	binDir := t.TempDir()
	status := fmt.Sprintf(
		`{"schemaVersion":%d,"kind":"peer","profile":{"configured":true,"owner":%q,"workspacePath":%q,"target":{"path":%q}}}`,
		PeerStatusSchemaVersion, owner, cfg.Target.Path, localPath)
	// `echo` is a shell builtin, so the stub needs nothing else on PATH. The
	// document carries no single quote, so single-quoting it is safe.
	writeStub(t, filepath.Join(binDir, "ssh"), "#!/bin/sh\necho '"+status+"'\n")
	writeStub(t, filepath.Join(binDir, "launchctl"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir)

	return cfg, filepath.Join(home, "Library", "LaunchAgents", "com.dotfiles.peer.plist")
}

func writeStub(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func peerScheduleRunner(dryRun bool) *exec.Runner {
	return exec.NewRunner(dryRun, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestPeerSchedule_DryRunWritesNoPlist pins BUG-14. The plist write bypasses the
// runner (os.MkdirAll + os.WriteFile), so a preview left
// ~/Library/LaunchAgents/com.dotfiles.peer.plist on disk with no loaded job
// behind it — precisely the inconsistent state the off-arm thirty lines above
// already refuses to create.
func TestPeerSchedule_DryRunWritesNoPlist(t *testing.T) {
	tests := []struct {
		name          string
		optDryRun     bool
		runnerDryRun  bool
		wantPlistOnFS bool
	}{
		{name: "option set", optDryRun: true, runnerDryRun: true},
		// A caller that set the flag on the runner and not on the option still
		// gets a preview: the same reconciliation the three aisettings managers
		// make.
		{name: "runner flag only", optDryRun: false, runnerDryRun: true},
		// Non-vacuity. Without this row the guard could be a permanent disable
		// and every row above would still be green.
		{name: "no dry-run at all", optDryRun: false, runnerDryRun: false, wantPlistOnFS: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, plist := peerScheduleSandbox(t)
			res, err := PeerSchedule(context.Background(), PeerScheduleOptions{
				Config:   cfg,
				Runner:   peerScheduleRunner(tc.runnerDryRun),
				Probe:    peerScheduleRunner(false),
				Interval: 15 * time.Minute,
				DryRun:   tc.optDryRun,
			})
			if err != nil {
				t.Fatalf("PeerSchedule: %v", err)
			}

			_, statErr := os.Stat(plist)
			if tc.wantPlistOnFS {
				if statErr != nil {
					t.Fatalf("a real run wrote no plist at %s (stat err: %v), so the dry-run guard disabled the install outright", plist, statErr)
				}
				if res.DryRun {
					t.Error("a real run reported DryRun on its result")
				}
				return
			}
			if !os.IsNotExist(statErr) {
				t.Errorf("dry-run wrote the plist it only previews: %s (stat err: %v)", plist, statErr)
			}
			if !res.DryRun {
				t.Error("the result carries no dry-run flag, so cli has nothing to branch on")
			}
			if res.Plist != plist {
				t.Errorf("result names plist %q, want %q — cli renders the path from the result", res.Plist, plist)
			}
		})
	}
}

// TestPeerSchedule_ValidationPrecedenceUnderDryRun holds the guard's POSITION,
// not just its existence. A preview that skipped the validation chain would be a
// different lie from the one BUG-14 fixes, and the off-arm's own precedence
// (validation sits AFTER it, so `--off --interval 1s` succeeds today) must not
// move either.
func TestPeerSchedule_ValidationPrecedenceUnderDryRun(t *testing.T) {
	t.Run("dry-run still fails a bad interval", func(t *testing.T) {
		cfg, plist := peerScheduleSandbox(t)
		_, err := PeerSchedule(context.Background(), PeerScheduleOptions{
			Config:   cfg,
			Runner:   peerScheduleRunner(true),
			Probe:    peerScheduleRunner(false),
			Interval: time.Second,
			DryRun:   true,
		})
		if err == nil {
			t.Fatal("a dry run accepted --interval 1s, so the guard was placed above the validation chain")
		}
		if _, statErr := os.Stat(plist); !os.IsNotExist(statErr) {
			t.Errorf("a rejected dry run still wrote %s", plist)
		}
	})

	t.Run("off arm still precedes validation", func(t *testing.T) {
		cfg, plist := peerScheduleSandbox(t)
		res, err := PeerSchedule(context.Background(), PeerScheduleOptions{
			Config:   cfg,
			Runner:   peerScheduleRunner(true),
			Probe:    peerScheduleRunner(false),
			Interval: time.Second, // below the minimum, and deliberately ignored
			Off:      true,
			DryRun:   true,
		})
		if err != nil {
			t.Fatalf("--off --interval 1s must still succeed: %v", err)
		}
		if !res.Off || !res.DryRun || res.Plist != plist {
			t.Errorf("off-arm result changed shape: %+v", res)
		}
	})
}

func TestPeerSchedule_RejectsInvalidHomeBeforeMutation(t *testing.T) {
	for _, invalidHome := range []string{
		string([]byte{'/', 't', 'm', 'p', '/', 0xff}),
		"/tmp/control\x01home",
	} {
		for _, off := range []bool{false, true} {
			t.Run(fmt.Sprintf("off=%t home=%q", off, invalidHome), func(t *testing.T) {
				cfg, _ := peerScheduleSandbox(t)
				cfg.Home = invalidHome
				plist := filepath.Join(cfg.HomeDir(), "Library", "LaunchAgents", "com.dotfiles.peer.plist")
				const seeded = "seeded peer plist"
				seededOnFS := os.MkdirAll(filepath.Dir(plist), 0o755) == nil
				if seededOnFS {
					seededOnFS = os.WriteFile(plist, []byte(seeded), 0o644) == nil
				}

				res, err := PeerSchedule(context.Background(), PeerScheduleOptions{
					Config: cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false),
					Interval: time.Hour, Off: off,
				})
				if err == nil {
					t.Fatal("invalid home unexpectedly reached peer scheduler mutation")
				}
				if res != nil {
					t.Fatalf("rejected peer scheduler returned result: %+v", res)
				}
				if seededOnFS {
					if got, readErr := os.ReadFile(plist); readErr != nil || string(got) != seeded {
						t.Fatalf("rejection changed seeded plist: got %q, read error %v", got, readErr)
					}
				} else if _, statErr := os.Stat(plist); !os.IsNotExist(statErr) {
					t.Fatalf("rejection created unseedable invalid-byte plist: %v", statErr)
				}
				for _, want := range []string{fmt.Sprintf("%q", invalidHome), plist, "XML 1.0", "left untouched", "dot peer setup"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q missing %q", err, want)
					}
				}
			})
		}
	}
}
