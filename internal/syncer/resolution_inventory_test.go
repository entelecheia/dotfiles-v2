package syncer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file is the structural guard for three requirements that share one
// root cause: a path resolved from only half of (home, profile).
//
//   RES-03  internal/syncer/helpers.go:120 — resolvePathsForHomeProfile takes
//           home and profile together and is the single resolution entry
//           point; this file fails the build on any site that resolves from
//           one of them alone, rather than enumerating the callers that do.
//   BUG-27  internal/cli/peer_status.go:61 — `dot peer status` now threads
//           --home into the bootstrap, so peer scheduler paths follow the
//           target home rather than the invoking user's.
//   BUG-28  internal/cli/status_cmd.go:263 — `dot status` now reads the
//           already-resolved cfg.SystemPaths, so scheduler state follows the
//           profile the config was resolved for.
//
// The guard is structural rather than behavioral on purpose. BUG-27's
// mis-resolved value was discarded before it reached any output, and BUG-28
// substituted a value that is byte-identical for the default profile, so a
// behavioral test for either would be green before and after the fix and is no
// evidence at all. What can be asserted is the shape of the source: no second
// place decides a path from a partial input.
//
// Production files only. Several test fixtures legitimately build a partial
// Paths, and including them would produce immediate false positives whose only
// relief is weakening the predicate, which is how a structural guard dies. The
// same rule self-excludes this file, whose own failure messages contain the
// literals it matches on.

// resolutionAllowlist names the DEFINITION SITES that are permitted to resolve
// from a partial input. Every name here is a definition; none is a caller.
// That distinction is the requirement: an entry added because a new caller
// tripped the predicate converts this guard into the enumerated-callers table
// RES-03 exists to rule out, and the correct response to a red inventory is to
// remove the partial resolution, not to name it.
//
// The list is fail-closed in both directions. Every name here must still be
// found as a declaration in the walked set: a rename that silently dropped a
// site from the guard's coverage is the failure this list exists to catch. And
// a site NOT on the list is still checked, so a new resolver is covered without
// touching this list.
var resolutionAllowlist = []string{
	// HomeDir resolves a home and never a Paths, so it cannot produce a
	// partial artifact layout; the engine calls it instead of os.UserHomeDir
	// precisely so a command cannot resolve its profile from one home and
	// transfer against another (internal/syncer/sync.go:113-116).
	"Config.HomeDir",
	// The one legitimate Paths composite literal in the package.
	"pathsFor",
	// Reads os.UserHomeDir for the non-override branch, then resolves through
	// the two-argument entry point.
	"resolveConfig",
	// The environment-sensitive base: os.UserHomeDir plus os.UserCacheDir.
	"resolvePaths",
	"resolvePathsForHome",
	"resolvePathsForHomeProfile",
}

// resolutionSite is one predicate match, attributed to the declaration that
// encloses it.
type resolutionSite struct {
	file   string
	fn     string
	clause string
	line   int
}

// matchSyncerResolution is the in-package predicate. It reports a description
// of the clause a node matches, or "" for no match.
//
// ponytail: known ceiling. The clauses key on selector and identifier NAMES, so
// an aliased import of os, or a resolver reached through a function value
// rather than a direct call, escapes them. The mitigation is the allowlist
// reachability check below plus the review habit, not a cleverer matcher; a
// type-aware analyzer would close it and costs a new dependency.
func matchSyncerResolution(n ast.Node) string {
	switch node := n.(type) {
	case *ast.CallExpr:
		if name, ok := calleeName(node); ok && isResolvePathsFamily(name) {
			return "a call to " + name + ", which resolves paths from a partial input"
		}
		if isCallTo("os", "UserHomeDir")(n) {
			return "a call to os.UserHomeDir, which reads the invoking user's home rather than the target's"
		}
		if isCallTo("os", "UserCacheDir")(n) {
			return "a call to os.UserCacheDir, which reads the invoking user's cache rather than the target's"
		}
	case *ast.CompositeLit:
		// LocalPaths is a different type and is not a system artifact layout.
		if ident, ok := node.Type.(*ast.Ident); ok && ident.Name == "Paths" {
			return "a Paths composite literal, which builds an artifact layout outside pathsFor"
		}
	}
	return ""
}

// matchCLIPathsConstruction is the widened clause: Go cannot stop a composite
// literal from outside the package, so the compiler is not a control for a
// syncer.Paths built in internal/cli. It currently matches zero sites, which is
// why the walker reachability check below is not optional.
func matchCLIPathsConstruction(n ast.Node) string {
	lit, ok := n.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Paths" {
		return ""
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != "syncer" {
		return ""
	}
	return "a syncer.Paths composite literal built outside the package that owns resolution"
}

func calleeName(call *ast.CallExpr) (string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name, true
	case *ast.SelectorExpr:
		return fun.Sel.Name, true
	}
	return "", false
}

// isResolvePathsFamily reports whether name is a member of the resolvePaths
// family. The comparison is case-insensitive so a re-exported ResolvePaths is
// caught by the same clause as the unexported successor.
func isResolvePathsFamily(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "resolvepaths")
}

// collectResolutionSites parses every production .go file in dir and returns
// each node the matcher accepts, attributed to its enclosing declaration, plus
// the number of files parsed and the qualified name of every function and
// method declared in them. Files are parsed individually rather than via
// parser.ParseDir, which is deprecated.
func collectResolutionSites(dir string, match func(ast.Node) string) (sites []resolutionSite, parsed int, decls []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, nil, err
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, 0, nil, err
		}
		parsed++
		emit := func(fn string) func(ast.Node, string) {
			return func(n ast.Node, clause string) {
				sites = append(sites, resolutionSite{
					file:   name,
					fn:     fn,
					clause: clause,
					line:   fset.Position(n.Pos()).Line,
				})
			}
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				// A package-level var or const initializer can construct just
				// as well as a function body can.
				inspectScope(decl, match, emit("package-level declaration"))
				continue
			}
			declName := qualifiedDeclName(fn)
			decls = append(decls, declName)
			inspectScope(fn.Body, match, emit(declName))
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.FuncLit)
				if !ok {
					return true
				}
				inspectScope(lit.Body, match, emit(declName+" func literal"))
				return true
			})
		}
	}
	return sites, parsed, decls, nil
}

// inspectScope walks scope reporting every node the matcher accepts, without
// descending into nested function literals: a match inside a closure belongs to
// the closure, not to the function that built it, and is collected separately
// so it is attributed rather than lost.
func inspectScope(scope ast.Node, match func(ast.Node) string, emit func(ast.Node, string)) {
	ast.Inspect(scope, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if _, isLit := n.(*ast.FuncLit); isLit && n != scope {
			return false
		}
		if clause := match(n); clause != "" {
			emit(n, clause)
		}
		return true
	})
}

// qualifiedDeclName returns the declaration name, qualified with the receiver
// type for methods.
func qualifiedDeclName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return ident.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

func isCallTo(pkg, name string) func(ast.Node) bool {
	return func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != name {
			return false
		}
		ident, ok := sel.X.(*ast.Ident)
		return ok && ident.Name == pkg
	}
}

