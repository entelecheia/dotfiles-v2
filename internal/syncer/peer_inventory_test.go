// Moved out of the cli package with the inventory machinery it covers
// (plan 03-05, D-13). Assertions are unchanged.
package syncer

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"
)

func TestParsePeerRemoteInventoryAcceptsSpacesAndKorean(t *testing.T) {
	stdout := "@@12\t2026/08/10-12:34:56\t자료/한글 파일.txt\n" +
		"@@0\t2026/08/10-12:34:56\t자료/\n"
	got, err := parsePeerRemoteInventory(stdout, time.FixedZone("peer", 9*60*60), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	file, ok := got["자료/한글 파일.txt"]
	if !ok || !file.Present || file.FP.Size != 12 {
		t.Fatalf("inventory = %#v", got)
	}
}

func TestParsePeerRemoteInventoryRejectsMalformedOrUnsafeRecords(t *testing.T) {
	for _, stdout := range []string{
		"warning continuation\n",
		"@@1\t2026/08/10-12:34:56\t../escape.txt\n",
		"@@1\t2026/08/10-12:34:56\t/absolute.txt\n",
		"@@1\t2026/08/10-12:34:56\ta/../escape.txt\n",
		"@@-1\t2026/08/10-12:34:56\tbad.txt\n",
		"@@1\tbad-time\tbad.txt\n",
		"@@1\t2026/08/10-12:34:56\ttab\tname.txt\n",
		"@@1\t2026/08/10-12:34:56\tdup.txt\n@@1\t2026/08/10-12:34:56\tdup.txt\n",
	} {
		if _, err := parsePeerRemoteInventory(stdout, time.UTC, nil, false); err == nil {
			t.Errorf("accepted unsafe inventory %q", stdout)
		} else if !strings.Contains(err.Error(), "peer inventory") {
			t.Errorf("unexpected error %v", err)
		}
	}
}

func TestParsePeerRemoteInventoryIgnoresUnknownNonRegularButRejectsBaselineTransition(t *testing.T) {
	stdout := "skipping non-regular file \"links/current\"\n"
	if got, err := parsePeerRemoteInventory(stdout, time.UTC, nil, false); err != nil || len(got) != 0 {
		t.Fatalf("unknown symlink should be outside payload: got=%v err=%v", got, err)
	}
	baseline := map[string]Fingerprint{"links/current": {Size: 1}}
	if _, err := parsePeerRemoteInventory(stdout, time.UTC, baseline, false); err == nil || !strings.Contains(err.Error(), "non-regular on the peer") {
		t.Fatalf("baseline symlink transition error = %v", err)
	}
}

func TestParsePeerRemoteInventoryRequiresNFDWhenMigrationIsMarked(t *testing.T) {
	nfc := "@@1\t2026/08/10-12:34:56\t자료/카페.txt\n"
	if _, err := parsePeerRemoteInventory(nfc, time.UTC, nil, true); err == nil || !strings.Contains(err.Error(), "not NFD-normalized") {
		t.Fatalf("NFC inventory error = %v", err)
	}
	nfd := "@@1\t2026/08/10-12:34:56\t" + norm.NFD.String("자료/카페.txt") + "\n"
	if _, err := parsePeerRemoteInventory(nfd, time.UTC, nil, true); err != nil {
		t.Fatalf("NFD inventory rejected: %v", err)
	}
}

func TestValidateRemotePeerStatusRequiresSameCoordinatorAndPair(t *testing.T) {
	cfg := &Config{
		Owner:     "coordinator.local",
		LocalPath: "/Users/test/work/",
		Target:    Target{Kind: TargetSSH, Host: "peer", Path: "/Users/test/work"},
	}
	raw := `{"schemaVersion":1,"kind":"peer","profile":{"configured":true,"workspacePath":"/Users/test/work","owner":"coordinator","target":{"path":"/Users/test/work"}}}`
	if err := validateRemotePeerStatus(cfg, raw); err != nil {
		t.Fatalf("matching remote status rejected: %v", err)
	}
	for _, bad := range []string{
		`{"schemaVersion":1,"kind":"peer","profile":{"configured":true,"workspacePath":"/Users/test/work","owner":"other","target":{"path":"/Users/test/work"}}}`,
		`{"schemaVersion":1,"kind":"peer","profile":{"configured":true,"workspacePath":"/wrong","owner":"coordinator","target":{"path":"/Users/test/work"}}}`,
	} {
		if err := validateRemotePeerStatus(cfg, bad); err == nil {
			t.Fatalf("unsafe remote status accepted: %s", bad)
		}
	}
}

func TestRemotePeerCommandsResolveLocalAndHomebrewInstalls(t *testing.T) {
	for _, want := range []string{
		`$HOME/.local/bin/dot`,
		`/opt/homebrew/bin/dot`,
		`/usr/local/bin/dot`,
		`command -v dot`,
	} {
		if !strings.Contains(remotePeerStatusCommand, want) || !strings.Contains(remotePeerNormalizeCommand, want) {
			t.Fatalf("remote dot resolver missing %q", want)
		}
	}
}
