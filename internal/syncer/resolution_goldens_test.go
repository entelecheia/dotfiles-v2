package syncer

// This file records the artifact layout every resolver shape produces at the
// PRE-COLLAPSE tree, so the collapse that follows shows the two defective rows
// changing as a reviewable diff. A row rewritten to match the corrected output
// asserts nothing about the change, which is the whole reason the record has to
// exist before the diff it protects rather than after it.
//
// Two rows below deliberately record a DEFECT as today's behavior. They are
// labeled inline with the site that produces it. When those rows change, read
// the diff: that is the fix landing, not a regression.
//
// Scope note: the rows assert Profile, LockDir, LaunchdPlist, SystemdService,
// SystemdTimer and LogFile. They deliberately cover only the fields the collapse
// moves; a field scheduled for deletion would change value for a reason that has
// nothing to do with either defect, so recording it would make the diff lie.

import (
	"path/filepath"
	"runtime"
	"testing"
)

// goldenLockDir mirrors cacheDirForHome's platform split. A hard-coded ".cache"
// passes the linux unit job and fails the macOS one.
func goldenLockDir(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches", "dotfiles", "sync.lock")
	}
	return filepath.Join(home, ".cache", "dotfiles", "sync.lock")
}

// goldenDefault is the recorded layout of the default ("sync") profile under a
// given home. Every value is spelled out rather than derived from production, so
// the record survives a change in how production derives it.
func goldenDefault(home string) Paths {
	return Paths{
		Profile:        "sync",
		LockDir:        goldenLockDir(home),
		LaunchdPlist:   filepath.Join(home, "Library", "LaunchAgents", "com.dotfiles.sync.plist"),
		SystemdService: filepath.Join(home, ".config", "systemd", "user", "dotfiles-sync.service"),
		SystemdTimer:   filepath.Join(home, ".config", "systemd", "user", "dotfiles-sync.timer"),
		LogFile:        filepath.Join(home, ".local", "log", "dotfiles-sync.log"),
	}
}

// goldenPeer is the recorded layout of the "peer" profile under a given home.
// The scheduler identities are per-profile; the lock is deliberately the default
// one, because peer and cloud runs mutate the same workspace tree and must never
// overlap.
func goldenPeer(home string) Paths {
	p := goldenDefault(home)
	p.Profile = "peer"
	p.LaunchdPlist = filepath.Join(home, "Library", "LaunchAgents", "com.dotfiles.peer.plist")
	p.SystemdService = filepath.Join(home, ".config", "systemd", "user", "dotfiles-peer.service")
	p.SystemdTimer = filepath.Join(home, ".config", "systemd", "user", "dotfiles-peer.timer")
	return p
}

func TestResolutionGoldens(t *testing.T) {
	// Sandbox the process environment the way homeFlagSandbox does. Without all
	// three the empty-home rows read the developer's real home and stop being
	// deterministic.
	invoker := t.TempDir()
	target := t.TempDir()
	t.Setenv("HOME", invoker)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(invoker, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(invoker, ".cache"))

	cases := []struct {
		name string
		// resolve is the one resolver shape this row records.
		resolve func() (*Paths, error)
		want    Paths
		// wantPushPlist, when set, records what PlistFor/LaunchdLabelFor derive
		// from the resolved value. Only the zero-literal row needs it.
		wantPushPlist string
	}{
		{
			name:    "resolveConfig/ResolveScheduler shape, empty home, sync profile",
			resolve: func() (*Paths, error) { return ResolvePathsForHomeProfile("", DefaultProfile) },
			want:    goldenDefault(invoker),
		},
		{
			name:    "resolveConfig/ResolveScheduler shape, target home, sync profile",
			resolve: func() (*Paths, error) { return ResolvePathsForHomeProfile(target, DefaultProfile) },
			want:    goldenDefault(target),
		},
		{
			name:    "resolveConfig/ResolveScheduler shape, target home, peer profile",
			resolve: func() (*Paths, error) { return ResolvePathsForHomeProfile(target, PeerProfile) },
			want:    goldenPeer(target),
		},
		{
			// The fourth cell of the matrix. It sits behind no current call site,
			// and it is the only row that keeps "the home came from the argument"
			// separable from "the profile came from the argument" once both defect
			// rows collapse onto the same call.
			name:    "resolveConfig/ResolveScheduler shape, empty home, peer profile",
			resolve: func() (*Paths, error) { return ResolvePathsForHomeProfile("", PeerProfile) },
			want:    goldenPeer(invoker),
		},
		{
			// DEFECT, recorded as-is: BUG-27 at internal/cli/peer_status.go:120
			// resolves by profile alone, so the --home target is dropped and the
			// layout lands under the INVOKING user's home. The expectation below
			// names invoker, not target, on purpose. This is not the target state.
			name:    "BUG-27 shape (internal/cli/peer_status.go:120), target home, peer profile",
			resolve: func() (*Paths, error) { return ResolvePathsForProfile(PeerProfile) },
			want:    goldenPeer(invoker),
		},
		{
			// DEFECT, recorded as-is: BUG-28 at internal/cli/status_cmd.go:249
			// resolves by home alone, so the caller's peer profile is dropped and
			// the layout carries the default profile and the default scheduler
			// identities. This is not the target state.
			name:    "BUG-28 shape (internal/cli/status_cmd.go:249), target home, peer profile",
			resolve: func() (*Paths, error) { return ResolvePathsForHome(target) },
			want:    goldenDefault(target),
		},
		{
			// The zero-literal fallback at internal/cli/peer_status.go:124. Every
			// field is empty, yet the derived scheduler identity is still the
			// default profile's name under an empty directory.
			name:          "zero-literal shape (internal/cli/peer_status.go:124)",
			resolve:       func() (*Paths, error) { return &Paths{}, nil },
			want:          Paths{},
			wantPushPlist: "com.dotfiles.sync.plist",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.resolve()
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.Profile != tc.want.Profile {
				t.Errorf("Profile = %q, want %q", got.Profile, tc.want.Profile)
			}
			if got.LockDir != tc.want.LockDir {
				t.Errorf("LockDir = %q, want %q", got.LockDir, tc.want.LockDir)
			}
			if got.LaunchdPlist != tc.want.LaunchdPlist {
				t.Errorf("LaunchdPlist = %q, want %q", got.LaunchdPlist, tc.want.LaunchdPlist)
			}
			if got.SystemdService != tc.want.SystemdService {
				t.Errorf("SystemdService = %q, want %q", got.SystemdService, tc.want.SystemdService)
			}
			if got.SystemdTimer != tc.want.SystemdTimer {
				t.Errorf("SystemdTimer = %q, want %q", got.SystemdTimer, tc.want.SystemdTimer)
			}
			if got.LogFile != tc.want.LogFile {
				t.Errorf("LogFile = %q, want %q", got.LogFile, tc.want.LogFile)
			}
			if tc.wantPushPlist != "" && got.PlistFor(SchedulerKindPush) != tc.wantPushPlist {
				t.Errorf("PlistFor(push) = %q, want %q", got.PlistFor(SchedulerKindPush), tc.wantPushPlist)
			}
		})
	}
}
