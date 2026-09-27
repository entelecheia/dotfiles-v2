package cli

import (
	"testing"
)

// The peer git command tree: `dot peer git status` carries --json (the GUARD-04
// golden matrix walks the live tree and would fail on an unregistered
// surface), `dot peer git realign` defaults to a preview and mutates only
// under --apply.
func TestPeerGitCommandWiring(t *testing.T) {
	peer := newPeerCmd()
	var gitCmdFound bool
	for _, sub := range peer.Commands() {
		if sub.Name() != "git" {
			continue
		}
		gitCmdFound = true
		var status, realign bool
		for _, leaf := range sub.Commands() {
			switch leaf.Name() {
			case "status":
				status = true
				if leaf.Flags().Lookup("json") == nil {
					t.Error("peer git status lacks --json")
				}
			case "realign":
				realign = true
				if leaf.Flags().Lookup("apply") == nil {
					t.Error("peer git realign lacks --apply")
				}
			}
		}
		if !status || !realign {
			t.Errorf("peer git subcommands: status=%v realign=%v", status, realign)
		}
	}
	if !gitCmdFound {
		t.Fatal("dot peer has no git subcommand")
	}
}
