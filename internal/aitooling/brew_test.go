package aitooling

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func brewFixture(t *testing.T, kind string) (*Engine, string, string) {
	t.Helper()
	e := testEngine(t)
	prefix := filepath.Join(e.opts.HomeDir, "brew")
	marker := "Cellar"
	if kind == "cask" {
		marker = "Caskroom"
	}
	binary := filepath.Join(prefix, marker, "codex", "1.0.0", "codex")
	brew := filepath.Join(prefix, "bin", "brew")
	for _, p := range []string{binary, brew} {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// macOS TempDir may use /var while provider provenance resolves its
	// /private/var alias. Compare canonical executable identities.
	var err error
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	brew, err = filepath.EvalSymlinks(brew)
	if err != nil {
		t.Fatal(err)
	}
	return e, binary, brew
}
func brewJSON(kind, version, tap string, pinned bool) string {
	if kind == "cask" {
		return fmt.Sprintf(`{"formulae":[],"casks":[{"token":"codex","full_token":"codex","tap":%q,"version":%q}]}`, tap, version)
	}
	return fmt.Sprintf(`{"formulae":[{"name":"codex","full_name":"codex","tap":%q,"versions":{"stable":%q},"pinned":%t}],"casks":[]}`, tap, version, pinned)
}
func TestBrewCheckUsesSelectedPackageMetadataOnly(t *testing.T) {
	for _, kind := range []string{"formula", "cask"} {
		t.Run(kind, func(t *testing.T) {
			e, path, brew := brewFixture(t, kind)
			calls := 0
			e.exec = func(_ context.Context, c command) (string, error) {
				calls++
				if c.Path != brew || !reflect.DeepEqual(c.Args, []string{"info", "--json=v2", "--" + kind, "codex"}) {
					t.Fatalf("unexpected command %s", commandDiagnostic(c))
				}
				if !strings.Contains(strings.Join(c.Env, "\n"), "HOMEBREW_NO_AUTO_UPDATE=1") {
					t.Fatal("automatic update enabled")
				}
				tap := "homebrew/core"
				if kind == "cask" {
					tap = "homebrew/cask"
				}
				return brewJSON(kind, "2.0.0", tap, false), nil
			}
			r := e.brewBinary(context.Background(), Entry{ID: "codex", Kind: "agent"}, "", Inspect, path, binarySpecs()["codex"], ItemResult{Installed: "1.0.0"})
			if r.Status != "update-available" || r.Latest != "2.0.0" || calls != 1 {
				t.Fatalf("%+v calls=%d", r, calls)
			}
		})
	}
}
func TestBrewPinAndForeignHomeNeverMutate(t *testing.T) {
	for _, tc := range []struct {
		name, pin string
		pinned    bool
		want      string
	}{{"requested unavailable", "1.5.0", false, "deferred-pin"}, {"brew pinned", "", true, "deferred-pin"}, {"foreign home", "", false, "deferred-home"}} {
		t.Run(tc.name, func(t *testing.T) {
			e, path, _ := brewFixture(t, "formula")
			e.exec = func(_ context.Context, c command) (string, error) {
				if c.Args[0] != "info" {
					t.Fatalf("unexpected mutation %s", commandDiagnostic(c))
				}
				return brewJSON("formula", "2.0.0", "homebrew/core", tc.pinned), nil
			}
			r := e.brewBinary(context.Background(), Entry{ID: "codex"}, tc.pin, Update, path, binarySpecs()["codex"], ItemResult{Installed: "1.0.0"})
			if r.Status != tc.want {
				t.Fatalf("%+v", r)
			}
		})
	}
}
func TestBrewUpgradeTargetsOnlyAdoptedPackage(t *testing.T) {
	for _, kind := range []string{"formula", "cask"} {
		t.Run(kind, func(t *testing.T) {
			e, path, brew := brewFixture(t, kind)
			e.opts.ExplicitHome = false
			var mutations int
			e.exec = func(_ context.Context, c command) (string, error) {
				if c.Args[0] == "info" {
					tap := "homebrew/core"
					if kind == "cask" {
						tap = "homebrew/cask"
					}
					return brewJSON(kind, "2.0.0", tap, false), nil
				}
				if c.Path == path {
					return "codex 2.0.0", nil
				}
				if c.Path != brew || !reflect.DeepEqual(c.Args, []string{"upgrade", "--" + kind, "codex"}) {
					t.Fatalf("unexpected mutation %s", commandDiagnostic(c))
				}
				for _, key := range []string{"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_INSTALL_CLEANUP=1", "HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK=1"} {
					if !strings.Contains(strings.Join(c.Env, "\n"), key) {
						t.Fatalf("missing %s", key)
					}
				}
				mutations++
				return "", nil
			}
			r := e.brewBinary(context.Background(), Entry{ID: "codex"}, "2.0.0", Update, path, binarySpecs()["codex"], ItemResult{Installed: "1.0.0"})
			if r.Status != "updated" || r.Installed != "2.0.0" || mutations != 1 || e.receipts["codex"].Provider != "brew" {
				t.Fatalf("%+v mutations=%d", r, mutations)
			}
		})
	}
}
func TestBrewRejectsForeignTapAndAmbiguousMetadata(t *testing.T) {
	for _, body := range []string{brewJSON("formula", "2.0.0", "thirdparty/tap", false), `{"formulae":[],"casks":[]}`, `broken`} {
		e, path, _ := brewFixture(t, "formula")
		e.exec = func(_ context.Context, c command) (string, error) {
			if c.Args[0] != "info" {
				t.Fatal("mutation")
			}
			return body, nil
		}
		r := e.brewBinary(context.Background(), Entry{ID: "codex"}, "", Update, path, binarySpecs()["codex"], ItemResult{Installed: "1.0.0"})
		if r.Status != "deferred-provenance" {
			t.Fatalf("%+v", r)
		}
	}
}
func TestBrewTargetMovementAndVerification(t *testing.T) {
	for _, tc := range []struct{ name, metadataAfter, binaryAfter, want string }{{"metadata moved", "3.0.0", "2.0.0", "deferred-provider"}, {"binary mismatch", "2.0.0", "1.0.0", "failed"}} {
		t.Run(tc.name, func(t *testing.T) {
			e, path, _ := brewFixture(t, "formula")
			e.opts.ExplicitHome = false
			infos := 0
			mutations := 0
			e.exec = func(_ context.Context, c command) (string, error) {
				if c.Args[0] == "info" {
					infos++
					version := "2.0.0"
					if infos > 1 {
						version = tc.metadataAfter
					}
					return brewJSON("formula", version, "homebrew/core", false), nil
				}
				if c.Path == path {
					return tc.binaryAfter, nil
				}
				mutations++
				return "", nil
			}
			r := e.brewBinary(context.Background(), Entry{ID: "codex"}, "", Update, path, binarySpecs()["codex"], ItemResult{Installed: "1.0.0"})
			if r.Status != tc.want {
				t.Fatalf("%+v", r)
			}
			if tc.name == "metadata moved" && mutations != 0 {
				t.Fatal("installed moved target")
			}
		})
	}
}
func TestBrewDryRunDoesNotUpgrade(t *testing.T) {
	e, path, _ := brewFixture(t, "formula")
	e.opts.ExplicitHome = false
	e.opts.DryRun = true
	e.exec = func(_ context.Context, c command) (string, error) {
		if c.Args[0] != "info" {
			t.Fatal("mutation")
		}
		return brewJSON("formula", "2.0.0", "homebrew/core", false), nil
	}
	r := e.brewBinary(context.Background(), Entry{ID: "codex"}, "", Update, path, binarySpecs()["codex"], ItemResult{Installed: "1.0.0"})
	if r.Status != "planned" {
		t.Fatalf("%+v", r)
	}
}

func TestBrewSelectedInstallAndFailure(t *testing.T) {
	e, _, brew := brewFixture(t, "formula")
	e.opts.ExplicitHome = false
	p := brewPackage{executable: brew, name: "codex", kind: "formula", version: "2.0.0"}
	e.exec = func(_ context.Context, c command) (string, error) {
		if !reflect.DeepEqual(c.Args, []string{"install", "--formula", "codex"}) {
			t.Fatalf("unexpected command %s", commandDiagnostic(c))
		}
		return "", fmt.Errorf("installer failed")
	}
	if err := e.applyBrew(context.Background(), p, "2.0.0", false); err == nil {
		t.Fatal("installation failure hidden")
	}
}