// TestResolutionInventory fails when a production site resolves an artifact
// path from only part of (home, profile): a resolvePaths-family call, a Paths
// composite literal, or an os.UserHomeDir/os.UserCacheDir read inside
// internal/syncer outside the six definition sites, or a syncer.Paths built in
// non-test internal/cli.
//
// Nobody edits a list to make this go red. A new call site turns it red on its
// own, which is the property RES-03 asks for and an enumerated-callers table
// cannot provide.
func TestResolutionInventory(t *testing.T) {
	if len(resolutionAllowlist) != 6 {
		t.Fatalf("resolutionAllowlist has %d entries, want 6: a seventh entry means a CALLER was admitted, which converts this guard into the enumerated-callers table RES-03 rules out — remove the partial resolution instead of naming it", len(resolutionAllowlist))
	}
	allowed := make(map[string]bool, len(resolutionAllowlist))
	for _, name := range resolutionAllowlist {
		allowed[name] = true
	}

	sites, parsed, decls, err := collectResolutionSites(".", matchSyncerResolution)
	if err != nil {
		t.Fatalf("parsing internal/syncer production sources: %v", err)
	}
	if parsed == 0 {
		t.Fatal("parsed zero production files in internal/syncer: the check would report success without measuring anything")
	}

	cliDir := filepath.Join("..", "cli")
	cliSites, cliParsed, _, err := collectResolutionSites(cliDir, matchCLIPathsConstruction)
	if err != nil {
		t.Fatalf("parsing %s production sources: %v", cliDir, err)
	}
	if cliParsed == 0 {
		t.Fatalf("parsed zero production files in %s: the check would report success without measuring anything", cliDir)
	}

	declared := make(map[string]bool, len(decls))
	for _, name := range decls {
		declared[name] = true
	}
	for _, want := range resolutionAllowlist {
		if !declared[want] {
			t.Errorf("allowlisted site %q is no longer declared in internal/syncer; if it was renamed, rename it in resolutionAllowlist too rather than leaving the guard blind to it", want)
		}
	}

	var findings []string
	for _, s := range sites {
		if allowed[s.fn] {
			continue
		}
		findings = append(findings, fmt.Sprintf("internal/syncer/%s:%d: %s inside %s", s.file, s.line, s.clause, s.fn))
	}
	for _, s := range cliSites {
		findings = append(findings, fmt.Sprintf("internal/cli/%s:%d: %s inside %s", s.file, s.line, s.clause, s.fn))
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Errorf("partial resolution site(s) found:\n  %s\nresolve through resolvePathsForHomeProfile, which takes home and profile together, or read the already-resolved Config.SystemPaths; do not add the enclosing function to resolutionAllowlist", strings.Join(findings, "\n  "))
	}
}

