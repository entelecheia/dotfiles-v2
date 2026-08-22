package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// GUARD-04: every `--json` surface is captured byte-for-byte under
// internal/cli/testdata/golden/, so a field that disappears or changes shape
// during the Phase 3 decomposition shows up as a diff in review instead of
// breaking a downstream consumer that still compiles.
//
// Regenerate after an intentional output change:
//
//	go test ./internal/cli -run 'TestJSONGolden' -update
//
// The -update flag is new machinery: this repo had no golden files and no
// TestMain before this test.
//
// What normalizeGolden erases is exactly what the goldens no longer protect.
// Currently two tokens, both unavoidable:
//
//	@HOME@    the fixture's temp HOME prefix
//	@ROOT@    the fixture's temp workspace-root prefix
//	@MACHINE@ the whole machineNames array, collapsed to one element
//
// @MACHINE@ is a concession, not a preference. syncer.MachineNames() reads
// os.Hostname() and, on darwin, shells out to scutil, so both its values and
// its element count are host- and OS-specific with no config or env seam to
// seed. Only the field's presence and position stay asserted. The `owner`
// field next to it IS seeded (to golden-machine) and stays fully protected,
// as does canPush, which is derived from the two.

var goldenUpdate = flag.Bool("update", false, "rewrite internal/cli/testdata/golden/*.json from live output")

const goldenDir = "testdata/golden"

// goldenMachineOwner is the seeded sync owner. It is a real state-file value,
// not a post-hoc substitution, so the owner-derived fields (canPush) stay real.
const goldenMachineOwner = "golden-machine"

// jsonCommandSurfaces walks the live cobra tree and returns the space-joined
// path of every command carrying a --json flag, without the `dot` root token.
//
// This is the matrix source, and it is a walk rather than a grep on purpose:
// addSkillScanFlags registers --json once and is called from two commands, so
// counting registration sites finds 12 surfaces and silently omits
// `dot ai skills list`. The walk finds 13.
func jsonCommandSurfaces(t *testing.T) []string {
	t.Helper()
	var surfaces []string
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		if len(path) > 0 && c.Flags().Lookup("json") != nil {
			surfaces = append(surfaces, strings.Join(path, " "))
		}
		for _, sub := range c.Commands() {
			walk(sub, append(append([]string{}, path...), sub.Name()))
		}
	}
	walk(NewRootCmd("dev", "test"), nil)
	sort.Strings(surfaces)
	return surfaces
}

// goldenCase is one --json surface: which command to run, with what stdin and
// which sandbox, and which golden file holds the expected document.
type goldenCase struct {
	surface string // space-joined command path, matching jsonCommandSurfaces
	args    []string
	stdin   string
	fixture func(t *testing.T) (home, root string)
}

func (tc goldenCase) goldenPath() string {
	return filepath.Join(goldenDir, strings.ReplaceAll(tc.surface, " ", "-")+".json")
}

// goldenCases covers all 13 surfaces. Loop variable is tc, per the repo's
// table-driven convention (internal/config/detector_test.go:26).
func goldenCases() []goldenCase {
	return []goldenCase{
		{surface: "ai auth status", args: []string{"ai", "auth", "status", "--json"}, fixture: goldenAIFixture},
		{surface: "ai skills list", args: []string{"ai", "skills", "list", "--json"}, fixture: goldenAIFixture},
		{surface: "ai skills status", args: []string{"ai", "skills", "status", "--json"}, fixture: goldenAIFixture},
		{surface: "ai skills validate", args: []string{"ai", "skills", "validate", "--json"}, fixture: goldenAIFixture},
		{surface: "ai update", args: []string{"ai", "update", "--json", "--dry-run"}, fixture: goldenAIFixture},
		{surface: "peer home-paths get", args: []string{"peer", "home-paths", "get", "--json"}, fixture: goldenSyncFixture},
		{surface: "peer home-paths set", args: []string{"peer", "home-paths", "set", "--json"}, stdin: "peer-host-a\npeer-host-b\n", fixture: goldenSyncFixture},
		{surface: "peer status", args: []string{"peer", "status", "--json"}, fixture: goldenSyncFixture},
		{surface: "sync configure", args: []string{"sync", "configure", "--json", "--yes"}, fixture: goldenSyncFixture},
		{surface: "sync filters get", args: []string{"sync", "filters", "get", "include", "--json"}, fixture: goldenSyncFixture},
		{surface: "sync filters set", args: []string{"sync", "filters", "set", "exclude", "--json"}, stdin: "*.tmp\nbuild/\n", fixture: goldenSyncFixture},
		{surface: "sync log", args: []string{"sync", "log", "--json"}, fixture: goldenSyncFixture},
		{surface: "sync status", args: []string{"sync", "status", "--json"}, fixture: goldenSyncFixture},
	}
}

