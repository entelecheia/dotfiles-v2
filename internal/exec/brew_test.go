package exec

import (
	"encoding/json"
	"testing"
)

// TestExtractAppName covers every shape that brew cask JSON emits for an
// `app` artifact entry: a plain string, a tuple with a target override, and
// a tuple with only a source. Keeping this close to the production function
// makes regressions cheap to catch when brew's schema shifts.
func TestExtractAppName(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain string", `"Raycast.app"`, "Raycast.app"},
		{"path-qualified string", `"Some/Nested/Path/Foo.app"`, "Foo.app"},
		{"tuple with target", `["Source.app", {"target": "/Applications/Target.app"}]`, "Target.app"},
		{"tuple source only", `["Source.app"]`, "Source.app"},
		{"empty tuple", `[]`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAppName(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Errorf("extractAppName(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestIsFormulaInstalledAcceptsTapQualifiedFormula(t *testing.T) {
	installed := map[string]bool{"bun": true}
	if !isFormulaInstalled(installed, "oven-sh/bun/bun") {
		t.Fatal("expected tap-qualified formula to match installed short formula name")
	}
	if isFormulaInstalled(installed, "oven-sh/other/other") {
		t.Fatal("unexpected match for unrelated tap-qualified formula")
	}
}

func TestTapsForFormulasIncludesAnchorCLI(t *testing.T) {
	got := TapsForFormulas([]string{
		"git",
		"anchor-cli",
		"staixbwlb/cask/anchor-cli",
		"anchor-cli",
	})
	want := []string{"staixbwlb/cask"}

	if len(got) != len(want) {
		t.Fatalf("expected %d taps, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tap %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestFormulaInstallGroupsSplitTapQualifiedFormula(t *testing.T) {
	got := formulaInstallGroups([]string{
		"git",
		"unzip",
		"gcc",
		"oven-sh/bun/bun",
		"tmux",
		"uv",
	})
	want := [][]string{
		{"git", "unzip", "gcc"},
		{"oven-sh/bun/bun"},
		{"tmux", "uv"},
	}

	if len(got) != len(want) {
		t.Fatalf("expected %d groups, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("group %d: expected %#v, got %#v", i, want[i], got[i])
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("group %d item %d: expected %q, got %q", i, j, want[i][j], got[i][j])
			}
		}
	}
}

func TestMissingFromInstalledDedupesInOrder(t *testing.T) {
	installed := map[string]bool{"homebrew/core": true}
	got := missingFromInstalled(installed, []string{
		"homebrew/core",
		"manaflow-ai/cmux",
		"manaflow-ai/cmux",
		"other/tap",
	})
	want := []string{"manaflow-ai/cmux", "other/tap"}
	if len(got) != len(want) {
		t.Fatalf("expected %d missing taps, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("missing tap %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestGCCCompilerFromBrewVersions(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"current gcc", "gcc 15.2.0_1\n", "gcc-15"},
		{"future major", "gcc 16.1.0\n", "gcc-16"},
		{"missing version", "gcc\n", ""},
		{"wrong formula", "llvm 22.0.0\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gccCompilerFromBrewVersions(tc.out); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