// TestSingleXDGConfigHomeRead asserts that exactly one place in the repository
// decides a path from the config-home variable. The count itself is the
// assertion: a second reader is a second precedence decision, and the two
// disagree the moment one of them is changed.
//
// The predicate matches a CALL to os.Getenv with the variable as its only
// argument, so the bare string in internal/module/gpg.go's subprocess argument
// list falls outside it by construction. Test files are skipped, which is the
// production-only rule this file states at the top and also excludes this file,
// whose own failure message contains the variable name.
func TestSingleXDGConfigHomeRead(t *testing.T) {
	root := filepath.Join("..", "..")
	const configHomeVar = "XDG_CONFIG_HOME"

	var sites []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "vendor" || name == "graphify-out" {
				return fs.SkipDir
			}
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parsing %s: %w", path, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if !isCallTo("os", "Getenv")(n) {
				return true
			}
			call := n.(*ast.CallExpr)
			if len(call.Args) != 1 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if value, unquoteErr := strconv.Unquote(lit.Value); unquoteErr == nil && value == configHomeVar {
				rel, _ := filepath.Rel(root, path)
				sites = append(sites, fmt.Sprintf("%s:%d", filepath.ToSlash(rel), fset.Position(call.Pos()).Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository for os.Getenv reads: %v", err)
	}
	sort.Strings(sites)

	switch {
	case len(sites) == 0:
		t.Fatalf("os.Getenv(%q) call count = 0, want 1: the walk rooted at %q matched nothing, so this check would report success having measured nothing", configHomeVar, root)
	case len(sites) > 1:
		t.Errorf("os.Getenv(%q) call count = %d, want 1 — a second place now decides a path from this variable:\n  %s", configHomeVar, len(sites), strings.Join(sites, "\n  "))
	}
}

type configFlowFinding struct {
	file      string
	line      int
	enclosing string
	callee    string
	derived   string
}

func (f configFlowFinding) String() string {
	return fmt.Sprintf("%s:%d: %s -> %s: %s derives from caller input while %s is defaulted", f.file, f.line, f.enclosing, f.callee, f.derived, map[string]string{"home": "profile", "profile": "home"}[f.derived])
}

// TestNoPartialConfigResolver rejects a caller flow that supplies exactly one
// role to the unique Config construction boundary. It deliberately bypasses
// resolutionAllowlist: that list safeguards Paths definition sites, whereas
// this guard discovers Config provenance without naming callers or wrappers.
//
// ponytail: known ceiling. Function-value aliases are followed when they are
// simple local bindings; an alias with an unknown target fails closed rather
// than being accepted as a default/default flow. Methods are included, and a
// wrapper's result type is irrelevant because every production declaration is
// scanned for calls into the discovered resolver graph.
func TestNoPartialConfigResolver(t *testing.T) {
	findings, _, err := collectConfigResolverFlows(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		return
	}
	formatted := make([]string, len(findings))
	for index, finding := range findings {
		formatted[index] = "internal/syncer/" + finding.String()
	}
	t.Errorf("partial Config resolver data flow found:\n  %s\npass both roles, default both roles at the single boundary, or read the already-resolved Config; do not add the enclosing function to resolutionAllowlist", strings.Join(formatted, "\n  "))
}

func TestConfigResolverInputFlow(t *testing.T) {
	base := `package syncer
type Config struct { Home, Profile string; SystemPaths any }
const DefaultProfile = "sync"
func resolveConfig(state any, migrate bool, home, profile string) (*Config, error) {
	override := home
	profile = normalize(profile)
	return &Config{Home: override, Profile: profile, SystemPaths: nil}, nil
}
func normalize(value string) string { return value }
`
	tests := []struct {
		name       string
		source     string
		want       string
		wantErr    string
		wantDerive string
	}{
		{"default_wrapper", `func defaults(state any) (*Config, error) { return resolveConfig(state, false, "", DefaultProfile) }`, "", "", ""},
		{"direct_full_pair", `func paired(state any, target, selected string) (*Config, error) { return resolveConfig(state, false, target, selected) }`, "", "", ""},
		{"full_options_pair", `type options struct { Home, Profile string }
func optionsPair(state any, opts options) (*Config, error) { return resolveConfig(state, false, opts.Home, opts.Profile) }`, "", "", ""},
		{"direct_partial", `func direct(state any, target string) (*Config, error) { return resolveConfig(state, false, target, DefaultProfile) }`, "direct", "", "home"},
		{"aliased_parameter_partial", `func aliased(state any, target string) (*Config, error) { alias := target; return resolveConfig(state, false, alias, DefaultProfile) }`, "aliased", "", "home"},
		{"options_struct_partial", `type options struct { Target string }
func optionsPartial(state any, opts options) (*Config, error) { return resolveConfig(state, false, opts.Target, DefaultProfile) }`, "optionsPartial", "", "home"},
		{"transitive_partial", `func paired(state any, target, selected string) (*Config, error) { return resolveConfig(state, false, target, selected) }
func transitive(state any, target string) (*Config, error) { return paired(state, target, DefaultProfile) }`, "transitive", "", "home"},
		{"method_partial", `type holder struct{}
func (holder) resolve(state any, migrate bool, home, profile string) (*Config, error) { return resolveConfig(state, migrate, home, profile) }
func methodPartial(state any, target string) (*Config, error) { return holder{}.resolve(state, false, target, DefaultProfile) }`, "methodPartial", "", "home"},
		{"selector_field_partial", `type holder struct { resolve func(any, bool, string, string) (*Config, error) }
func selectorField(state any, target string) (*Config, error) { h := holder{}; h.resolve = resolveConfig; return h.resolve(state, false, target, DefaultProfile) }`, "selectorField", "", "home"},
		{"closure_capture_partial", `func closureCapture(state any, target string) (*Config, error) { resolve := func() (*Config, error) { return resolveConfig(state, false, target, DefaultProfile) }; return resolve() }`, "closureCapture", "", "home"},
		{"recursive_full_pair", `func recursive(state any, home, profile string, again bool) (*Config, error) { if again { return recursive(state, home, profile, false) }; return resolveConfig(state, false, home, profile) }
func recursiveControl(state any, home, profile string) (*Config, error) { return recursive(state, home, profile, true) }`, "", "", ""},
		{"unbound_field", `type holder struct { resolve func(any, bool, string, string) (*Config, error) }
func unboundField(state any, target string) (*Config, error) { var h holder; return h.resolve(state, false, target, DefaultProfile) }`, "", "unresolved Config resolver call target", ""},
		{"opaque_field_assignment", `type holder struct { resolve func(any, bool, string, string) (*Config, error) }
func opaque() func(any, bool, string, string) (*Config, error) { return nil }
func opaqueField(state any, target string) (*Config, error) { h := holder{}; h.resolve = opaque(); return h.resolve(state, false, target, DefaultProfile) }`, "", "unresolved Config resolver call target", ""},
		{"helper_default_laundering", `func home() string { return "" }
func helperDefault(state any, target string) (*Config, error) { return resolveConfig(state, false, target, home()) }`, "", "unverified Config resolver input", ""},
		{"package_source", `var packageHome = ""
func packageSource(state any) (*Config, error) { return resolveConfig(state, false, packageHome, DefaultProfile) }`, "", "unverified Config resolver input", ""},
		{"environment_source", `func environment() string { return "" }
func environmentSource(state any) (*Config, error) { return resolveConfig(state, false, environment(), DefaultProfile) }`, "", "unverified Config resolver input", ""},
		{"wrapper_result_partial", `func wrapped(state any, target string) error { _, err := resolveConfig(state, false, target, DefaultProfile); return err }`, "wrapped", "", "home"},
		{"zero_constructor", `func nothing() {}`, "", "structural Config constructor count = 0", ""},
		{"multiple_constructors", `func another(state any, home, profile string) *Config { return &Config{Home: home, Profile: profile, SystemPaths: nil} }`, "", "structural Config constructor count = 2", ""},
		{"unresolved_summary", `func unresolved(state any, target string) (*Config, error) { resolver := resolveConfig; resolver = nil; return resolver(state, false, target, DefaultProfile) }`, "", "unresolved Config resolver call target", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			source := base + tt.source
			if tt.name == "zero_constructor" {
				source = "package syncer\ntype Config struct { Home, Profile string; SystemPaths any }\nconst DefaultProfile = \"sync\"\nfunc nothing() {}\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			findings, parsed, err := collectConfigResolverFlows(dir)
			if parsed != 1 {
				t.Fatalf("parsed production files = %d, want 1", parsed)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if len(findings) != 0 {
					t.Fatalf("findings = %v, want none", findings)
				}
				return
			}
			for _, finding := range findings {
				if finding.enclosing == tt.want && finding.derived == tt.wantDerive {
					return
				}
			}
			t.Fatalf("findings = %v, want a %s finding for %s", findings, tt.wantDerive, tt.want)
		})
	}

	t.Run("zero_parsed_files", func(t *testing.T) {
		_, _, err := collectConfigResolverFlows(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "parsed zero production files") {
			t.Fatalf("error = %v, want parsed-file non-vacuity failure", err)
		}
	})
}

// configPackageManifest is the only production source manifest accepted by
// the typed Config-flow collector. GoFiles and CgoFiles are intentionally kept
// separate until selection: CompiledGoFiles can contain generated cgo output
// that is neither source nor present in this package today.
type configPackageManifest struct {
	ImportPath     string
	Dir            string
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	TestGoFiles    []string
	XTestGoFiles   []string
}

type configExportRecord struct {
	ImportPath string
	Export     string
}

func configBuildEnv(goos string) map[string]string {
	// The checked package has no CgoFiles. Disabling cgo gives go list and the
	// export-data importer one portable dependency universe for Darwin/Linux
	// selection tests instead of asking the host C compiler to cross-build.
	return map[string]string{"GOOS": goos, "GOARCH": runtime.GOARCH, "CGO_ENABLED": "0"}
}

func configCommandEnv(overrides map[string]string) []string {
	keys := map[string]bool{"GOOS": true, "GOARCH": true, "CGO_ENABLED": true}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !keys[key] {
			env = append(env, entry)
		}
	}
	for _, key := range []string{"GOOS", "GOARCH", "CGO_ENABLED"} {
		if value, ok := overrides[key]; ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func configRunGoList(dir string, env map[string]string, args ...string) ([]byte, error) {
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = dir
	cmd.Env = configCommandEnv(env)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go %s in %s: %w: %s", strings.Join(append([]string{"list"}, args...), " "), dir, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func configPackageManifestFor(dir string, env map[string]string) (configPackageManifest, []string, error) {
	out, err := configRunGoList(dir, env, "-json", ".")
	if err != nil {
		return configPackageManifest{}, nil, err
	}
	var manifest configPackageManifest
	if err := json.Unmarshal(out, &manifest); err != nil {
		return configPackageManifest{}, nil, fmt.Errorf("decode go list manifest: %w", err)
	}
	if manifest.ImportPath == "" || manifest.Dir == "" {
		return configPackageManifest{}, nil, fmt.Errorf("go list manifest is missing ImportPath or Dir")
	}
	ignored := map[string]bool{}
	for _, name := range manifest.IgnoredGoFiles {
		ignored[name] = true
	}
	seen := map[string]bool{}
	selected := make([]string, 0, len(manifest.GoFiles)+len(manifest.CgoFiles))
	for _, names := range [][]string{manifest.GoFiles, manifest.CgoFiles} {
		for _, name := range names {
			if name == "" || strings.HasSuffix(name, "_test.go") || ignored[name] {
				return configPackageManifest{}, nil, fmt.Errorf("invalid build-selected file %q", name)
			}
			path := filepath.Join(manifest.Dir, name)
			rel, relErr := filepath.Rel(manifest.Dir, path)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return configPackageManifest{}, nil, fmt.Errorf("build-selected file %q is outside %s", name, manifest.Dir)
			}
			if !seen[path] {
				seen[path] = true
				selected = append(selected, path)
			}
		}
	}
	if len(selected) == 0 {
		return configPackageManifest{}, nil, fmt.Errorf("go list selected zero production files in %s", dir)
	}
	return manifest, selected, nil
}

func configExportFilesFor(dir string, env map[string]string) (map[string]string, error) {
	out, err := configRunGoList(dir, env, "-export", "-json", "-deps", ".")
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	exports := map[string]string{}
	for decoder.More() {
		var record configExportRecord
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("decode go list export record: %w", err)
		}
		if record.ImportPath != "" && record.Export != "" {
			exports[record.ImportPath] = record.Export
		}
	}
	return exports, nil
}

