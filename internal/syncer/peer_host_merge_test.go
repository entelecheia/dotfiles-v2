package syncer

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
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

// A bad host_merge key stops a run before anything moves; a symlink on the
// peer is refused like one here; a file outside home-paths.txt is not
// merged (the plan's host scope is that list).
func TestHostMergeGuards(t *testing.T) {
	cfg, _, peerHome := claudeJSONFixture(t, `{"mcpServers":{"mine":{}}}`, `{"mcpServers":{"kimi-cu":{}}}`)
	cfg.HostMerge = map[string][]string{"~/.claude.json": {"mcpServers"}}
	if _, err := PeerDiff(context.Background(), PeerDiffOptions{Config: cfg, Probe: peerScheduleRunner(false), Itemize: true}); err == nil || !strings.Contains(err.Error(), "host_merge") {
		t.Fatalf("PeerDiff with a bad key: %v", err)
	}
	if _, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(true), Probe: peerScheduleRunner(false), DryRun: true}); err == nil || !strings.Contains(err.Error(), "host_merge") {
		t.Fatalf("PeerSync with a bad key: %v", err)
	}

	r := peerScheduleRunner(false)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}, ".other.json": {"mcpServers"}}
	writePeerHomeFile(t, cfg.HomeDir(), ".other.json", `{"mcpServers":{"a":{}}}`, peerHomeFixedTime)
	writePeerHomeFile(t, peerHome, ".other.json", `{"mcpServers":{"b":{}}}`, peerHomeFixedTime)
	merged, err := mergePeerHostFiles(context.Background(), r, r, cfg)
	if err != nil || !slices.Equal(merged, []string{".claude.json"}) {
		t.Fatalf("merged %v, %v; want only the listed .claude.json", merged, err)
	}

	peerFile := filepath.Join(peerHome, ".claude.json")
	if err := os.Rename(peerFile, peerFile+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(peerFile+".real", peerFile); err != nil {
		t.Fatal(err)
	}
	if _, err := mergePeerHostFiles(context.Background(), r, r, cfg); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("a peer symlink was not refused: %v", err)
	}
	if info, _ := os.Lstat(peerFile); info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the peer symlink was replaced")
	}
}

// #180 AC2 for host_merge: the dry-run plan lists exactly the merges the
// real run performs (its merge events), and a file it cannot merge stops the
// run before anything moves. Both come from planHostMerges.
func TestHostMergePlanMatchesWhatTheRunDid(t *testing.T) {
	planned := func(t *testing.T, cfg *Config) []string {
		t.Helper()
		res, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(true), Probe: peerScheduleRunner(false), DryRun: true, Itemize: true})
		if err != nil {
			t.Fatalf("dry run: %v", err)
		}
		var out []string
		for _, it := range res.Plan.Items {
			if it.Action == "merge" {
				out = append(out, it.Path)
			}
		}
		return out
	}
	did := func(t *testing.T, cfg *Config) []string {
		t.Helper()
		var out []string
		_, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false),
			Progress: func(e PeerEvent) {
				if e.Kind == PeerEventHostMerged {
					out = append(out, e.Path)
				}
			}})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		return out
	}
	for _, tc := range []struct {
		name        string
		local, peer string
		setup       func(t *testing.T, cfg *Config, localHome, peerHome string)
		want        []string
	}{
		{"entries on both sides merge", `{"mcpServers":{"mine":{}}}`, `{"mcpServers":{"kimi-cu":{}}}`, nil, []string{".claude.json"}},
		{"equal in substance needs no merge", `{"mcpServers":{"a":{}}}`, `{ "mcpServers": { "a": {} } }`, nil, nil},
		{"a file the additive pass does not move", `{"mcpServers":{"a":{}}}`, `{"mcpServers":{"a":{}}}`, func(t *testing.T, cfg *Config, localHome, peerHome string) {
			// .claude holds a tracked entry, so the additive pass drops it.
			cfg.HostMerge = map[string][]string{".claude/settings.json": {"permissions"}}
			writePeerHomeFile(t, localHome, ".claude/settings.json", `{"permissions":{"a":1}}`, peerHomeFixedTime)
			writePeerHomeFile(t, peerHome, ".claude/settings.json", `{"permissions":{"b":1}}`, peerHomeFixedTime)
			if err := os.WriteFile(PeerHomePathsFile(cfg.LocalPaths), []byte(".claude.json\n.claude\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(PeerHomeTrackedFile(cfg.LocalPaths), []byte("mem\n.claude/skills/own\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, localHome, peerHome := claudeJSONFixture(t, tc.local, tc.peer)
			cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
			if tc.setup != nil {
				tc.setup(t, cfg, localHome, peerHome)
			}
			plan := planned(t, cfg)
			ran := did(t, cfg)
			if !slices.Equal(plan, tc.want) || !slices.Equal(ran, tc.want) {
				t.Fatalf("planned %v, ran %v, want %v", plan, ran, tc.want)
			}
		})
	}

	t.Run("a refused file stops the run before anything moves", func(t *testing.T) {
		cfg, localHome, _ := claudeJSONFixture(t, `{"mcpServers":{"mine":{}}}`, `{"mcpServers":{"kimi-cu":{}}}`)
		cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
		path := filepath.Join(localHome, ".claude.json")
		if err := os.Rename(path, path+".real"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path+".real", path); err != nil {
			t.Fatal(err)
		}
		for _, dry := range []bool{true, false} {
			if _, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(dry), Probe: peerScheduleRunner(false), DryRun: dry}); err == nil || !strings.Contains(err.Error(), "nothing was transferred") {
				t.Fatalf("dry=%v: err = %v", dry, err)
			}
		}
		if _, err := os.Stat(filepath.Join(strings.TrimRight(cfg.LocalPath, "/"), "peer-only.txt")); !os.IsNotExist(err) {
			t.Fatalf("the workspace pass ran before the refusal: %v", err)
		}
	})
}

