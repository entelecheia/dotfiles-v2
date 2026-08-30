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

// configInputSet records which formal parameter positions can influence an
// expression. Parameter spelling is deliberately absent from this type: a
// selector such as opts.Target derives from opts exactly as a local alias does.
type configInputSet map[int]struct{}

// configObject preserves parser-resolved lexical identity without requiring a
// type checker for a production-only source inventory.
//
//nolint:staticcheck // ast.Object is the parser's lexical-object contract here.
type configObject ast.Object

func configObjectFor(ident *ast.Ident) *configObject {
	if ident == nil || ident.Obj == nil {
		return nil
	}
	return (*configObject)(ident.Obj)
}

func cloneConfigInputSet(in configInputSet) configInputSet {
	out := make(configInputSet, len(in))
	for index := range in {
		out[index] = struct{}{}
	}
	return out
}

func unionConfigInputSets(sets ...configInputSet) configInputSet {
	out := configInputSet{}
	for _, set := range sets {
		for index := range set {
			out[index] = struct{}{}
		}
	}
	return out
}

type configFlowSummary struct {
	home    configInputSet
	profile configInputSet
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

type configResolverDecl struct {
	file   string
	fset   *token.FileSet
	decl   *ast.FuncDecl
	key    string
	name   string
	params []*configObject
}

type configFlowState struct {
	inputs  map[*configObject]configInputSet
	aliases map[*configObject]configFunctionAlias
}

type configFunctionAlias struct {
	targets    map[string]struct{}
	unresolved bool
}

type configCall struct {
	line       int
	targets    []string
	unresolved bool
	args       []ast.Expr
	inputs     map[*configObject]configInputSet
}

func cloneConfigFlowState(in configFlowState) configFlowState {
	out := configFlowState{inputs: make(map[*configObject]configInputSet, len(in.inputs)), aliases: make(map[*configObject]configFunctionAlias, len(in.aliases))}
	for object, set := range in.inputs {
		out.inputs[object] = cloneConfigInputSet(set)
	}
	for object, alias := range in.aliases {
		out.aliases[object] = cloneConfigFunctionAlias(alias)
	}
	return out
}

func cloneConfigFunctionAlias(in configFunctionAlias) configFunctionAlias {
	out := configFunctionAlias{targets: make(map[string]struct{}, len(in.targets)), unresolved: in.unresolved}
	for target := range in.targets {
		out.targets[target] = struct{}{}
	}
	return out
}

func mergeConfigFlowStates(left, right configFlowState) configFlowState {
	out := cloneConfigFlowState(left)
	for object, set := range right.inputs {
		out.inputs[object] = unionConfigInputSets(out.inputs[object], set)
	}
	for object, alias := range right.aliases {
		merged := out.aliases[object]
		if merged.targets == nil {
			merged.targets = map[string]struct{}{}
		}
		for target := range alias.targets {
			merged.targets[target] = struct{}{}
		}
		merged.unresolved = merged.unresolved || alias.unresolved
		out.aliases[object] = merged
	}
	return out
}

// configExprInputs evaluates lexical provenance. It recursively reads selector
// bases and unions children, so callers cannot hide a parameter behind an alias,
// options struct, composite value, or ordinary expression shape.
func configExprInputs(expr ast.Expr, inputs map[*configObject]configInputSet) configInputSet {
	if expr == nil {
		return configInputSet{}
	}
	switch value := expr.(type) {
	case *ast.Ident:
		return cloneConfigInputSet(inputs[configObjectFor(value)])
	case *ast.SelectorExpr:
		return configExprInputs(value.X, inputs)
	case *ast.ParenExpr:
		return configExprInputs(value.X, inputs)
	case *ast.UnaryExpr:
		return configExprInputs(value.X, inputs)
	case *ast.StarExpr:
		return configExprInputs(value.X, inputs)
	case *ast.IndexExpr:
		return unionConfigInputSets(configExprInputs(value.X, inputs), configExprInputs(value.Index, inputs))
	case *ast.IndexListExpr:
		sets := []configInputSet{configExprInputs(value.X, inputs)}
		for _, index := range value.Indices {
			sets = append(sets, configExprInputs(index, inputs))
		}
		return unionConfigInputSets(sets...)
	case *ast.SliceExpr:
		return unionConfigInputSets(configExprInputs(value.X, inputs), configExprInputs(value.Low, inputs), configExprInputs(value.High, inputs), configExprInputs(value.Max, inputs))
	case *ast.CallExpr:
		sets := make([]configInputSet, 0, len(value.Args)+1)
		sets = append(sets, configExprInputs(value.Fun, inputs))
		for _, arg := range value.Args {
			sets = append(sets, configExprInputs(arg, inputs))
		}
		return unionConfigInputSets(sets...)
	case *ast.CompositeLit:
		sets := make([]configInputSet, 0, len(value.Elts))
		for _, element := range value.Elts {
			sets = append(sets, configNodeInputs(element, inputs))
		}
		return unionConfigInputSets(sets...)
	case *ast.BinaryExpr:
		return unionConfigInputSets(configExprInputs(value.X, inputs), configExprInputs(value.Y, inputs))
	case *ast.KeyValueExpr:
		return unionConfigInputSets(configExprInputs(value.Key, inputs), configExprInputs(value.Value, inputs))
	}
	return configInputSet{}
}

func configNodeInputs(node ast.Node, inputs map[*configObject]configInputSet) configInputSet {
	expr, ok := node.(ast.Expr)
	if !ok {
		return configInputSet{}
	}
	return configExprInputs(expr, inputs)
}

func isStructuralConfigConstructor(lit *ast.CompositeLit) (home, profile ast.Expr, ok bool) {
	ident, ok := lit.Type.(*ast.Ident)
	if !ok || ident.Name != "Config" {
		return nil, nil, false
	}
	var hasHome, hasProfile, hasSystemPaths bool
	for _, element := range lit.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nil, nil, false
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Home":
			home, hasHome = field.Value, true
		case "Profile":
			profile, hasProfile = field.Value, true
		case "SystemPaths":
			hasSystemPaths = true
		}
	}
	return home, profile, hasHome && hasProfile && hasSystemPaths
}

