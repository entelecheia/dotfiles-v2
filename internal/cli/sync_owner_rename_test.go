package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

func TestSyncOwnerRenameGuards(t *testing.T) {
	f := newSyncCLIFixture(t)
	paths := syncer.ResolveLocalPaths(f.local)
	if err := syncer.SaveLocalConfig(paths, &syncer.LocalConfig{Target: "local:" + f.mirror, Owner: "other-mac", Propagation: syncer.DefaultPropagationPolicy()}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"sync", "owner", "--local-only"}, "--local-only only applies to --rename"},
		{[]string{"sync", "owner", "--rename", "--set", "x", "a", "b"}, "none of the others can be"},
		{[]string{"sync", "owner", "--rename", "other-mac", "another-mac"}, "run --rename on the machine being renamed"},
	} {
		if _, _, err := runDotForTest(tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
	if got, _, _ := syncer.LoadLocalConfig(paths); got.Owner != "other-mac" {
		t.Fatalf("a refused rename wrote %+v", got)
	}

	// --local-only on the peer side (the plumbing) renames, --dry-run first.
	if _, _, err := runDotForTest("--dry-run", "sync", "owner", "--rename", "--local-only", "other-mac", "renamed-mac"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := syncer.LoadLocalConfig(paths); got.Owner != "other-mac" {
		t.Fatalf("--dry-run wrote %+v", got)
	}
	if _, _, err := runDotForTest("sync", "owner", "--rename", "--local-only", "other-mac", "renamed-mac"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := syncer.LoadLocalConfig(paths); got.Owner != "renamed-mac" || len(got.OwnerAliases) != 1 {
		t.Fatalf("after --local-only rename: %+v", got)
	}
}

// Without the peer's answer, only a Mac that still answers to the old name
// may rename: after the host rename it could be the other Mac claiming the
// owner (round-3 review).
func TestSyncOwnerRenameFailsClosedWithoutThePeer(t *testing.T) {
	f := newSyncCLIFixture(t)
	bin := t.TempDir()
	writeCLITestFile(t, filepath.Join(bin, "ssh"), "#!/bin/sh\nexit 255\n")
	if err := os.Chmod(filepath.Join(bin, "ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	self := syncer.PreferredMachineName()
	if self == "" {
		t.Skip("no machine name")
	}
	for _, profile := range []string{"sync", "peer"} {
		writeCLITestFile(t, filepath.Join(f.local, ".dotfiles", profile, "config.yaml"),
			"target: ssh:asleep-peer:/remote/work\nowner: coordinator-mac\npropagation:\n  create: true\n  update: true\n  delete: true\n")
	}
	_, _, err := runDotForTest("sync", "owner", "--rename", "coordinator-mac", self)
	if err == nil || !strings.Contains(err.Error(), "could not confirm") {
		t.Fatalf("err = %v, want a fail-closed refusal", err)
	}
	local, _, _ := syncer.LoadLocalConfig(syncer.ResolveLocalPathsForProfile(f.local, "peer"))
	if local.Owner != "coordinator-mac" {
		t.Fatalf("a refused rename wrote %+v", local)
	}
	// This Mac owns the profiles, but the new name is the peer's target.
	writeCLITestFile(t, filepath.Join(f.local, ".dotfiles", "peer", "config.yaml"),
		"target: ssh:asleep-peer:/remote/work\nowner: "+self+"\npropagation:\n  create: true\n  update: true\n  delete: true\n")
	if _, _, err := runDotForTest("sync", "owner", "--rename", self, "asleep-peer"); err == nil || !strings.Contains(err.Error(), "peer target") {
		t.Fatalf("renaming to the peer target's name: %v", err)
	}
}
