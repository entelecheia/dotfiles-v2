package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

// #182 AC3: peer status states the role, and on the peer it does not show
// its own timestamps as the coordinator's activity.
func TestPeerStatusShowsTheRole(t *testing.T) {
	goldenSyncFixture(t) // owner golden-machine: this machine is the peer
	out, errOut, err := runDotForTest("peer", "status")
	if err != nil {
		t.Fatalf("peer status: %v\n%s", err, errOut)
	}
	for _, want := range []string{"peer; syncs run on the coordinator golden-machine", "Last run here:          (never)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Last pull") {
		t.Errorf("the peer shows coordinator timestamps:\n%s", out)
	}
}

// #182 AC1: the other Mac's doctor runs exactly `dot peer doctor --self` and
// reads JSON back; an unknown flag there would silently drop every
// both-machines check.
func TestPeerDoctorSelfPrintsFacts(t *testing.T) {
	goldenSyncFixture(t)
	out, errOut, err := runDotForTest("peer", "doctor", "--self")
	if err != nil {
		t.Fatalf("peer doctor --self: %v\n%s", err, errOut)
	}
	var facts syncer.PeerSideFacts
	if err := json.Unmarshal([]byte(out), &facts); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if facts.Owner != "golden-machine" || facts.Coordinator {
		t.Fatalf("facts = %+v", facts)
	}
}