func collectConfigResolverDecls(dir string) (map[string]configResolverDecl, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	fset := token.NewFileSet()
	decls := map[string]configResolverDecl{}
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
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			params := []*configObject{}
			if fn.Type.Params != nil {
				for _, field := range fn.Type.Params.List {
					for _, parameter := range field.Names {
						params = append(params, configObjectFor(parameter))
					}
				}
			}
			name := qualifiedDeclName(fn)
			key := entry.Name() + ":" + name
			decls[key] = configResolverDecl{file: entry.Name(), fset: fset, decl: fn, key: key, name: name, params: params}
		}
	}
	return decls, parsed, nil
}

func configDirectTarget(decls map[string]configResolverDecl, name string) (string, bool) {
	var target string
	for key, decl := range decls {
		if decl.name != name {
			continue
		}
		if target != "" {
			return "", false
		}
		target = key
	}
	return target, target != ""
}

func configInitialState(decl configResolverDecl) configFlowState {
	state := configFlowState{inputs: map[*configObject]configInputSet{}, aliases: map[*configObject]configFunctionAlias{}}
	for index, object := range decl.params {
		if object != nil {
			state.inputs[object] = configInputSet{index: {}}
		}
	}
	return state
}

func configFunctionAliasFor(expr ast.Expr, state configFlowState, decls map[string]configResolverDecl) (configFunctionAlias, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return configFunctionAlias{}, false
	}
	if alias, ok := state.aliases[configObjectFor(ident)]; ok {
		return cloneConfigFunctionAlias(alias), true
	}
	if target, ok := configDirectTarget(decls, ident.Name); ok {
		return configFunctionAlias{targets: map[string]struct{}{target: {}}}, true
	}
	return configFunctionAlias{}, false
}

func bindConfigIdentifier(left *ast.Ident, right ast.Expr, state *configFlowState, decls map[string]configResolverDecl) {
	object := configObjectFor(left)
	if object == nil {
		return
	}
	state.inputs[object] = configExprInputs(right, state.inputs)
	if alias, ok := configFunctionAliasFor(right, *state, decls); ok {
		state.aliases[object] = alias
	} else if _, wasAlias := state.aliases[object]; wasAlias {
		state.aliases[object] = configFunctionAlias{targets: map[string]struct{}{}, unresolved: true}
	} else {
		delete(state.aliases, object)
	}
}

func configCallFor(call *ast.CallExpr, state configFlowState, decls map[string]configResolverDecl) configCall {
	out := configCall{args: call.Args, inputs: cloneConfigFlowState(state).inputs}
	if ident, ok := call.Fun.(*ast.Ident); ok {
		if alias, ok := state.aliases[configObjectFor(ident)]; ok {
			out.unresolved = alias.unresolved
			for target := range alias.targets {
				out.targets = append(out.targets, target)
			}
		} else if target, ok := configDirectTarget(decls, ident.Name); ok {
			out.targets = []string{target}
		}
	}
	sort.Strings(out.targets)
	return out
}