func configExportImporter(fset *token.FileSet, exports map[string]string) types.Importer {
	return importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		export, ok := exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for import %q", path)
		}
		return os.Open(export)
	})
}

type configTypedPackage struct {
	fset         *token.FileSet
	files        []*ast.File
	info         *types.Info
	pkg          *types.Package
	manifest     *configPackageManifest
	selected     []string
	configType   *types.TypeName
	defaultConst *types.Const
	callables    map[*types.Func]*configTypedCallable
	literals     map[token.Pos]*configTypedCallable
	storage      map[*types.Var]configTypedTargets
}

type configTypedCallable struct {
	key       string
	name      string
	fn        *types.Func
	signature *types.Signature
	body      *ast.BlockStmt
	captures  map[*types.Var]struct{}
}

type configTypedTargets struct {
	targets map[*configTypedCallable]struct{}
	seen    bool
	unknown bool
}

func configTypedPackageFromFiles(importPath string, filenames []string, files []*ast.File, fset *token.FileSet, importerForCheck types.Importer, manifest *configPackageManifest) (*configTypedPackage, error) {
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Scopes:     map[ast.Node]*types.Scope{},
	}
	var typeErrors []error
	conf := types.Config{Importer: importerForCheck, Error: func(err error) { typeErrors = append(typeErrors, err) }}
	pkg, err := conf.Check(importPath, fset, files, info)
	if err != nil {
		return nil, fmt.Errorf("type-check build-selected Config package: %w", err)
	}
	if len(typeErrors) != 0 {
		return nil, fmt.Errorf("type-check build-selected Config package: %v", typeErrors[0])
	}
	tp := &configTypedPackage{fset: fset, files: files, info: info, pkg: pkg, manifest: manifest, selected: filenames, callables: map[*types.Func]*configTypedCallable{}, literals: map[token.Pos]*configTypedCallable{}, storage: map[*types.Var]configTypedTargets{}}
	tp.configType, _ = pkg.Scope().Lookup("Config").(*types.TypeName)
	tp.defaultConst, _ = pkg.Scope().Lookup("DefaultProfile").(*types.Const)
	if tp.configType == nil || tp.defaultConst == nil {
		return nil, fmt.Errorf("typed Config package is missing Config or DefaultProfile")
	}
	for _, file := range files {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			object, ok := info.Defs[fn.Name].(*types.Func)
			if !ok {
				return nil, fmt.Errorf("typed function identity missing for %s", fn.Name.Name)
			}
			signature, ok := object.Type().(*types.Signature)
			if !ok {
				return nil, fmt.Errorf("function %s has no signature", fn.Name.Name)
			}
			tp.callables[object] = &configTypedCallable{key: fmt.Sprintf("%s@%s", object.FullName(), fset.Position(fn.Pos())), name: qualifiedDeclName(fn), fn: object, signature: signature, body: fn.Body}
		}
	}
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.FuncLit)
			if !ok {
				return true
			}
			signature, ok := info.TypeOf(literal).(*types.Signature)
			if !ok {
				return true
			}
			position := fset.Position(literal.Pos())
			captures := map[*types.Var]struct{}{}
			literalParams := map[*types.Var]bool{}
			for index := 0; index < signature.Params().Len(); index++ {
				literalParams[signature.Params().At(index)] = true
			}
			ast.Inspect(literal.Body, func(child ast.Node) bool {
				if nested, ok := child.(*ast.FuncLit); ok && nested != literal {
					return false
				}
				ident, ok := child.(*ast.Ident)
				if !ok {
					return true
				}
				variable, ok := info.Uses[ident].(*types.Var)
				if ok && !literalParams[variable] {
					captures[variable] = struct{}{}
				}
				return true
			})
			tp.literals[literal.Pos()] = &configTypedCallable{key: fmt.Sprintf("literal@%s", position), name: "func literal", signature: signature, body: literal.Body, captures: captures}
			return true
		})
	}
	tp.indexFunctionStorage()
	return tp, nil
}

func loadConfigTypedPackage(dir string, env map[string]string) (*configTypedPackage, error) {
	manifest, selected, err := configPackageManifestFor(dir, env)
	if err != nil {
		return nil, err
	}
	exports, err := configExportFilesFor(dir, env)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(selected))
	for _, filename := range selected {
		file, parseErr := parser.ParseFile(fset, filename, nil, parser.AllErrors)
		if parseErr != nil {
			return nil, fmt.Errorf("parse build-selected file %s: %w", filename, parseErr)
		}
		files = append(files, file)
	}
	return configTypedPackageFromFiles(manifest.ImportPath, selected, files, fset, configExportImporter(fset, exports), &manifest)
}

func configTypedFixture(dir string) (*configTypedPackage, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		filename := filepath.Join(dir, entry.Name())
		file, parseErr := parser.ParseFile(fset, filename, nil, parser.AllErrors)
		if parseErr != nil {
			return nil, parseErr
		}
		names, files = append(names, filename), append(files, file)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("parsed zero production files in %s: the check would report success without measuring anything", dir)
	}
	return configTypedPackageFromFiles("fixture/syncer", names, files, fset, importer.Default(), nil)
}

func configTypedStorageObject(info *types.Info, expression ast.Expr) (*types.Var, bool) {
	switch value := expression.(type) {
	case *ast.Ident:
		object, ok := info.ObjectOf(value).(*types.Var)
		return object, ok
	case *ast.SelectorExpr:
		object, ok := info.Uses[value.Sel].(*types.Var)
		return object, ok
	}
	return nil, false
}

func configTypedCallableForExpression(tp *configTypedPackage, expression ast.Expr) (*configTypedCallable, bool) {
	if literal, ok := expression.(*ast.FuncLit); ok {
		callable, found := tp.literals[literal.Pos()]
		return callable, found
	}
	if ident, ok := expression.(*ast.Ident); ok {
		function, ok := tp.info.Uses[ident].(*types.Func)
		if ok {
			callable, found := tp.callables[function]
			return callable, found
		}
	}
	if selector, ok := expression.(*ast.SelectorExpr); ok {
		if selection := tp.info.Selections[selector]; selection != nil {
			function, ok := selection.Obj().(*types.Func)
			if ok {
				callable, found := tp.callables[function]
				return callable, found
			}
		}
		function, ok := tp.info.Uses[selector.Sel].(*types.Func)
		if ok {
			callable, found := tp.callables[function]
			return callable, found
		}
	}
	return nil, false
}

