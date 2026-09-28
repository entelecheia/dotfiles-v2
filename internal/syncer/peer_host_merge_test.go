package syncer

import (
	"context"
	"fmt"
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

// #181 AC2 in a one-way run: host_merge merges only in a two-way run, so a
// --pull-only or --push-only run holds a file present on both machines
// instead of letting the newer copy drop the other's entries on both, also
// when the other copy appears only during the run (late), and says so.
func TestPeerSync_OneWayRunHoldsHostMergeFiles(t *testing.T) {
	for _, tc := range []struct {
		name               string
		pushOnly, pullOnly bool
		direction          string
		late               bool
	}{
		{"pull-only, peer newer", false, true, "pull", false},
		{"push-only, local newer", true, false, "push", false},
		{"pull-only, the peer's copy appears late", false, true, "pull", true},
		{"push-only, the local copy appears late", true, false, "push", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := `{"mcpServers":{"a":{},"mine":{}}}`
			cfg, localHome, peerHome := claudeJSONFixture(t, local, `{"mcpServers":{"a":{},"kimi-cu":{}}}`)
			if tc.pushOnly {
				writePeerHomeFile(t, localHome, ".claude.json", local, peerHomeFixedTime.Add(7200e9))
			}
			cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
			opts := PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(true), Probe: peerScheduleRunner(false), DryRun: true, PushOnly: tc.pushOnly, PullOnly: tc.pullOnly, Itemize: true}
			if !tc.late {
				res, err := PeerSync(context.Background(), opts)
				if err != nil {
					t.Fatal(err)
				}
				it := findItem(t, res.Plan, ".claude.json")
				if it.Action != "update" || it.Direction != tc.direction || !strings.HasPrefix(it.Reason, "held: host_merge") || it.Local == nil || it.Peer == nil {
					t.Fatalf("item = %+v", it)
				}
			} else {
				// The receiver's copy is there from the start; the sender's
				// (the newer) appears once the workspace pass runs, after the
				// decision, as an app's first save would.
				sender := filepath.Join(peerHome, ".claude.json")
				if tc.pushOnly {
					sender = filepath.Join(localHome, ".claude.json")
				}
				if err := os.Rename(sender, sender+".later"); err != nil {
					t.Fatal(err)
				}
				real, err := osexec.LookPath("ssh")
				if err != nil {
					t.Fatal(err)
				}
				bin, done := t.TempDir(), filepath.Join(t.TempDir(), "done")
				writeStub(t, filepath.Join(bin, "ssh"), "#!/bin/sh\n"+
					"case \"$*\" in *'rsync --server'*)\n"+
					"  if [ ! -f '"+done+"' ]; then mv '"+sender+".later' '"+sender+"'; touch '"+done+"'; fi ;;\n"+
					"esac\n"+
					"exec '"+real+"' \"$@\"\n")
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			var held []string
			opts.Runner, opts.DryRun, opts.Itemize = peerScheduleRunner(false), false, false
			opts.Progress = func(e PeerEvent) {
				if e.Kind == PeerEventHostMergeHeld {
					held = append(held, e.Path)
				}
			}
			if _, err := PeerSync(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			for home, want := range map[string]string{localHome: `"mine"`, peerHome: `"kimi-cu"`} {
				if body := string(gitStateFileBytes(t, filepath.Join(home, ".claude.json"))); !strings.Contains(body, want) {
					t.Errorf("%s lost %s: %s", home, want, body)
				}
			}
			if !tc.late && !slices.Equal(held, []string{".claude.json"}) {
				t.Errorf("held events = %v", held)
			}
		})
	}

	// A one-way run whose sending copy is the older one moves nothing, so
	// the plan lists nothing for it.
	cfg, _, _ := claudeJSONFixture(t, `{"mcpServers":{"mine":{}}}`, `{"mcpServers":{"kimi-cu":{}}}`)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
	res, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(true), Probe: peerScheduleRunner(false), DryRun: true, PushOnly: true, Itemize: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range res.Plan.Items {
		if it.Path == ".claude.json" {
			t.Fatalf("an older sender's copy is listed: %+v", it)
		}
	}
}

// A host_merge file on one Mac only is created on the other, in the
// directions the run moves, and listed as a create; a copy that is not a
// regular file here is no refusal while the peer has none (#194 round 9).
func TestPeerSync_HostMergeFileOnOneMacIsCreatedOnTheOther(t *testing.T) {
	for _, pullOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("pullOnly=%v", pullOnly), func(t *testing.T) {
			cfg, localHome, peerHome := claudeJSONFixture(t, `{"mcpServers":{"mine":{}}}`, `{"mcpServers":{"kimi-cu":{}}}`)
			cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
			from, to, direction := localHome, peerHome, "push"
			if pullOnly {
				from, to, direction = peerHome, localHome, "pull"
			}
			if err := os.Remove(filepath.Join(to, ".claude.json")); err != nil {
				t.Fatal(err)
			}
			opts := PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(true), Probe: peerScheduleRunner(false), DryRun: true, PullOnly: pullOnly, Itemize: true}
			res, err := PeerSync(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if it := findItem(t, res.Plan, ".claude.json"); it.Action != "create" || it.Direction != direction {
				t.Fatalf("item = %+v", it)
			}
			opts.Runner, opts.DryRun, opts.Itemize = peerScheduleRunner(false), false, false
			if _, err := PeerSync(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			want := string(gitStateFileBytes(t, filepath.Join(from, ".claude.json")))
			if got := string(gitStateFileBytes(t, filepath.Join(to, ".claude.json"))); got != want {
				t.Fatalf("created copy = %q, want %q", got, want)
			}
		})
	}

	cfg, localHome, peerHome := claudeJSONFixture(t, `{"mcpServers":{"mine":{}}}`, `{"mcpServers":{"kimi-cu":{}}}`)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
	if err := os.Remove(filepath.Join(peerHome, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(localHome, ".claude.json")
	if err := os.Rename(path, path+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".real", path); err != nil {
		t.Fatal(err)
	}
	if merges, err := planHostMerges(context.Background(), peerScheduleRunner(false), cfg); err != nil || len(merges) != 0 {
		t.Fatalf("a symlink with no peer copy was decided: %+v %v", merges, err)
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
		// The plan lists it as an item with an action, like every item.
		res, _ := PeerDiff(context.Background(), PeerDiffOptions{Config: cfg, Probe: peerScheduleRunner(false), Itemize: true})
		if it := findItem(t, res.Items, ".claude.json"); it.Action != "conflict" || it.Direction != "both" || !strings.Contains(it.Reason, "cannot merge") {
			t.Fatalf("refused item = %+v", it)
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

// The peer's app saving its copy while the merge step works on it: the
// write re-reads the peer's copy and decides again, so its new entry is not
// overwritten by the merged push.
func TestHostMergeSurvivesAPeerSaveDuringTheMerge(t *testing.T) {
	cfg, localHome, peerHome := claudeJSONFixture(t,
		`{"mcpServers":{"a":{},"mine":{}}}`,
		`{"mcpServers":{"a":{},"kimi-cu":{}}}`)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
	real, err := osexec.LookPath("ssh")
	if err != nil {
		t.Fatal(err)
	}
	// The second peer-mtime read (the merge step's decision) saves the
	// peer's file after the decision read it.
	bin, count := t.TempDir(), filepath.Join(t.TempDir(), "count")
	peer := filepath.Join(peerHome, ".claude.json")
	writeStub(t, filepath.Join(bin, "ssh"), "#!/bin/sh\n"+
		"case \"$*\" in *'stat -c %Y'*)\n"+
		"  n=$(( $(cat '"+count+"' 2>/dev/null || echo 0) + 1 )); echo $n > '"+count+"'\n"+
		"  if [ \"$n\" -eq 2 ]; then printf '{\"mcpServers\":{\"a\":{},\"kimi-cu\":{},\"late\":{}}}' > '"+peer+"'; fi ;;\n"+
		"esac\n"+
		"exec '"+real+"' \"$@\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false)}); err != nil {
		t.Fatal(err)
	}
	for _, home := range []string{localHome, peerHome} {
		body := string(gitStateFileBytes(t, filepath.Join(home, ".claude.json")))
		for _, want := range []string{`"mine"`, `"kimi-cu"`, `"late"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s: %s", home, want, body)
			}
		}
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
	// No keys would exclude the file from newest-wins and merge nothing: it
	// would never move. A line break would split the create-only list.
	for _, m := range []map[string][]string{{".claude.json": {}}, {".claude.json": nil}, {".a\n.json": {"k"}}} {
		if err := validateHostMerge(m); err == nil {
			t.Errorf("%q accepted", m)
		}
	}
	if err := validateHostMerge(map[string][]string{".claude.json": {"mcpServers"}}); err != nil {
		t.Fatal(err)
	}
}

// host_merge writes regular files only, and the create-only pass copies
// nothing else: a lone symlink on either Mac stays where it is, the plan
// lists nothing for it, and the next run is not refused. A directory on
// both is refused like a symlink (#194 round 10).
func TestHostMergeCreatesRegularFilesOnly(t *testing.T) {
	for _, onPeer := range []bool{false, true} {
		t.Run(fmt.Sprintf("symlink on peer=%v", onPeer), func(t *testing.T) {
			cfg, localHome, peerHome := claudeJSONFixture(t, `{"mcpServers":{"mine":{}}}`, `{"mcpServers":{"kimi-cu":{}}}`)
			cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
			from, to := localHome, peerHome
			if onPeer {
				from, to = peerHome, localHome
			}
			if err := os.Remove(filepath.Join(to, ".claude.json")); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(from, ".claude.json")
			if err := os.Rename(link, link+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(link+".real", link); err != nil {
				t.Fatal(err)
			}
			res, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(true), Probe: peerScheduleRunner(false), DryRun: true, Itemize: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range res.Plan.Items {
				if it.Path == ".claude.json" {
					t.Fatalf("the plan lists the lone symlink: %+v", it)
				}
			}
			for run := 0; run < 2; run++ {
				if _, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false)}); err != nil {
					t.Fatalf("run %d: %v", run, err)
				}
			}
			if _, err := os.Lstat(filepath.Join(to, ".claude.json")); !os.IsNotExist(err) {
				t.Fatalf("the symlink was copied: %v", err)
			}
		})
	}

	cfg, localHome, peerHome := claudeJSONFixture(t, `{}`, `{}`)
	cfg.HostMerge = map[string][]string{".cfg": {"k"}}
	if err := os.WriteFile(PeerHomePathsFile(cfg.LocalPaths), []byte(".cfg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, home := range []string{localHome, peerHome} {
		writePeerHomeFile(t, home, ".cfg/a.json", `{}`, peerHomeFixedTime)
	}
	merges, err := planHostMerges(context.Background(), peerScheduleRunner(false), cfg)
	if err != nil || len(merges) != 1 || !strings.Contains(merges[0].refused, "not a regular file") {
		t.Fatalf("a directory on both was not refused: %+v %v", merges, err)
	}
}

// A stale save after the merge (an app writing its in-memory copy) must not
// reach the other Mac through the additive pass: the merged file is not
// newest-wins input any more, so the peer keeps the union and the next run
// restores this Mac.
func TestHostMergeSurvivesAStaleSaveAfterTheMerge(t *testing.T) {
	// late: the peer's copy appears only after the preflight decided, so
	// the merge step's own decision must keep it out of newest-wins.
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late=%v", late), func(t *testing.T) {
			cfg, localHome, peerHome := claudeJSONFixture(t,
				`{"mcpServers":{"a":{},"mine":{}}}`,
				`{"mcpServers":{"a":{},"kimi-cu":{},"pencil":{}}}`)
			cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers", "projects"}}
			real, err := osexec.LookPath("ssh")
			if err != nil {
				t.Fatal(err)
			}
			peerFile := filepath.Join(peerHome, ".claude.json")
			appear := ""
			if late {
				if err := os.Rename(peerFile, peerFile+".later"); err != nil {
					t.Fatal(err)
				}
				// The second read of the peer's copy (the merge step's) finds it.
				count := filepath.Join(t.TempDir(), "reads")
				appear = "case \"$*\" in *__dot_absent__*)\n" +
					"  n=$(( $(cat '" + count + "' 2>/dev/null || echo 0) + 1 )); echo $n > '" + count + "'\n" +
					"  if [ \"$n\" -eq 2 ]; then mv '" + peerFile + ".later' '" + peerFile + "'; fi ;;\n" +
					"esac\n"
			}
			// Once the merge has written this Mac (the file holds the peer's
			// entry), the next rsync starts with a stale copy saved here,
			// clearly newer than the merge (an app saves seconds later; POSIX
			// touch -t).
			bin, done := t.TempDir(), filepath.Join(t.TempDir(), "done")
			local := filepath.Join(localHome, ".claude.json")
			later := time.Now().Add(time.Hour).Format("200601021504.05")
			writeStub(t, filepath.Join(bin, "ssh"), "#!/bin/sh\n"+appear+
				"case \"$*\" in *'rsync --server'*)\n"+
				"  if [ ! -f '"+done+"' ] && grep -q kimi-cu '"+local+"'; then\n"+
				"    printf '{\"mcpServers\":{\"a\":{},\"mine\":{}},\"numStartups\":99}' > '"+local+"'; touch -t "+later+" '"+local+"'; touch '"+done+"'\n"+
				"  fi ;;\n"+
				"esac\n"+
				"exec '"+real+"' \"$@\"\n")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

			run := func() {
				t.Helper()
				if _, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false)}); err != nil {
					t.Fatal(err)
				}
			}
			run()
			if _, err := os.Stat(done); err != nil {
				t.Fatal("the stale save never happened; the test proves nothing")
			}
			if peer := string(gitStateFileBytes(t, peerFile)); !strings.Contains(peer, "kimi-cu") || !strings.Contains(peer, "pencil") || !strings.Contains(peer, "mine") {
				t.Fatalf("the stale save reached the peer: %s", peer)
			}
			run()
			for _, home := range []string{localHome, peerHome} {
				body := string(gitStateFileBytes(t, filepath.Join(home, ".claude.json")))
				for _, want := range []string{"kimi-cu", "pencil", "mine"} {
					if !strings.Contains(body, want) {
						t.Errorf("%s lacks %s after the next run: %s", home, want, body)
					}
				}
			}
		})
	}
}

// Copies equal at the merge step are still host_merge's for the rest of
// the run: a stale save during the additive pass must not reach the other
// Mac (it stays here; the next two-way run's merge restores it).
func TestHostMergeKeepsEqualCopiesOutOfNewestWins(t *testing.T) {
	full := `{"mcpServers":{"a":{},"mine":{},"kimi-cu":{}}}`
	cfg, localHome, peerHome := claudeJSONFixture(t, full, full)
	cfg.HostMerge = map[string][]string{".claude.json": {"mcpServers"}}
	real, err := osexec.LookPath("ssh")
	if err != nil {
		t.Fatal(err)
	}
	// After the merge step's read of the peer copy (the second; the first is
	// the preflight's), the next rsync starts with a stale save here.
	bin, dir := t.TempDir(), t.TempDir()
	count, done := filepath.Join(dir, "reads"), filepath.Join(dir, "done")
	local := filepath.Join(localHome, ".claude.json")
	later := time.Now().Add(2 * time.Hour).Format("200601021504.05")
	writeStub(t, filepath.Join(bin, "ssh"), "#!/bin/sh\n"+
		"case \"$*\" in\n"+
		"  *__dot_absent__*) echo x >> '"+count+"' ;;\n"+
		"  *'rsync --server'*)\n"+
		"    if [ ! -f '"+done+"' ] && [ \"$(wc -l < '"+count+"' 2>/dev/null || echo 0)\" -ge 2 ]; then\n"+
		"      printf '{\"mcpServers\":{\"a\":{},\"mine\":{}},\"numStartups\":99}' > '"+local+"'; touch -t "+later+" '"+local+"'; touch '"+done+"'\n"+
		"    fi ;;\n"+
		"esac\n"+
		"exec '"+real+"' \"$@\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	run := func() {
		t.Helper()
		if _, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false)}); err != nil {
			t.Fatal(err)
		}
	}
	run()
	if _, err := os.Stat(done); err != nil {
		t.Fatal("the stale save never happened; the test proves nothing")
	}
	if peer := string(gitStateFileBytes(t, filepath.Join(peerHome, ".claude.json"))); !strings.Contains(peer, "kimi-cu") {
		t.Fatalf("the stale save reached the peer: %s", peer)
	}
	run()
	if body := string(gitStateFileBytes(t, local)); !strings.Contains(body, "kimi-cu") {
		t.Fatalf("the next run did not restore this Mac: %s", body)
	}
}

// Only a single JSON object merges: null (a nil map) or trailing data would
// merge as a partial object and drop the file's other keys.
func TestDecodeJSONObjectRefusesNullAndTrailingData(t *testing.T) {
	for _, bad := range []string{"null", `{"a":1} {"b":2}`, "[1]"} {
		if _, err := decodeJSONObject([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if _, err := decodeJSONObject([]byte(`{"a":1}` + "\n")); err != nil {
		t.Fatal(err)
	}
}