// goldenSyncFixture builds on newSyncCLIFixture (sync_cli_test.go:22) and adds
// the two store configs the sync and peer profiles read, with owner seeded to a
// fixed value so the owner-derived fields are deterministic by construction
// rather than by post-processing.
func goldenSyncFixture(t *testing.T) (home, root string) {
	t.Helper()
	f := newSyncCLIFixture(t)
	sandboxGoldenPATH(t)
	storeCfg := "target: local:" + f.mirror + "\nowner: " + goldenMachineOwner + "\npropagation:\n  create: true\n  update: true\n  delete: false\ninterval: 600\n"
	for _, profile := range []string{"sync", "peer"} {
		writeCLITestFile(t, filepath.Join(f.local, ".dotfiles", profile, "config.yaml"), storeCfg)
	}
	return f.home, f.local
}

// goldenAIFixture reuses newOnestopFixture (onestop_cli_test.go:20), which
// already seeds the AI tool settings the `dot ai` surfaces read.
// goldenAIFixture reuses newOnestopFixture (onestop_cli_test.go:20) and adds
// one MCP server, one pending-auth entry, and one valid skill.
//
// The seeding is not decoration. An unseeded sandbox makes `ai auth status`
// emit "servers": [] and `ai skills list` emit "items": null, and an empty
// array pins nothing about the shape of its elements - which is the exact
// drift GUARD-04 exists to catch. One populated element per collection puts
// every per-item field under the golden.
func goldenAIFixture(t *testing.T) (home, root string) {
	t.Helper()
	f := newOnestopFixture(t)
	sandboxGoldenPATH(t)
	writeCLITestFile(t, filepath.Join(f.home, ".claude.json"),
		`{"mcpServers":{"golden-http":{"type":"http","url":"https://mcp.golden.invalid/sse"}}}`)
	writeCLITestFile(t, filepath.Join(f.home, ".claude", "mcp-needs-auth-cache.json"),
		`{"golden-connector":{}}`)
	skill := "---\nname: golden-skill\ndescription: A seeded skill so the scan report has one real item.\nschema_version: v1\n---\n\nBody.\n"
	writeCLITestFile(t, filepath.Join(f.home, ".claude", "skills", "golden-skill", "SKILL.md"), skill)
	writeCLITestFile(t, filepath.Join(f.home, ".maru", "skills", "golden-skill", "SKILL.md"), skill)
	return f.home, f.root
}

// sandboxGoldenPATH empties PATH so every CommandExists probe answers the same
// on a developer laptop and on a CI runner.
//
// This is not belt-and-braces: `dot ai update --json` branches on
// CommandExists(claude), CommandExists(codex) and six more, and `dot sync
// status --json` emits rsyncVersion only when rsync is reachable. Without this,
// the captured documents differ in SHAPE, not just in values, between a machine
// that has those tools and one that does not - and no normalizer can repair
// that. The goldens therefore record the tool-absent shape, which is the one
// shape both environments can reproduce.
func sandboxGoldenPATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// TestJSONGoldenMatrixIsComplete is the fail-closed half of GUARD-04: the
// invocation table below carries judgement (which argv and which sandbox make a
// surface produce a real document) and this test carries the completeness the
// table's author will forget. A new command that grows a --json flag has no
// golden until someone adds one here, and that is what turns red.
func TestJSONGoldenMatrixIsComplete(t *testing.T) {
	surfaces := jsonCommandSurfaces(t)
	if len(surfaces) == 0 {
		t.Fatal("the cobra walk found no --json surfaces; the guard would pass vacuously")
	}

	covered := map[string]bool{}
	for _, tc := range goldenCases() {
		covered[tc.surface] = true
	}

	for _, surface := range surfaces {
		if !covered[surface] {
			t.Errorf("command %q carries --json but has no entry in goldenCases()", surface)
			continue
		}
	}

	live := map[string]bool{}
	for _, surface := range surfaces {
		live[surface] = true
	}
	for _, tc := range goldenCases() {
		if !live[tc.surface] {
			t.Errorf("goldenCases() lists %q, which is no longer a command carrying --json", tc.surface)
			continue
		}
		if _, err := os.Stat(tc.goldenPath()); err != nil {
			t.Errorf("surface %q has no golden at %s: run `go test ./internal/cli -run TestJSONGolden -update`", tc.surface, tc.goldenPath())
		}
	}
}