func configTypedConfigCompatible(tp *configTypedPackage, typ types.Type) bool {
	signature, ok := typ.Underlying().(*types.Signature)
	if !ok || signature.Results().Len() == 0 {
		return false
	}
	pointer, ok := signature.Results().At(0).Type().(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := pointer.Elem().(*types.Named)
	return ok && named.Obj() == tp.configType
}

func (tp *configTypedPackage) indexFunctionStorage() {
	register := func(variable *types.Var) {
		if variable == nil || !configTypedConfigCompatible(tp, variable.Type()) {
			return
		}
		if _, ok := tp.storage[variable]; !ok {
			tp.storage[variable] = configTypedTargets{targets: map[*configTypedCallable]struct{}{}}
		}
	}
	for _, file := range tp.files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.Field:
				for _, name := range value.Names {
					variable, _ := tp.info.Defs[name].(*types.Var)
					register(variable)
				}
			case *ast.ValueSpec:
				for _, name := range value.Names {
					variable, _ := tp.info.Defs[name].(*types.Var)
					register(variable)
				}
			case *ast.AssignStmt:
				for _, left := range value.Lhs {
					variable, ok := configTypedStorageObject(tp.info, left)
					if ok {
						register(variable)
					}
				}
			}
			return true
		})
	}
	addAssignment := func(variable *types.Var, expression ast.Expr) {
		targets, ok := tp.storage[variable]
		if !ok {
			return
		}
		targets.seen = true
		if callable, found := configTypedCallableForExpression(tp, expression); found {
			targets.targets[callable] = struct{}{}
		} else if source, found := configTypedStorageObject(tp.info, expression); found {
			if inherited, exists := tp.storage[source]; exists {
				for callable := range inherited.targets {
					targets.targets[callable] = struct{}{}
				}
				targets.unknown = targets.unknown || inherited.unknown || !inherited.seen
			} else {
				targets.unknown = true
			}
		} else {
			targets.unknown = true
		}
		tp.storage[variable] = targets
	}
	for _, file := range tp.files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.ValueSpec:
				for index, name := range value.Names {
					if index >= len(value.Values) {
						continue
					}
					variable, _ := tp.info.Defs[name].(*types.Var)
					addAssignment(variable, value.Values[index])
				}
			case *ast.AssignStmt:
				for index, left := range value.Lhs {
					if index >= len(value.Rhs) {
						continue
					}
					variable, _ := configTypedStorageObject(tp.info, left)
					addAssignment(variable, value.Rhs[index])
				}
			}
			return true
		})
	}
	for variable, targets := range tp.storage {
		if !targets.seen {
			targets.unknown = true
			tp.storage[variable] = targets
		}
	}
}

// configOrigin is a three-fact provenance lattice. Inputs preserve exact
// go/types variable identity; verifiedDefault is legal only at Config's role
// boundary; unknown is deliberately sticky so an opaque helper or assignment
// cannot be laundered by a later known value.
type configOrigin struct {
	inputs          map[*types.Var]struct{}
	verifiedDefault bool
	unknown         bool
}

func configOriginInput(variable *types.Var) configOrigin {
	return configOrigin{inputs: map[*types.Var]struct{}{variable: {}}}
}

func configOriginUnknown() configOrigin { return configOrigin{unknown: true} }

func configOriginDefault() configOrigin { return configOrigin{verifiedDefault: true} }

func configOriginJoin(origins ...configOrigin) configOrigin {
	out := configOrigin{inputs: map[*types.Var]struct{}{}}
	for _, origin := range origins {
		for variable := range origin.inputs {
			out.inputs[variable] = struct{}{}
		}
		out.verifiedDefault = out.verifiedDefault || origin.verifiedDefault
		out.unknown = out.unknown || origin.unknown
	}
	return out
}

func configOriginEqual(left, right configOrigin) bool {
	if left.verifiedDefault != right.verifiedDefault || left.unknown != right.unknown || len(left.inputs) != len(right.inputs) {
		return false
	}
	for variable := range left.inputs {
		if _, ok := right.inputs[variable]; !ok {
			return false
		}
	}
	return true
}

func configOriginFacts(origin configOrigin) int {
	count := len(origin.inputs)
	if origin.verifiedDefault {
		count++
	}
	if origin.unknown {
		count++
	}
	return count
}

type configTypedSummary struct {
	home    configOrigin
	profile configOrigin
	results []configOrigin
}

func configTypedEmptySummary(callable *configTypedCallable) configTypedSummary {
	results := make([]configOrigin, callable.signature.Results().Len())
	for index := range results {
		results[index] = configOrigin{inputs: map[*types.Var]struct{}{}}
	}
	return configTypedSummary{home: configOrigin{inputs: map[*types.Var]struct{}{}}, profile: configOrigin{inputs: map[*types.Var]struct{}{}}, results: results}
}

func configTypedJoinSummary(left, right configTypedSummary) configTypedSummary {
	out := configTypedSummary{home: configOriginJoin(left.home, right.home), profile: configOriginJoin(left.profile, right.profile), results: make([]configOrigin, len(left.results))}
	for index := range out.results {
		out.results[index] = configOriginJoin(left.results[index], right.results[index])
	}
	return out
}

func configTypedSummaryEqual(left, right configTypedSummary) bool {
	if !configOriginEqual(left.home, right.home) || !configOriginEqual(left.profile, right.profile) || len(left.results) != len(right.results) {
		return false
	}
	for index := range left.results {
		if !configOriginEqual(left.results[index], right.results[index]) {
			return false
		}
	}
	return true
}

type configTypedState struct{ values map[*types.Var]configOrigin }

func configTypedInitialState(callable *configTypedCallable) configTypedState {
	state := configTypedState{values: map[*types.Var]configOrigin{}}
	if receiver := callable.signature.Recv(); receiver != nil {
		state.values[receiver] = configOriginInput(receiver)
	}
	for index := 0; index < callable.signature.Params().Len(); index++ {
		parameter := callable.signature.Params().At(index)
		state.values[parameter] = configOriginInput(parameter)
	}
	for captured := range callable.captures {
		state.values[captured] = configOriginInput(captured)
	}
	return state
}

func configTypedCloneState(in configTypedState) configTypedState {
	out := configTypedState{values: map[*types.Var]configOrigin{}}
	for variable, origin := range in.values {
		out.values[variable] = configOriginJoin(origin)
	}
	return out
}

func configTypedJoinState(left, right configTypedState) configTypedState {
	out := configTypedCloneState(left)
	for variable, origin := range right.values {
		out.values[variable] = configOriginJoin(out.values[variable], origin)
	}
	return out
}

type configTypedCall struct {
	file       string
	line       int
	callable   *configTypedCallable
	args       []ast.Expr
	state      configTypedState
	compatible bool
	unresolved bool
}

type configTypedAnalysis struct {
	constructors []configTypedSummary
	calls        []configTypedCall
	results      []configOrigin
}

