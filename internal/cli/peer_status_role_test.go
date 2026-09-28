package cli

import (
	"strings"
	"testing"
)

// #182 AC3: peer status states the role, and on the peer it does not show
// its own timestamps as the coordinator's activity.
func TestPeerStatusShowsTheRole(t *testing.T) {
	goldenSyncFixture(t) // owner golden-machine: this machine is the peer
	out, errOut, err := runDotForTest("peer", "status")
	if err != nil {
		t.Fatalf("peer status: %v\n%s", err, errOut)
	}
	for _, want := range []string{"peer; syncs run on the coordinator golden-machine", "Last run here", "while this Mac was the coordinator"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Last pull") {
		t.Errorf("the peer shows coordinator timestamps:\n%s", out)
	}
}