// An app saving ~/.claude.json while the merge step reads it: the merge is
// decided again from the new copy, so the peer-only entries survive on both
// machines. One that keeps saving stops the run before the additive pass,
// whose newest-wins push would otherwise drop them.
func TestHostMergeSurvivesALocalSaveDuringTheMerge(t *testing.T) {
	for _, tc := range []struct {
		name    string
		saveOn  string // which peer mtime reads trigger a local save: "2" or "2+"
		wantErr bool
	}{{"saved once", "2", false}, {"keeps saving", "2+", true}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, localHome, peerHome := claudeJSONFixture(t,
				`{"mcpServers":{"a":{},"mine":{}}}`,
				`{"mcpServers":{"a":{},"kimi-cu":{},"pencil":{}}}`)
			cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers", "projects"}}
			real, err := osexec.LookPath("ssh")
			if err != nil {
				t.Fatal(err)
			}
			// Wrap the fake ssh: the Nth peer-mtime read (the merge step's
			// decision; the first is the preflight's) saves the local file.
			bin, count := t.TempDir(), filepath.Join(t.TempDir(), "count")
			local := filepath.Join(localHome, ".claude.json")
			cond := `[ "$n" -eq 2 ]`
			if tc.saveOn == "2+" {
				cond = `[ "$n" -ge 2 ]`
			}
			writeStub(t, filepath.Join(bin, "ssh"), "#!/bin/sh\n"+
				"case \"$*\" in *'stat -c %Y'*)\n"+
				"  n=$(( $(cat '"+count+"' 2>/dev/null || echo 0) + 1 )); echo $n > '"+count+"'\n"+
				"  if "+cond+"; then printf '{\"mcpServers\":{\"a\":{},\"mine\":{}},\"numStartups\":%s}' \"$n\" > '"+local+"'; fi ;;\n"+
				"esac\n"+
				"exec '"+real+"' \"$@\"\n")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

			_, err = PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false)})
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			for _, home := range []string{localHome, peerHome} {
				body := string(gitStateFileBytes(t, filepath.Join(home, ".claude.json")))
				if !tc.wantErr && (!strings.Contains(body, "kimi-cu") || !strings.Contains(body, "mine")) {
					t.Errorf("%s lost entries: %s", home, body)
				}
			}
			if peer := string(gitStateFileBytes(t, filepath.Join(peerHome, ".claude.json"))); !strings.Contains(peer, "kimi-cu") {
				t.Errorf("the peer's own entries are gone: %s", peer)
			}
		})
	}
}

// host_merge never writes where BOUNDARIES forbids: skill roots and Maru's
// trees, in any letter case (APFS is case-insensitive).
func TestValidateHostMergeRefusesForbiddenRoots(t *testing.T) {
	for _, rel := range []string{".claude/skills/x/manifest.json", ".Claude/Skills/x.json", ".maru/skills/registry.json", ".maru/env/x.json"} {
		if err := validateHostMerge(map[string][]string{rel: {"k"}}); err == nil {
			t.Errorf("%s accepted", rel)
		}
	}
	if err := validateHostMerge(map[string][]string{".claude.json": {"mcpServers"}}); err != nil {
		t.Fatal(err)
	}
}
