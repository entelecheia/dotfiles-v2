package syncer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
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

type configResolver struct {
	file       string
	name       string
	line       int
	hasHome    bool
	hasProfile bool
}

func collectConfigResolvers(dir string) ([]configResolver, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}

	fset := token.NewFileSet()
	var resolvers []configResolver
	parsed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, 0, err
		}
		parsed++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Type.Results == nil {
				continue
			}
			returnsConfig := false
			for _, result := range fn.Type.Results.List {
				star, ok := result.Type.(*ast.StarExpr)
				if !ok {
					continue
				}
				ident, ok := star.X.(*ast.Ident)
				if ok && ident.Name == "Config" {
					returnsConfig = true
					break
				}
			}
			if !returnsConfig {
				continue
			}
			resolver := configResolver{
				file: name,
				name: qualifiedDeclName(fn),
				line: fset.Position(fn.Pos()).Line,
			}
			for _, field := range fn.Type.Params.List {
				for _, param := range field.Names {
					switch param.Name {
					case "home":
						resolver.hasHome = true
					case "profile":
						resolver.hasProfile = true
					}
				}
			}
			resolvers = append(resolvers, resolver)
		}
	}
	return resolvers, parsed, nil
}

// TestNoPartialConfigResolver closes the Config-family partial resolver class
// structurally: the compiler cannot refuse a new resolver that silently defaults
// one of home or profile. This deliberately bypasses resolutionAllowlist because
// that list permits call-site-bearing definition sites; a seventh entry would turn
// RES-03 into the enumerated table it rules out.
//
// ponytail: known ceiling. This predicate keys parameter checks on names, so aliases
// or options structs read as neither, and wrapper return types are not selected. The
// package naming convention and review mitigate those residuals without a type-aware
// analyzer dependency. Methods are deliberately included: a receiver does not excuse
// deriving a Config from just one of home or profile.
func TestNoPartialConfigResolver(t *testing.T) {
	dir := "."
	resolvers, parsed, err := collectConfigResolvers(dir)
	if err != nil {
		t.Fatalf("parsing %s production sources: %v", dir, err)
	}
	if parsed == 0 {
		t.Fatalf("parsed zero production files in %s: the check would report success without measuring anything", dir)
	}
	if len(resolvers) == 0 {
		t.Fatalf("examined zero Config-family declarations in %s: the check would report success without measuring anything", dir)
	}

	var findings []string
	for _, resolver := range resolvers {
		if resolver.hasHome == resolver.hasProfile {
			continue
		}
		named := "home"
		if resolver.hasProfile {
			named = "profile"
		}
		findings = append(findings, fmt.Sprintf("internal/syncer/%s:%d: %s names %s", resolver.file, resolver.line, resolver.name, named))
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Errorf("Config resolver declaration(s) name exactly one of home and profile, silently defaulting the other to the invoking user's home:\n  %s\ndelete the partial resolver rather than naming it anywhere; ResolveConfigForHomeProfile already exists for the case where only one is known", strings.Join(findings, "\n  "))
	}
}