func configTypedCallTarget(tp *configTypedPackage, call *ast.CallExpr) (*configTypedCallable, bool, bool) {
	if callable, ok := configTypedCallableForExpression(tp, call.Fun); ok {
		return callable, configTypedConfigCompatible(tp, callable.signature), false
	}
	storage, ok := configTypedStorageObject(tp.info, call.Fun)
	if !ok {
		return nil, configTypedConfigCompatible(tp, tp.info.TypeOf(call.Fun)), false
	}
	targets, tracked := tp.storage[storage]
	compatible := configTypedConfigCompatible(tp, storage.Type())
	if !tracked {
		return nil, compatible, compatible
	}
	if targets.unknown || len(targets.targets) == 0 {
		return nil, compatible, compatible
	}
	// A branch-local resolver variable can have more than one exact local
	// assignment (Bootstrap selects its read-only form this way). Its static
	// signature is already Config-compatible; the solver joins the selected
	// callable summary rather than treating a formally proved local set as an
	// opaque external value. Unknown assignments still fail closed above.
	ordered := make([]*configTypedCallable, 0, len(targets.targets))
	for callable := range targets.targets {
		ordered = append(ordered, callable)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].key < ordered[j].key })
	return ordered[0], compatible, false
}

func configTypedCallArguments(call *ast.CallExpr, callable *configTypedCallable) []ast.Expr {
	if callable == nil {
		return append([]ast.Expr(nil), call.Args...)
	}
	arguments := append([]ast.Expr(nil), call.Args...)
	if callable.signature.Recv() == nil {
		return arguments
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	return append([]ast.Expr{selector.X}, arguments...)
}

func configTypedSubstitute(tp *configTypedPackage, origin configOrigin, callee *configTypedCallable, call configTypedCall, summaries map[*configTypedCallable]configTypedSummary, role string) configOrigin {
	if role == "" && origin.verifiedDefault {
		// A helper result is not a role-boundary literal. Defaults stay verified
		// only where Config is constructed, so helper results fail closed.
		origin.unknown = true
		origin.verifiedDefault = false
	}
	arguments := configTypedCallArgumentsFromRecord(call, callee)
	output := configOrigin{inputs: map[*types.Var]struct{}{}, unknown: origin.unknown}
	for variable := range origin.inputs {
		if expression, ok := arguments[variable]; ok {
			output = configOriginJoin(output, configTypedExprOrigin(tp, expression, call.state, summaries, role))
			continue
		}
		if captured, ok := call.state.values[variable]; ok {
			output = configOriginJoin(output, captured)
			continue
		}
		output.unknown = true
	}
	return output
}

func configTypedCallArgumentsFromRecord(call configTypedCall, callable *configTypedCallable) map[*types.Var]ast.Expr {
	arguments := configTypedCallArgumentsFromAST(call.args, callable)
	if arguments == nil {
		return nil
	}
	out := map[*types.Var]ast.Expr{}
	index := 0
	if receiver := callable.signature.Recv(); receiver != nil {
		if len(arguments) == 0 {
			return out
		}
		out[receiver] = arguments[0]
		index++
	}
	for parameter := 0; parameter < callable.signature.Params().Len() && index < len(arguments); parameter, index = parameter+1, index+1 {
		out[callable.signature.Params().At(parameter)] = arguments[index]
	}
	return out
}

// args stores the direct argument list. A method record prefixes the receiver
// before it reaches this helper, keeping substitutions keyed by exact vars.
func configTypedCallArgumentsFromAST(arguments []ast.Expr, callable *configTypedCallable) []ast.Expr {
	if callable.signature.Recv() == nil {
		return arguments
	}
	if len(arguments) == 0 {
		return nil
	}
	return arguments
}

func configTypedExprOrigin(tp *configTypedPackage, expression ast.Expr, state configTypedState, summaries map[*configTypedCallable]configTypedSummary, role string) configOrigin {
	if expression == nil {
		return configOriginUnknown()
	}
	switch value := expression.(type) {
	case *ast.BasicLit:
		if role == "home" && value.Kind == token.STRING {
			if text, err := strconv.Unquote(value.Value); err == nil && text == "" {
				return configOriginDefault()
			}
		}
		return configOriginUnknown()
	case *ast.Ident:
		if variable, ok := tp.info.ObjectOf(value).(*types.Var); ok {
			if origin, found := state.values[variable]; found {
				return origin
			}
			return configOriginUnknown()
		}
		if role == "profile" && tp.info.ObjectOf(value) == tp.defaultConst {
			return configOriginDefault()
		}
		return configOriginUnknown()
	case *ast.SelectorExpr:
		return configTypedExprOrigin(tp, value.X, state, summaries, "")
	case *ast.ParenExpr:
		return configTypedExprOrigin(tp, value.X, state, summaries, role)
	case *ast.UnaryExpr:
		return configTypedExprOrigin(tp, value.X, state, summaries, "")
	case *ast.StarExpr:
		return configTypedExprOrigin(tp, value.X, state, summaries, "")
	case *ast.CompositeLit:
		origins := make([]configOrigin, 0, len(value.Elts))
		for _, element := range value.Elts {
			origins = append(origins, configTypedExprOrigin(tp, element, state, summaries, ""))
		}
		if len(origins) == 0 {
			return configOriginUnknown()
		}
		return configOriginJoin(origins...)
	case *ast.KeyValueExpr:
		return configTypedExprOrigin(tp, value.Value, state, summaries, "")
	case *ast.BinaryExpr:
		return configOriginJoin(configTypedExprOrigin(tp, value.X, state, summaries, ""), configTypedExprOrigin(tp, value.Y, state, summaries, ""))
	case *ast.CallExpr:
		callable, compatible, unresolved := configTypedCallTarget(tp, value)
		if unresolved || callable == nil {
			return configOriginUnknown()
		}
		summary, ok := summaries[callable]
		if !ok || len(summary.results) == 0 {
			return configOriginUnknown()
		}
		call := configTypedCall{callable: callable, args: configTypedCallArguments(value, callable), state: state, compatible: compatible}
		if configOriginFacts(summary.results[0]) == 0 {
			// During the monotonic prepass an exact local helper may not have
			// produced its result summary yet. Keep this bottom until the next
			// pass rather than permanently laundering it into unknown.
			return configOrigin{inputs: map[*types.Var]struct{}{}}
		}
		return configTypedSubstitute(tp, summary.results[0], callable, call, summaries, "")
	case *ast.IndexExpr:
		return configOriginJoin(configTypedExprOrigin(tp, value.X, state, summaries, ""), configTypedExprOrigin(tp, value.Index, state, summaries, ""))
	case *ast.SliceExpr:
		return configTypedExprOrigin(tp, value.X, state, summaries, "")
	}
	return configOriginUnknown()
}

func configTypedConstructor(tp *configTypedPackage, literal *ast.CompositeLit) (ast.Expr, ast.Expr, bool) {
	named, ok := tp.info.TypeOf(literal).(*types.Named)
	if !ok || named.Obj() != tp.configType {
		return nil, nil, false
	}
	var home, profile ast.Expr
	var hasHome, hasProfile, hasPaths bool
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nil, nil, false
		}
		name, ok := field.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch name.Name {
		case "Home":
			home, hasHome = field.Value, true
		case "Profile":
			profile, hasProfile = field.Value, true
		case "SystemPaths":
			hasPaths = true
		}
	}
	return home, profile, hasHome && hasProfile && hasPaths
}