func configSubstitute(summary configFlowSummary, callee configResolverDecl, call configCall) (configFlowSummary, error) {
	substitute := func(set configInputSet) (configInputSet, error) {
		out := configInputSet{}
		for position := range set {
			if position >= len(call.args) {
				return nil, fmt.Errorf("callee %s summary references parameter %d but call has %d arguments", callee.name, position, len(call.args))
			}
			out = unionConfigInputSets(out, configExprInputs(call.args[position], call.inputs))
		}
		return out, nil
	}
	home, err := substitute(summary.home)
	if err != nil {
		return configFlowSummary{}, err
	}
	profile, err := substitute(summary.profile)
	if err != nil {
		return configFlowSummary{}, err
	}
	return configFlowSummary{home: home, profile: profile}, nil
}

type configDeclAnalysis struct {
	constructors []configFlowSummary
	calls        []configCall
}

func analyzeConfigDecl(decl configResolverDecl, decls map[string]configResolverDecl) configDeclAnalysis {
	analysis := configDeclAnalysis{}
	state := configInitialState(decl)
	var walkExpr func(ast.Expr, configFlowState)
	var walkStatements func([]ast.Stmt, *configFlowState)
	walkExpr = func(expr ast.Expr, current configFlowState) {
		if expr == nil {
			return
		}
		ast.Inspect(expr, func(node ast.Node) bool {
			if node == nil {
				return false
			}
			if _, ok := node.(*ast.FuncLit); ok {
				return false
			}
			switch value := node.(type) {
			case *ast.CompositeLit:
				if home, profile, ok := isStructuralConfigConstructor(value); ok {
					analysis.constructors = append(analysis.constructors, configFlowSummary{home: configExprInputs(home, current.inputs), profile: configExprInputs(profile, current.inputs)})
				}
			case *ast.CallExpr:
				call := configCallFor(value, current, decls)
				call.line = decl.fset.Position(value.Pos()).Line
				if len(call.targets) > 0 || call.unresolved {
					analysis.calls = append(analysis.calls, call)
				}
			}
			return true
		})
	}
	walkStatements = func(statements []ast.Stmt, current *configFlowState) {
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
						if index < len(variable.Values) {
							walkExpr(variable.Values[index], *current)
							bindConfigIdentifier(name, variable.Values[index], current, decls)
						}
					}
				}
			case *ast.AssignStmt:
				for _, right := range value.Rhs {
					walkExpr(right, *current)
				}
				for index, left := range value.Lhs {
					if index >= len(value.Rhs) {
						break
					}
					if ident, ok := left.(*ast.Ident); ok {
						bindConfigIdentifier(ident, value.Rhs[index], current, decls)
					}
				}
			case *ast.ExprStmt:
				walkExpr(value.X, *current)
			case *ast.ReturnStmt:
				for _, result := range value.Results {
					walkExpr(result, *current)
				}
			case *ast.BlockStmt:
				walkStatements(value.List, current)
			case *ast.IfStmt:
				if value.Init != nil {
					walkStatements([]ast.Stmt{value.Init}, current)
				}
				walkExpr(value.Cond, *current)
				thenState := cloneConfigFlowState(*current)
				walkStatements(value.Body.List, &thenState)
				elseState := cloneConfigFlowState(*current)
				if block, ok := value.Else.(*ast.BlockStmt); ok {
					walkStatements(block.List, &elseState)
				} else if nested, ok := value.Else.(*ast.IfStmt); ok {
					walkStatements([]ast.Stmt{nested}, &elseState)
				}
				*current = mergeConfigFlowStates(thenState, elseState)
			case *ast.ForStmt:
				loopState := cloneConfigFlowState(*current)
				walkStatements(value.Body.List, &loopState)
				*current = mergeConfigFlowStates(*current, loopState)
			case *ast.RangeStmt:
				walkExpr(value.X, *current)
				loopState := cloneConfigFlowState(*current)
				walkStatements(value.Body.List, &loopState)
				*current = mergeConfigFlowStates(*current, loopState)
			}
		}
	}
	walkStatements(decl.decl.Body.List, &state)
	return analysis
}

func equalConfigInputSets(left, right configInputSet) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if _, ok := right[index]; !ok {
			return false
		}
	}
	return true
}

func equalConfigFlowSummaries(left, right configFlowSummary) bool {
	return equalConfigInputSets(left.home, right.home) && equalConfigInputSets(left.profile, right.profile)
}