// TestJSONGoldens asserts every --json document byte-for-byte. A field-by-field
// assertion (sync_json_test.go:12) passes when a field is dropped; a whole-
// document comparison does not, which is the entire point before Phase 3 moves
// 16,387 lines out of this package.
//
// Runs serially, per the repo convention: every fixture sets HOME, PATH and
// XDG_* via t.Setenv, which a parallel test may not do.
func TestJSONGoldens(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.surface, func(t *testing.T) {
			home, root := tc.fixture(t)
			out, errOut, err := runGoldenSurface(tc)
			if err != nil {
				t.Fatalf("%s: %v\nstderr=%s", strings.Join(tc.args, " "), err, errOut)
			}
			got := normalizeGolden(out, home, root)

			path := tc.goldenPath()
			if *goldenUpdate {
				if err := os.MkdirAll(goldenDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading golden: %v (run with -update to capture)", err)
			}
			if got != string(want) {
				t.Errorf("%s --json drifted from its golden %s\ngot:\n%s\nwant:\n%s", tc.surface, path, got, want)
			}
		})
	}
}

// runGoldenSurface invokes the real cobra tree in-process. Surfaces that do not
// read stdin go through runDotForTest (ai_agentos_cmd_test.go:289) unchanged;
// the two `set` surfaces read cmd.InOrStdin() and need it bound, or the test
// process would read its own stdin and could block.
func runGoldenSurface(tc goldenCase) (string, string, error) {
	if tc.stdin == "" {
		return runDotForTest(tc.args...)
	}
	root := NewRootCmd("dev", "test")
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetIn(strings.NewReader(tc.stdin))
	root.SetArgs(tc.args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

// normalizeGolden replaces only values that cannot be made deterministic by
// seeding a fixture. It does NOT touch schema versions, booleans, counts, or
// any structural field: those stay under the goldens' protection. Every token
// added here is a field the goldens stop guarding, so add one only after
// establishing there is no seam to seed instead.
func normalizeGolden(raw, home, root string) string {
	// Longest prefix first, so a root nested under home is not half-substituted.
	prefixes := []struct{ path, token string }{
		{home, "@HOME@"},
		{root, "@ROOT@"},
	}
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i].path) > len(prefixes[j].path) })
	for _, p := range prefixes {
		if p.path != "" {
			raw = strings.ReplaceAll(raw, p.path, p.token)
		}
	}
	return normalizeMachineNames(raw)
}

// normalizeMachineNames collapses every machineNames array to the single
// element @MACHINE@. syncer.MachineNames() derives its values from
// os.Hostname() and, on darwin, from scutil --get LocalHostName/ComputerName -
// so both the values AND the element count are host- and OS-specific (3 on a
// configured Mac, 1 on a Linux runner) with no config or env seam to seed.
// Presence and position of the field stay asserted; contents and length do not.
func normalizeMachineNames(raw string) string {
	const open = "\"machineNames\": ["
	var b strings.Builder
	for {
		start := strings.Index(raw, open)
		if start < 0 {
			break
		}
		end := strings.Index(raw[start:], "]")
		if end < 0 {
			break
		}
		b.WriteString(raw[:start+len(open)])
		b.WriteString("\"@MACHINE@\"")
		raw = raw[start+end:]
	}
	b.WriteString(raw)
	return b.String()
}