func configTypedAssign(tp *configTypedPackage, state *configTypedState, lhs ast.Expr, rhs ast.Expr, summaries map[*configTypedCallable]configTypedSummary) {
	variable, ok := configTypedStorageObject(tp.info, lhs)
	if !ok {
		return
	}
	next := configTypedExprOrigin(tp, rhs, *state, summaries, "")
	if previous, exists := state.values[variable]; exists && len(previous.inputs) != 0 && next.unknown && len(next.inputs) == 0 {
		// A normalization assignment such as profile = NormalizeProfile(profile)
		// still carries its formal caller position. Do not let a conservative
		// helper-result branch erase an already-proved direct formal source.
		next = previous
	}
	state.values[variable] = next
}

func configTypedAnalyze(tp *configTypedPackage, callable *configTypedCallable, summaries map[*configTypedCallable]configTypedSummary) configTypedAnalysis {
	analysis := configTypedAnalysis{results: make([]configOrigin, callable.signature.Results().Len())}
	for index := range analysis.results {
		analysis.results[index] = configOrigin{inputs: map[*types.Var]struct{}{}}
	}
	state := configTypedInitialState(callable)
	var walkStatements func([]ast.Stmt, *configTypedState)
	walkExpr := func(expression ast.Expr, current configTypedState) {
		if expression == nil {
			return
		}
		ast.Inspect(expression, func(node ast.Node) bool {
			if node == nil {
				return false
			}
			if literal, ok := node.(*ast.FuncLit); ok && literal != expression {
				return false
			}
			switch value := node.(type) {
			case *ast.CompositeLit:
				if home, profile, ok := configTypedConstructor(tp, value); ok {
					analysis.constructors = append(analysis.constructors, configTypedSummary{home: configTypedExprOrigin(tp, home, current, summaries, "home"), profile: configTypedExprOrigin(tp, profile, current, summaries, "profile"), results: make([]configOrigin, callable.signature.Results().Len())})
				}
			case *ast.CallExpr:
				target, compatible, unresolved := configTypedCallTarget(tp, value)
				if target != nil || (compatible && unresolved) {
					position := tp.fset.Position(value.Pos())
					analysis.calls = append(analysis.calls, configTypedCall{file: position.Filename, line: position.Line, callable: target, args: configTypedCallArguments(value, target), state: configTypedCloneState(current), compatible: compatible, unresolved: unresolved})
				}
			}
			return true
		})
	}
	walkStatements = func(statements []ast.Stmt, current *configTypedState) {
		for _, statement := range statements {
			switch value := statement.(type) {
			case *ast.DeclStmt:
				declaration, ok := value.Decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range declaration.Specs {
					variable, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for index, name := range variable.Names {
						if index >= len(variable.Values) {
							continue
						}
						walkExpr(variable.Values[index], *current)
						configTypedAssign(tp, current, name, variable.Values[index], summaries)
					}
				}
			case *ast.AssignStmt:
				for _, rhs := range value.Rhs {
					walkExpr(rhs, *current)
				}
				for index, lhs := range value.Lhs {
					if index < len(value.Rhs) {
						configTypedAssign(tp, current, lhs, value.Rhs[index], summaries)
					}
				}
			case *ast.ExprStmt:
				walkExpr(value.X, *current)
			case *ast.ReturnStmt:
				for index, result := range value.Results {
					walkExpr(result, *current)
					if index < len(analysis.results) {
						analysis.results[index] = configOriginJoin(analysis.results[index], configTypedExprOrigin(tp, result, *current, summaries, ""))
					}
				}
			case *ast.BlockStmt:
				walkStatements(value.List, current)
			case *ast.IfStmt:
				if value.Init != nil {
					walkStatements([]ast.Stmt{value.Init}, current)
				}
				walkExpr(value.Cond, *current)
				thenState := configTypedCloneState(*current)
				walkStatements(value.Body.List, &thenState)
				elseState := configTypedCloneState(*current)
				if block, ok := value.Else.(*ast.BlockStmt); ok {
					walkStatements(block.List, &elseState)
				} else if nested, ok := value.Else.(*ast.IfStmt); ok {
					walkStatements([]ast.Stmt{nested}, &elseState)
				}
				*current = configTypedJoinState(thenState, elseState)
			case *ast.ForStmt:
				loopState := configTypedCloneState(*current)
				walkStatements(value.Body.List, &loopState)
				*current = configTypedJoinState(*current, loopState)
			case *ast.RangeStmt:
				walkExpr(value.X, *current)
				loopState := configTypedCloneState(*current)
				walkStatements(value.Body.List, &loopState)
				*current = configTypedJoinState(*current, loopState)
			}
		}
	}
	walkStatements(callable.body.List, &state)
	return analysis
}

func configTypedSortedCallables(tp *configTypedPackage) []*configTypedCallable {
	seen := map[*configTypedCallable]bool{}
	all := make([]*configTypedCallable, 0, len(tp.callables)+len(tp.literals))
	for _, callable := range tp.callables {
		if !seen[callable] {
			seen[callable] = true
			all = append(all, callable)
		}
	}
	for _, callable := range tp.literals {
		if !seen[callable] {
			seen[callable] = true
			all = append(all, callable)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].key < all[j].key })
	return all
}

// configFlowStateDomain computes the finite lattice capacity from exactly the
// slots the solver mutates: two Config roles plus every typed result. It is
// deliberately shared with the convergence loop, so a declaration-count cap
// cannot accidentally consume changing summaries.
func configFlowStateDomain(callables []*configTypedCallable) int {
	inputs := map[*types.Var]struct{}{}
	slots := 0
	for _, callable := range callables {
		slots += 2 + callable.signature.Results().Len()
		if receiver := callable.signature.Recv(); receiver != nil {
			inputs[receiver] = struct{}{}
		}
		for index := 0; index < callable.signature.Params().Len(); index++ {
			inputs[callable.signature.Params().At(index)] = struct{}{}
		}
		for captured := range callable.captures {
			inputs[captured] = struct{}{}
		}
	}
	return slots * (len(inputs) + 2)
}

func configTypedFactCount(summaries map[*configTypedCallable]configTypedSummary) int {
	count := 0
	for _, summary := range summaries {
		count += configOriginFacts(summary.home) + configOriginFacts(summary.profile)
		for _, result := range summary.results {
			count += configOriginFacts(result)
		}
	}
	return count
}