// collectConfigResolverFlows finds the single structural Config constructor,
// seeds its home/profile roles from actual expressions, then propagates those
// roles through every production declaration and simple function-value alias.
func collectConfigResolverFlows(dir string) ([]configFlowFinding, int, error) {
	decls, parsed, err := collectConfigResolverDecls(dir)
	if err != nil {
		return nil, 0, err
	}
	if parsed == 0 {
		return nil, parsed, fmt.Errorf("parsed zero production files in %s: the check would report success without measuring anything", dir)
	}

	type constructor struct {
		decl    configResolverDecl
		summary configFlowSummary
	}
	constructors := []constructor{}
	for _, decl := range decls {
		for _, summary := range analyzeConfigDecl(decl, decls).constructors {
			constructors = append(constructors, constructor{decl: decl, summary: summary})
		}
	}
	if len(constructors) != 1 {
		return nil, parsed, fmt.Errorf("structural Config constructor count = %d, want 1: the check cannot derive a unique home/profile boundary", len(constructors))
	}

	summaries := map[string]configFlowSummary{constructors[0].decl.key: constructors[0].summary}
	for iteration := 0; iteration <= len(decls); iteration++ {
		changed := false
		for name, decl := range decls {
			if name == constructors[0].decl.key {
				continue
			}
			analysis := analyzeConfigDecl(decl, decls)
			var summary configFlowSummary
			known := false
			for _, call := range analysis.calls {
				for _, target := range call.targets {
					targetSummary, ok := summaries[target]
					if !ok {
						continue
					}
					flow, err := configSubstitute(targetSummary, decls[target], call)
					if err != nil {
						return nil, parsed, fmt.Errorf("unresolved Config resolver summary in %s:%d: %w", decl.file, token.NoPos, err)
					}
					summary.home = unionConfigInputSets(summary.home, flow.home)
					summary.profile = unionConfigInputSets(summary.profile, flow.profile)
					known = true
				}
			}
			_, exists := summaries[name]
			if known && (!exists || !equalConfigFlowSummaries(summaries[name], summary)) {
				summaries[name] = summary
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	var findings []configFlowFinding
	for _, decl := range decls {
		analysis := analyzeConfigDecl(decl, decls)
		for _, call := range analysis.calls {
			if call.unresolved {
				return nil, parsed, fmt.Errorf("unresolved Config resolver summary in %s:%d: function-value alias has an unknown target", decl.file, call.line)
			}
			for _, target := range call.targets {
				summary, ok := summaries[target]
				if !ok {
					continue
				}
				flow, err := configSubstitute(summary, decls[target], call)
				if err != nil {
					return nil, parsed, fmt.Errorf("unresolved Config resolver summary in %s:%d: %w", decl.file, call.line, err)
				}
				if (len(flow.home) == 0) == (len(flow.profile) == 0) {
					continue
				}
				derived := "home"
				if len(flow.home) == 0 {
					derived = "profile"
				}
				findings = append(findings, configFlowFinding{file: decl.file, line: call.line, enclosing: decl.name, callee: decls[target].name, derived: derived})
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].String() < findings[j].String() })
	return findings, parsed, nil
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
		{"wrapper_result_partial", `func wrapped(state any, target string) error { _, err := resolveConfig(state, false, target, DefaultProfile); return err }`, "wrapped", "", "home"},
		{"zero_constructor", `func nothing() {}`, "", "structural Config constructor count = 0", ""},
		{"multiple_constructors", `func another(state any, home, profile string) *Config { return &Config{Home: home, Profile: profile, SystemPaths: nil} }`, "", "structural Config constructor count = 2", ""},
		{"unresolved_summary", `func unresolved(state any, target string) (*Config, error) { resolver := resolveConfig; resolver = nil; return resolver(state, false, target, DefaultProfile) }`, "", "unresolved Config resolver summary", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			source := base + tt.source
			if tt.name == "zero_constructor" {
				source = "package syncer\nfunc nothing() {}\n"
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
			if len(findings) != 1 || findings[0].enclosing != tt.want || findings[0].derived != tt.wantDerive {
				t.Fatalf("findings = %v, want one for %s with %s derived", findings, tt.want, tt.wantDerive)
			}
		})
	}

	t.Run("zero_parsed_files", func(t *testing.T) {
		_, _, err := collectConfigResolverFlows(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "parsed zero production files") {
			t.Fatalf("error = %v, want parsed-file non-vacuity failure", err)
		}
	})
}
