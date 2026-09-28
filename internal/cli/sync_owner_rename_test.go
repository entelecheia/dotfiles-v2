package cli

import (
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