func solveConfigFlowSummaries(tp *configTypedPackage) (map[*configTypedCallable]configTypedSummary, map[*configTypedCallable]configTypedAnalysis, error) {
	callables := configTypedSortedCallables(tp)
	summaries := map[*configTypedCallable]configTypedSummary{}
	for _, callable := range callables {
		summaries[callable] = configTypedEmptySummary(callable)
	}
	capacity := configFlowStateDomain(callables)
	if capacity == 0 {
		return nil, nil, fmt.Errorf("Config resolver summaries exceeded finite lattice bound: pass=0 count=0 bound=0")
	}
	for pass := 1; ; pass++ {
		analyses := map[*configTypedCallable]configTypedAnalysis{}
		for _, callable := range callables {
			analysis := configTypedAnalyze(tp, callable, summaries)
			analyses[callable] = analysis
			for _, call := range analysis.calls {
				if call.compatible && call.unresolved {
					return nil, nil, fmt.Errorf("unresolved Config resolver call target in %s:%d", callable.name, call.line)
				}
			}
			candidate := configTypedEmptySummary(callable)
			for _, constructor := range analysis.constructors {
				candidate.home = configOriginJoin(candidate.home, constructor.home)
				candidate.profile = configOriginJoin(candidate.profile, constructor.profile)
			}
			for _, call := range analysis.calls {
				if call.callable == nil {
					continue
				}
				target, ok := summaries[call.callable]
				if !ok {
					continue
				}
				candidate.home = configOriginJoin(candidate.home, configTypedSubstitute(tp, target.home, call.callable, call, summaries, "home"))
				candidate.profile = configOriginJoin(candidate.profile, configTypedSubstitute(tp, target.profile, call.callable, call, summaries, "profile"))
			}
			for index, result := range analysis.results {
				candidate.results[index] = configOriginJoin(candidate.results[index], result)
			}
			joined := configTypedJoinSummary(summaries[callable], candidate)
			if !configTypedSummaryEqual(summaries[callable], joined) {
				summaries[callable] = joined
			}
		}
		count := configTypedFactCount(summaries)
		if count > capacity {
			return nil, nil, fmt.Errorf("Config resolver summaries exceeded finite lattice bound: pass=%d count=%d bound=%d", pass, count, capacity)
		}
		stable := true
		for _, callable := range callables {
			analysis := configTypedAnalyze(tp, callable, summaries)
			candidate := configTypedEmptySummary(callable)
			for _, constructor := range analysis.constructors {
				candidate.home = configOriginJoin(candidate.home, constructor.home)
				candidate.profile = configOriginJoin(candidate.profile, constructor.profile)
			}
			for _, call := range analysis.calls {
				if target, ok := summaries[call.callable]; ok {
					candidate.home = configOriginJoin(candidate.home, configTypedSubstitute(tp, target.home, call.callable, call, summaries, "home"))
					candidate.profile = configOriginJoin(candidate.profile, configTypedSubstitute(tp, target.profile, call.callable, call, summaries, "profile"))
				}
			}
			for index, result := range analysis.results {
				candidate.results[index] = configOriginJoin(candidate.results[index], result)
			}
			if !configTypedSummaryEqual(summaries[callable], configTypedJoinSummary(summaries[callable], candidate)) {
				stable = false
				break
			}
		}
		if stable {
			final := map[*configTypedCallable]configTypedAnalysis{}
			for _, callable := range callables {
				final[callable] = configTypedAnalyze(tp, callable, summaries)
			}
			return summaries, final, nil
		}
		if pass >= capacity {
			return nil, nil, fmt.Errorf("Config resolver summaries exceeded finite lattice bound: pass=%d count=%d bound=%d", pass, count, capacity)
		}
	}
}

func collectConfigResolverFlows(dir string) ([]configFlowFinding, int, error) {
	var typedPackage *configTypedPackage
	var err error
	if filepath.Clean(dir) == "." {
		typedPackage, err = loadConfigTypedPackage(dir, configBuildEnv(runtime.GOOS))
	} else {
		typedPackage, err = configTypedFixture(dir)
	}
	if err != nil {
		return nil, 0, err
	}
	constructors := 0
	for _, callable := range configTypedSortedCallables(typedPackage) {
		constructors += len(configTypedAnalyze(typedPackage, callable, map[*configTypedCallable]configTypedSummary{}).constructors)
	}
	if constructors != 1 {
		return nil, len(typedPackage.files), fmt.Errorf("structural Config constructor count = %d, want 1: the check cannot derive a unique home/profile boundary", constructors)
	}
	summaries, analyses, err := solveConfigFlowSummaries(typedPackage)
	if err != nil {
		return nil, len(typedPackage.files), err
	}
	var findings []configFlowFinding
	for callable, analysis := range analyses {
		for _, call := range analysis.calls {
			if !call.compatible || call.callable == nil {
				continue
			}
			summary, ok := summaries[call.callable]
			if !ok {
				return nil, len(typedPackage.files), fmt.Errorf("unverified Config resolver input in %s:%d", callable.name, call.line)
			}
			home := configTypedSubstitute(typedPackage, summary.home, call.callable, call, summaries, "home")
			profile := configTypedSubstitute(typedPackage, summary.profile, call.callable, call, summaries, "profile")
			if home.unknown || profile.unknown {
				return nil, len(typedPackage.files), fmt.Errorf("unverified Config resolver input in %s:%d", callable.name, call.line)
			}
			homeDerived := len(home.inputs) != 0
			profileDerived := len(profile.inputs) != 0
			if !homeDerived && !profileDerived {
				if home.verifiedDefault && profile.verifiedDefault {
					continue
				}
				return nil, len(typedPackage.files), fmt.Errorf("internal Config resolver analysis error in %s:%d: bottom role origin", callable.name, call.line)
			}
			if homeDerived == profileDerived {
				continue
			}
			derived := "home"
			if !homeDerived {
				derived = "profile"
			}
			findings = append(findings, configFlowFinding{file: filepath.Base(call.file), line: call.line, enclosing: callable.name, callee: call.callable.name, derived: derived})
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].String() < findings[j].String() })
	return findings, len(typedPackage.files), nil
}

func TestConfigTypedPackageBuildSelection(t *testing.T) {
	for _, target := range []struct{ name, goos string }{{"native", runtime.GOOS}, {"darwin", "darwin"}, {"linux", "linux"}} {
		target := target
		t.Run(target.name, func(t *testing.T) {
			goos := target.goos
			env := configBuildEnv(goos)
			manifest, selected, err := configPackageManifestFor(".", env)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.CgoFiles) != 0 {
				t.Fatalf("CgoFiles = %v, want empty in the current tree", manifest.CgoFiles)
			}
			ignored := map[string]bool{}
			for _, name := range manifest.IgnoredGoFiles {
				ignored[name] = true
			}
			actual := make([]string, 0, len(selected))
			for _, filename := range selected {
				base := filepath.Base(filename)
				if strings.HasSuffix(base, "_test.go") || ignored[base] {
					t.Fatalf("selected build file %q is a test or ignored file", base)
				}
				actual = append(actual, base)
			}
			sort.Strings(actual)
			typedPackage, err := loadConfigTypedPackage(".", env)
			if err != nil {
				t.Fatal(err)
			}
			checked := make([]string, 0, len(typedPackage.selected))
			for _, filename := range typedPackage.selected {
				checked = append(checked, filepath.Base(filename))
			}
			sort.Strings(checked)
			if strings.Join(actual, ",") != strings.Join(checked, ",") {
				t.Fatalf("typed checked files = %v, want go-list manifest %v", checked, actual)
			}
			contains := func(names []string, want string) bool {
				return sort.SearchStrings(names, want) < len(names) && names[sort.SearchStrings(names, want)] == want
			}
			switch goos {
			case "darwin":
				if !contains(actual, "scheduler_darwin.go") || contains(actual, "scheduler_other.go") || !ignored["scheduler_other.go"] {
					t.Fatalf("Darwin selection = %v, ignored = %v; want scheduler_darwin.go only", actual, manifest.IgnoredGoFiles)
				}
			case "linux":
				if !contains(actual, "scheduler_other.go") || contains(actual, "scheduler_darwin.go") || !ignored["scheduler_darwin.go"] {
					t.Fatalf("Linux selection = %v, ignored = %v; want scheduler_other.go only", actual, manifest.IgnoredGoFiles)
				}
			}
		})
	}
}
