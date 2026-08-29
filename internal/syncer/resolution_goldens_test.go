package syncer

// This file records the artifact layout every resolver shape produces. It
// landed one commit BEFORE the collapse, recording the two defects as today's
// behavior, so that the collapse commit shows those two rows changing as a
// reviewable diff. A row rewritten to match the corrected output asserts
// nothing about the change, which is the whole reason the record had to exist
// before the diff it protects rather than after it.
//
// The two defect rows have now been re-pointed at the single surviving entry
// point, resolvePathsForHomeProfile. Read `git log -p` on this file for the
// record: the diff where their expected values move is the fix landing.
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
			resolve: func() (*Paths, error) { return resolvePathsForHomeProfile("", DefaultProfile) },
			want:    goldenDefault(invoker),
		},
		{
			name:    "resolveConfig/ResolveScheduler shape, target home, sync profile",
			resolve: func() (*Paths, error) { return resolvePathsForHomeProfile(target, DefaultProfile) },
			want:    goldenDefault(target),
		},
		{
			name:    "resolveConfig/ResolveScheduler shape, target home, peer profile",
			resolve: func() (*Paths, error) { return resolvePathsForHomeProfile(target, PeerProfile) },
			want:    goldenPeer(target),
		},
		{
			// The fourth cell of the matrix. It sits behind no current call site,
			// and it is the only row that keeps "the home came from the argument"
			// separable from "the profile came from the argument" once both defect
			// rows collapse onto the same call.
			name:    "resolveConfig/ResolveScheduler shape, empty home, peer profile",
			resolve: func() (*Paths, error) { return resolvePathsForHomeProfile("", PeerProfile) },
			want:    goldenPeer(invoker),
		},
		// The two rows below and the "target home, peer profile" row above now
		// call the same resolver with the same arguments and expect the same
		// layout. That convergence IS the fix: three call shapes that used to
		// disagree became one. It is not duplication to be cleaned up - deleting
		// the duplicates would delete the record of the change, so the rows keep
		// distinct names and `-v` still identifies which is which.
		{
			// RE-POINTED. BUG-27 (internal/cli/peer_status.go:120) resolved by
			// profile alone, so the --home target was dropped and the layout
			// landed under the INVOKING user's home; this row expected invoker.
			// The profile-only resolver is gone, the site now resolves through
			// the single entry point with the target home as an argument, and the
			// expectation moved from invoker to target.
			name:    "BUG-27 re-pointed, target home, peer profile",
			resolve: func() (*Paths, error) { return resolvePathsForHomeProfile(target, PeerProfile) },
			want:    goldenPeer(target),
		},
		{
			// RE-POINTED. BUG-28 (internal/cli/status_cmd.go:249) resolved by home
			// alone, so the caller's peer profile was dropped and the layout
			// carried the default profile and the default scheduler identities;
			// this row expected goldenDefault. The profile is now an argument
			// rather than an assumption, so Profile moved from "sync" to "peer"
			// and the launchd/systemd names from the default units to the
			// per-profile ones.
			name:    "BUG-28 re-pointed, target home, peer profile",
			resolve: func() (*Paths, error) { return resolvePathsForHomeProfile(target, PeerProfile) },
			want:    goldenPeer(target),
		},
		{
			// The zero-literal fallback that used to live at
			// internal/cli/peer_status.go:124. It no longer exists in production:
			// every field is empty, yet the derived scheduler identity is still
			// the default profile's name under an empty directory. This row
			// stands as the standing reason ResolveScheduler returns a named
			// error instead of degrading to a zero value.
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
