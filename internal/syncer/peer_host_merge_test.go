package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	// The file holds tokens (0600 on both Macs): the merge write keeps it.
	for _, home := range []string{localHome, peerHome} {
		if err := os.Chmod(filepath.Join(home, ".claude.json"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
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
	if li.Mode().Perm() != 0o600 {
		t.Fatalf("merge left mode %v", li.Mode().Perm())
	}
	pi, _ := os.Stat(filepath.Join(peerHome, ".claude.json"))
	if !li.ModTime().Equal(pi.ModTime()) {
		t.Fatalf("copies left with different mtimes: %v / %v", li.ModTime(), pi.ModTime())
	}
}

// #181: host_merge runs only in a two-way run; a one-directional plan must
// not claim the merge and must warn about what newest-wins drops.
func TestPeerSync_OneWayPlanDoesNotClaimTheMerge(t *testing.T) {
	cfg, _, _ := claudeJSONFixture(t,
		`{"mcpServers":{"a":{},"mine":{}}}`,
		`{"mcpServers":{"a":{},"kimi-cu":{}}}`)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
	res, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(true), Probe: peerScheduleRunner(false), DryRun: true, PullOnly: true, Itemize: true})
	if err != nil {
		t.Fatal(err)
	}
	it := findItem(t, res.Plan, ".claude.json")
	if strings.Contains(it.Reason, "merged") || !strings.Contains(it.Warning, "mine") || !strings.Contains(it.Warning, "host_merge runs only in a two-way sync") {
		t.Fatalf("item = %+v", it)
	}
}

// The merge write keeps a token file's mode and refuses to replace a symlink.
func TestMergePeerHostFilesKeepsModeAndSymlinks(t *testing.T) {
	cfg, localHome, _ := claudeJSONFixture(t, `{"mcpServers":{"mine":{}}}`, `{"mcpServers":{"kimi-cu":{}}}`)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
	path := filepath.Join(localHome, ".claude.json")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	r := peerScheduleRunner(false)
	if merged, err := mergePeerHostFiles(context.Background(), r, r, cfg); err != nil || len(merged) != 1 {
		t.Fatalf("merged %v, %v", merged, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}

	cfg, localHome, _ = claudeJSONFixture(t, `{"mcpServers":{"mine":{}}}`, `{"mcpServers":{"kimi-cu":{}}}`)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
	path = filepath.Join(localHome, ".claude.json")
	real := filepath.Join(localHome, "real.json")
	if err := os.Rename(path, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, path); err != nil {
		t.Fatal(err)
	}
	if _, err := mergePeerHostFiles(context.Background(), r, r, cfg); err == nil {
		t.Fatal("merge replaced a symlink")
	}
	if info, _ := os.Lstat(path); info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink gone")
	}
}

// Same-second writes: the peer's mtime is read to the second, so a copy the
// pass will pull can look older here. The merge still writes both sides.
func TestMergePeerHostFilesWritesBothSidesWhateverIsNewer(t *testing.T) {
	cfg, localHome, peerHome := claudeJSONFixture(t, `{"mcpServers":{"a":{},"mine":{}}}`, `{"mcpServers":{"a":{}}}`)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
	sec := peerHomeFixedTime.Truncate(time.Second)
	if err := os.Chtimes(filepath.Join(localHome, ".claude.json"), sec.Add(300e6), sec.Add(300e6)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(peerHome, ".claude.json"), sec.Add(700e6), sec.Add(700e6)); err != nil {
		t.Fatal(err)
	}
	r := peerScheduleRunner(false)
	if merged, err := mergePeerHostFiles(context.Background(), r, r, cfg); err != nil || len(merged) != 1 {
		t.Fatalf("merged %v, %v", merged, err)
	}
	if body := string(gitStateFileBytes(t, filepath.Join(peerHome, ".claude.json"))); !strings.Contains(body, `"mine"`) {
		t.Fatalf("the peer copy lacks the local entry: %s", body)
	}
}

// A host_merge file under a tracked host entry belongs to the tracked pass.
func TestMergePeerHostFilesSkipsTrackedPaths(t *testing.T) {
	cfg, _, peerHome := claudeJSONFixture(t, `{"mcpServers":{"mine":{}}}`, `{"mcpServers":{"kimi-cu":{}}}`)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
	if err := os.WriteFile(PeerHomeTrackedFile(cfg.LocalPaths), []byte("mem\n.claude.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := peerScheduleRunner(false)
	if merged, err := mergePeerHostFiles(context.Background(), r, r, cfg); err != nil || len(merged) != 0 {
		t.Fatalf("merged %v, %v", merged, err)
	}
	if body := string(gitStateFileBytes(t, filepath.Join(peerHome, ".claude.json"))); strings.Contains(body, "mine") {
		t.Fatalf("a tracked file was merged: %s", body)
	}
}
