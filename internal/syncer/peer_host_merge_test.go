package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeJSONKeysKeepsEveryEntryAndNumbers(t *testing.T) {
	newer, _ := decodeJSONObject([]byte(`{"mcpServers":{"a":{"v":2},"new":{}},"n":12345678901234567890,"other":"newer"}`))
	older, _ := decodeJSONObject([]byte(`{"mcpServers":{"a":{"v":1},"old":{}},"projects":{"/p":{}},"other":"older"}`))
	merged := mergeJSONKeys(newer, older, []string{"mcpServers", "projects"})
	body, err := encodeJSONObject(merged)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"new"`, `"old"`, `"v": 2`, `"/p"`, `"other": "newer"`, `12345678901234567890`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("merged lacks %s:\n%s", want, body)
		}
	}
	d := diffJSONKeys(older, newer, []string{"mcpServers"})
	if len(d) != 1 || d[0].String() != "mcpServers: peer only new; here only old; differ a" {
		t.Fatalf("diff = %v", d)
	}
}

func claudeJSONFixture(t *testing.T, local, peer string) (*Config, string, string) {
	cfg, localHome, peerHome := peerPlanFixture(t)
	writePeerHomeFile(t, localHome, ".claude.json", local, peerHomeFixedTime)
	writePeerHomeFile(t, peerHome, ".claude.json", peer, peerHomeFixedTime.Add(3600e9))
	return cfg, localHome, peerHome
}

func findItem(t *testing.T, p *PeerRunPlan, path string) PeerPlanItem {
	t.Helper()
	for _, it := range p.Items {
		if it.Path == path {
			return it
		}
	}
	t.Fatalf("no plan item for %s", path)
	return PeerPlanItem{}
}

// #181 AC1: the plan marks ~/.claude.json hot and shows key-level
// differences; without a merge policy it warns about what newest-wins drops.
func TestPeerDiff_MarksHotClaudeJSON(t *testing.T) {
	cfg, _, _ := claudeJSONFixture(t,
		`{"mcpServers":{"a":{},"mine":{}}}`,
		`{"mcpServers":{"a":{},"kimi-cu":{},"pencil":{}},"projects":{"/p":{}}}`)
	res, err := PeerDiff(context.Background(), PeerDiffOptions{Config: cfg, Probe: peerScheduleRunner(false), Itemize: true})
	if err != nil {
		t.Fatal(err)
	}
	it := findItem(t, res.Items, ".claude.json")
	keys := strings.Join(it.Keys, "\n")
	if !it.Hot || !strings.Contains(keys, "mcpServers: peer only kimi-cu, pencil; here only mine") || !strings.Contains(keys, "projects: peer only (whole key)") {
		t.Fatalf("item = %+v", it)
	}
	if !strings.Contains(it.Warning, "mine") {
		t.Fatalf("no newest-wins warning: %q", it.Warning)
	}
}

// #181 AC2: with a merge policy, MCP server entries from either side survive.
func TestPeerSync_HostMergeKeepsEntriesFromBothMacs(t *testing.T) {
	cfg, localHome, peerHome := claudeJSONFixture(t,
		`{"mcpServers":{"a":{},"mine":{}},"numStartups":12}`,
		`{"mcpServers":{"a":{},"kimi-cu":{},"pencil":{}},"numStartups":40}`)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers", "projects"}}
	var merged []string
	res, err := PeerSync(context.Background(), PeerSyncOptions{
		Config: cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false),
		Progress: func(e PeerEvent) {
			if e.Kind == PeerEventHostMerged {
				merged = append(merged, e.Path)
			}
		},
	})
	if err != nil || !res.Complete {
		t.Fatalf("PeerSync: %+v %v", res, err)
	}
	if len(merged) != 1 || merged[0] != ".claude.json" {
		t.Fatalf("merged = %v", merged)
	}
	for _, home := range []string{localHome, peerHome} {
		body := string(gitStateFileBytes(t, filepath.Join(home, ".claude.json")))
		for _, want := range []string{`"mine"`, `"kimi-cu"`, `"pencil"`, `"numStartups": 40`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s:\n%s", home, want, body)
			}
		}
	}
	li, _ := os.Stat(filepath.Join(localHome, ".claude.json"))
	pi, _ := os.Stat(filepath.Join(peerHome, ".claude.json"))
	if !li.ModTime().Equal(pi.ModTime()) {
		t.Fatalf("copies left with different mtimes: %v / %v", li.ModTime(), pi.ModTime())
	}
}
