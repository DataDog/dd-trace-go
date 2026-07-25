// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type rawReadLocation struct {
	File string
	Func string
}

type rawReadAllowlist map[rawReadLocation]struct{}

const unresolvedAliasIdentity = "unresolved raw-read alias"

func defaultRawReadAllowlist() rawReadAllowlist {
	return rawReadAllowlist{
		{File: "internal/env/env.go", Func: "Get"}:                                                   {},
		{File: "internal/env/env.go", Func: "Lookup"}:                                                {},
		{File: "internal/config/bootstrap/appsec.go", Func: "resolveAppSecStackTrace"}:               {},
		{File: "internal/config/bootstrap/telemetry.go", Func: "TelemetryEnabled"}:                   {},
		{File: "internal/config/bootstrap/testoptimization.go", Func: "resolveTestOptimization"}:     {},
		{File: "internal/civisibility/utils/ci_environment.go", Func: "lookupCIProviderEnvironment"}: {},
		{File: "instrumentation/env/env.go", Func: "Get"}:                                            {},
		{File: "instrumentation/env/env.go", Func: "Lookup"}:                                         {},
		{File: "instrumentation/options/options.go", Func: "GetBoolEnv"}:                             {},
	}
}

// Finding is a raw configuration read that the syntax pass found. A finding is
// unresolved when its key or function-alias identity cannot be proved.
type Finding struct {
	Key        string   `json:"key,omitempty"`
	CallSite   CallSite `json:"call_site"`
	Unresolved bool     `json:"unresolved,omitempty"`
	Suppressed bool     `json:"suppressed,omitempty"`
}

type syntaxFile struct {
	file     *ast.File
	filename string
}

// scanSyntax parses every production Go file in root without applying build
// constraints. Nested modules are excluded because they are outside the
// root-module migration contract.
func scanSyntax(root string, allow rawReadAllowlist) ([]Finding, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root: %w", err)
	}
	module, nested, err := discoverModules(root)
	if err != nil {
		return nil, err
	}
	nestedDirs := make(map[string]struct{}, len(nested))
	for _, child := range nested {
		nestedDirs[child.Dir] = struct{}{}
	}
	fset := token.NewFileSet()
	var files []syntaxFile
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if _, ok := nestedDirs[path]; ok {
				return filepath.SkipDir
			}
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return fmt.Errorf("parsing %s: %w", path, parseErr)
		}
		files = append(files, syntaxFile{file: file, filename: path})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking production files: %w", err)
	}
	packages := make(map[string][]syntaxFile)
	for _, file := range files {
		key := filepath.Dir(file.filename) + "\x00" + file.file.Name.Name
		packages[key] = append(packages[key], file)
	}
	keys := make([]string, 0, len(packages))
	for key := range packages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var findings []Finding
	for _, key := range keys {
		files := packages[key]
		aliases := packageFunctionAliases(files)
		for name, identity := range packageFunctionWrappers(files, aliases) {
			aliases[name] = identity
		}
		for _, file := range files {
			findings = append(findings, syntaxFindings(fset, file.file, file.filename, root, module.Path, allow, aliases)...)
		}
	}
	return findings, nil
}

func syntaxFindings(fset *token.FileSet, file *ast.File, filename, root, modulePath string, allow rawReadAllowlist, packageAliases map[string]string) []Finding {
	imports := importPaths(file)
	constants := stringConstants(file)
	suppressed := syntaxSuppressedLines(fset, file)
	rel, err := filepath.Rel(root, filename)
	if err != nil {
		rel = filename
	}
	rel = filepath.ToSlash(rel)
	pkgPath := modulePath
	if dir := filepath.ToSlash(filepath.Dir(rel)); dir != "." {
		pkgPath += "/" + dir
	}
	var findings []Finding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		scope := newAliasScope(nil)
		for name, identity := range packageAliases {
			scope.bindings[name] = &aliasBinding{identity: identity}
		}
		bindFieldNames(scope, fn.Recv)
		bindFieldNames(scope, fn.Type.Params)
		bindFieldNames(scope, fn.Type.Results)
		state := newSyntaxFunctionState()
		handle := func(call *ast.CallExpr, identity string) {
			if len(call.Args) == 0 {
				return
			}
			if _, ok := allow[rawReadLocation{File: rel, Func: fn.Name.Name}]; ok {
				return
			}
			key, resolved := syntaxStringArg(call.Args[0], constants)
			if resolved && !isConfigKey(key) {
				return
			}
			pos := fset.Position(call.Pos())
			finding := Finding{
				Key: key,
				CallSite: CallSite{
					File:    filename,
					Line:    pos.Line,
					Func:    identity,
					Package: pkgPath,
				},
				Unresolved: !resolved || identity == unresolvedAliasIdentity,
			}
			for line := pos.Line; line <= fset.Position(call.End()).Line; line++ {
				if suppressed[line] {
					finding.Suppressed = true
					break
				}
			}
			findings = append(findings, finding)
		}
		scanSyntaxBlock(fn.Body, scope, imports, handle, state)
		state.flushPending(handle)
	}
	return findings
}

func packageFunctionAliases(files []syntaxFile) map[string]string {
	aliases := map[string]string{}
	for changed := true; changed; {
		changed = false
		for _, file := range files {
			imports := importPaths(file.file)
			for _, decl := range file.file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					values, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range values.Names {
						if i >= len(values.Values) {
							continue
						}
						identity, ok := syntaxCallIdentity(values.Values[i], imports, aliases)
						if !ok {
							continue
						}
						changed = recordDiscoveredIdentity(aliases, name.Name, identity) || changed
					}
				}
			}
		}
	}
	return aliases
}

func packageFunctionWrappers(files []syntaxFile, packageAliases map[string]string) map[string]string {
	wrappers := map[string]string{}
	for changed := true; changed; {
		changed = false
		aliases := make(map[string]string, len(packageAliases)+len(wrappers))
		for name, identity := range packageAliases {
			aliases[name] = identity
		}
		for name, identity := range wrappers {
			aliases[name] = identity
		}
		for _, file := range files {
			imports := importPaths(file.file)
			for _, decl := range file.file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Body == nil {
					continue
				}
				identity, ok := forwardingFunctionIdentity(fn, imports, aliases)
				if !ok {
					continue
				}
				changed = recordDiscoveredIdentity(wrappers, fn.Name.Name, identity) || changed
			}
		}
	}
	return wrappers
}

func recordDiscoveredIdentity(identities map[string]string, name, candidate string) bool {
	identity, exists := identities[name]
	if !exists {
		identities[name] = candidate
		return true
	}
	// Conflicts are absorbing so fixed-point discovery can only move from
	// unseen to concrete to unresolved, never back to a competing identity.
	if identity == candidate || identity == unresolvedAliasIdentity {
		return false
	}
	identities[name] = unresolvedAliasIdentity
	return true
}

func forwardingFunctionIdentity(fn *ast.FuncDecl, imports, aliases map[string]string) (string, bool) {
	var identity string
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if identity != "" {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 || !isFunctionParameter(fn, call.Args[0]) {
			return true
		}
		if resolved, ok := syntaxCallIdentity(call.Fun, imports, aliases); ok {
			identity = resolved
			return false
		}
		return true
	})
	return identity, identity != ""
}

func isFunctionParameter(fn *ast.FuncDecl, expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok || fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == ident.Name {
				return true
			}
		}
	}
	return false
}

func importPaths(file *ast.File) map[string]string {
	imports := make(map[string]string, len(file.Imports))
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = path
	}
	return imports
}

func stringConstants(file *ast.File) map[string]string {
	constants := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range values.Names {
				if i >= len(values.Values) {
					continue
				}
				if value, ok := syntaxStringArg(values.Values[i], constants); ok {
					constants[name.Name] = value
				}
			}
		}
	}
	return constants
}

type aliasScope struct {
	parent   *aliasScope
	bindings map[string]*aliasBinding
}

type aliasBinding struct {
	identity string
}

func newAliasScope(parent *aliasScope) *aliasScope {
	return &aliasScope{parent: parent, bindings: map[string]*aliasBinding{}}
}

func (s *aliasScope) binding(name string) (*aliasBinding, bool) {
	for scope := s; scope != nil; scope = scope.parent {
		binding, ok := scope.bindings[name]
		if ok {
			return binding, true
		}
	}
	return nil, false
}

func (s *aliasScope) identity(name string) (string, bool) {
	binding, ok := s.binding(name)
	if !ok {
		return "", false
	}
	return binding.identity, binding.identity != ""
}

func (s *aliasScope) assign(name, identity string) *aliasBinding {
	if binding, ok := s.binding(name); ok {
		binding.identity = identity
		return binding
	}
	binding := &aliasBinding{identity: identity}
	s.bindings[name] = binding
	return binding
}

type aliasBindingSnapshot struct {
	binding  *aliasBinding
	identity string
}

type aliasSnapshot map[*aliasScope]map[string]aliasBindingSnapshot

func snapshotAliasScopes(scope *aliasScope) aliasSnapshot {
	snapshot := aliasSnapshot{}
	for current := scope; current != nil; current = current.parent {
		bindings := make(map[string]aliasBindingSnapshot, len(current.bindings))
		for name, binding := range current.bindings {
			bindings[name] = aliasBindingSnapshot{binding: binding, identity: binding.identity}
		}
		snapshot[current] = bindings
	}
	return snapshot
}

func restoreAliasScopes(snapshot aliasSnapshot) {
	for scope, bindings := range snapshot {
		scope.bindings = make(map[string]*aliasBinding, len(bindings))
		for name, state := range bindings {
			state.binding.identity = state.identity
			scope.bindings[name] = state.binding
		}
	}
}

func mergeAliasOutcomes(baseline aliasSnapshot, outcomes ...aliasSnapshot) {
	for scope, bindings := range baseline {
		for name := range bindings {
			readers := map[string]struct{}{}
			for _, outcome := range outcomes {
				if identity := outcome[scope][name].identity; identity != "" {
					readers[identity] = struct{}{}
				}
			}
			binding := bindings[name].binding
			switch len(readers) {
			case 0:
				binding.identity = ""
			case 1:
				for identity := range readers {
					binding.identity = identity
				}
			default:
				binding.identity = unresolvedAliasIdentity
			}
		}
	}
}

func bindFieldNames(scope *aliasScope, fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			if name.Name != "_" {
				scope.bindings[name.Name] = &aliasBinding{}
			}
		}
	}
}

type syntaxCallHandler func(call *ast.CallExpr, identity string)

type pendingAliasCall struct {
	call    *ast.CallExpr
	binding *aliasBinding
}

type syntaxFunctionState struct {
	mayReaders   map[*aliasBinding]string
	captured     map[*aliasBinding]struct{}
	pendingCalls []pendingAliasCall
	closureDepth int
}

func newSyntaxFunctionState() *syntaxFunctionState {
	return &syntaxFunctionState{
		mayReaders: map[*aliasBinding]string{},
		captured:   map[*aliasBinding]struct{}{},
	}
}

func (s *syntaxFunctionState) addMayReader(binding *aliasBinding, identity string) {
	if identity == "" {
		return
	}
	current, exists := s.mayReaders[binding]
	if !exists {
		s.mayReaders[binding] = identity
		return
	}
	if current != identity {
		s.mayReaders[binding] = unresolvedAliasIdentity
	}
}

func (s *syntaxFunctionState) noteAssignment(binding *aliasBinding, identity string) {
	if _, captured := s.captured[binding]; captured || s.closureDepth > 0 {
		s.addMayReader(binding, identity)
	}
}

func (s *syntaxFunctionState) noteBranchExit(scope *aliasScope) {
	seen := map[string]struct{}{}
	for current := scope; current != nil; current = current.parent {
		for name, binding := range current.bindings {
			if _, shadowed := seen[name]; shadowed {
				continue
			}
			seen[name] = struct{}{}
			s.addMayReader(binding, binding.identity)
		}
	}
}

func (s *syntaxFunctionState) flushPending(handle syntaxCallHandler) {
	for _, pending := range s.pendingCalls {
		if identity := s.mayReaders[pending.binding]; identity != "" {
			handle(pending.call, identity)
		}
	}
}

func scanSyntaxBlock(block *ast.BlockStmt, parent *aliasScope, imports map[string]string, handle syntaxCallHandler, state *syntaxFunctionState) {
	scope := newAliasScope(parent)
	for _, stmt := range block.List {
		scanSyntaxStmt(stmt, scope, imports, handle, state)
	}
}

func scanSyntaxStmt(stmt ast.Stmt, scope *aliasScope, imports map[string]string, handle syntaxCallHandler, state *syntaxFunctionState) {
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		for _, expr := range stmt.Rhs {
			scanSyntaxExpr(expr, scope, imports, handle, state)
		}
		for _, expr := range stmt.Lhs {
			scanSyntaxExpr(expr, scope, imports, handle, state)
		}
		applyAliasAssignment(stmt, scope, imports, state)
	case *ast.DeclStmt:
		gen, ok := stmt.Decl.(*ast.GenDecl)
		if !ok {
			return
		}
		for _, spec := range gen.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, expr := range values.Values {
				scanSyntaxExpr(expr, scope, imports, handle, state)
			}
			for i, name := range values.Names {
				if name.Name == "_" {
					continue
				}
				identity := ""
				if len(values.Values) == len(values.Names) {
					identity, _ = syntaxExprIdentity(values.Values[i], imports, scope)
				}
				binding := &aliasBinding{identity: identity}
				scope.bindings[name.Name] = binding
				state.noteAssignment(binding, identity)
			}
		}
	case *ast.ExprStmt:
		scanSyntaxExpr(stmt.X, scope, imports, handle, state)
	case *ast.ReturnStmt:
		for _, expr := range stmt.Results {
			scanSyntaxExpr(expr, scope, imports, handle, state)
		}
	case *ast.BlockStmt:
		scanSyntaxBlock(stmt, scope, imports, handle, state)
	case *ast.IfStmt:
		control := newAliasScope(scope)
		if stmt.Init != nil {
			scanSyntaxStmt(stmt.Init, control, imports, handle, state)
		}
		scanSyntaxExpr(stmt.Cond, control, imports, handle, state)
		baseline := snapshotAliasScopes(control)
		scanSyntaxBlock(stmt.Body, control, imports, handle, state)
		outcomes := []aliasSnapshot{snapshotAliasScopes(control)}
		restoreAliasScopes(baseline)
		if stmt.Else != nil {
			scanSyntaxStmt(stmt.Else, control, imports, handle, state)
			outcomes = append(outcomes, snapshotAliasScopes(control))
		} else {
			outcomes = append(outcomes, baseline)
		}
		restoreAliasScopes(baseline)
		mergeAliasOutcomes(baseline, outcomes...)
	case *ast.ForStmt:
		control := newAliasScope(scope)
		if stmt.Init != nil {
			scanSyntaxStmt(stmt.Init, control, imports, handle, state)
		}
		scanSyntaxExpr(stmt.Cond, control, imports, handle, state)
		baseline := snapshotAliasScopes(control)
		scanSyntaxBlock(stmt.Body, control, imports, handle, state)
		if stmt.Post != nil {
			scanSyntaxStmt(stmt.Post, control, imports, handle, state)
		}
		iterated := snapshotAliasScopes(control)
		restoreAliasScopes(baseline)
		mergeAliasOutcomes(baseline, baseline, iterated)
	case *ast.RangeStmt:
		scanSyntaxExpr(stmt.X, scope, imports, handle, state)
		control := newAliasScope(scope)
		applyRangeBindings(stmt, control, state)
		baseline := snapshotAliasScopes(control)
		scanSyntaxBlock(stmt.Body, control, imports, handle, state)
		iterated := snapshotAliasScopes(control)
		restoreAliasScopes(baseline)
		mergeAliasOutcomes(baseline, baseline, iterated)
	case *ast.SwitchStmt:
		control := newAliasScope(scope)
		if stmt.Init != nil {
			scanSyntaxStmt(stmt.Init, control, imports, handle, state)
		}
		scanSyntaxExpr(stmt.Tag, control, imports, handle, state)
		scanSyntaxCaseClauses(stmt.Body, control, imports, handle, state)
	case *ast.TypeSwitchStmt:
		control := newAliasScope(scope)
		if stmt.Init != nil {
			scanSyntaxStmt(stmt.Init, control, imports, handle, state)
		}
		scanSyntaxStmt(stmt.Assign, control, imports, handle, state)
		scanSyntaxCaseClauses(stmt.Body, control, imports, handle, state)
	case *ast.SelectStmt:
		baseline := snapshotAliasScopes(scope)
		var outcomes []aliasSnapshot
		for _, rawClause := range stmt.Body.List {
			restoreAliasScopes(baseline)
			clause := rawClause.(*ast.CommClause)
			clauseScope := newAliasScope(scope)
			if clause.Comm != nil {
				scanSyntaxStmt(clause.Comm, clauseScope, imports, handle, state)
			}
			for _, bodyStmt := range clause.Body {
				scanSyntaxStmt(bodyStmt, clauseScope, imports, handle, state)
			}
			outcomes = append(outcomes, snapshotAliasScopes(scope))
		}
		restoreAliasScopes(baseline)
		mergeAliasOutcomes(baseline, append(outcomes, baseline)...)
	case *ast.GoStmt:
		scanSyntaxExpr(stmt.Call, scope, imports, handle, state)
	case *ast.DeferStmt:
		scanSyntaxExpr(stmt.Call, scope, imports, handle, state)
	case *ast.SendStmt:
		scanSyntaxExpr(stmt.Chan, scope, imports, handle, state)
		scanSyntaxExpr(stmt.Value, scope, imports, handle, state)
	case *ast.IncDecStmt:
		scanSyntaxExpr(stmt.X, scope, imports, handle, state)
	case *ast.BranchStmt:
		switch stmt.Tok {
		case token.BREAK, token.CONTINUE, token.GOTO:
			state.noteBranchExit(scope)
		}
	case *ast.LabeledStmt:
		scanSyntaxStmt(stmt.Stmt, scope, imports, handle, state)
	}
}

func scanSyntaxCaseClauses(body *ast.BlockStmt, scope *aliasScope, imports map[string]string, handle syntaxCallHandler, state *syntaxFunctionState) {
	baseline := snapshotAliasScopes(scope)
	var outcomes []aliasSnapshot
	var fallthroughState aliasSnapshot
	for _, rawClause := range body.List {
		restoreAliasScopes(baseline)
		clause := rawClause.(*ast.CaseClause)
		if fallthroughState != nil {
			mergeAliasOutcomes(baseline, baseline, fallthroughState)
		}
		for _, expr := range clause.List {
			scanSyntaxExpr(expr, scope, imports, handle, state)
		}
		clauseScope := newAliasScope(scope)
		for _, stmt := range clause.Body {
			scanSyntaxStmt(stmt, clauseScope, imports, handle, state)
		}
		outcome := snapshotAliasScopes(scope)
		outcomes = append(outcomes, outcome)
		if caseFallsThrough(clause) {
			fallthroughState = outcome
		} else {
			fallthroughState = nil
		}
	}
	restoreAliasScopes(baseline)
	mergeAliasOutcomes(baseline, append(outcomes, baseline)...)
}

func caseFallsThrough(clause *ast.CaseClause) bool {
	if len(clause.Body) == 0 {
		return false
	}
	branch, ok := clause.Body[len(clause.Body)-1].(*ast.BranchStmt)
	return ok && branch.Tok == token.FALLTHROUGH
}

func scanSyntaxExpr(expr ast.Expr, scope *aliasScope, imports map[string]string, handle syntaxCallHandler, state *syntaxFunctionState) {
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FuncLit:
			baseline := snapshotAliasScopes(scope)
			literalScope := newAliasScope(scope)
			bindFieldNames(literalScope, node.Type.Params)
			bindFieldNames(literalScope, node.Type.Results)
			state.closureDepth++
			scanSyntaxBlock(node.Body, literalScope, imports, handle, state)
			state.closureDepth--
			restoreAliasScopes(baseline)
			return false
		case *ast.CallExpr:
			if identity, ok := syntaxExprIdentity(node.Fun, imports, scope); ok {
				handle(node, identity)
				return true
			}
			ident, ok := node.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			binding, ok := scope.binding(ident.Name)
			if !ok {
				return true
			}
			state.pendingCalls = append(state.pendingCalls, pendingAliasCall{call: node, binding: binding})
			if state.closureDepth > 0 {
				state.captured[binding] = struct{}{}
				state.addMayReader(binding, binding.identity)
			}
		}
		return true
	})
}

func applyAliasAssignment(stmt *ast.AssignStmt, scope *aliasScope, imports map[string]string, state *syntaxFunctionState) {
	for i, lhs := range stmt.Lhs {
		name, ok := lhs.(*ast.Ident)
		if !ok || name.Name == "_" {
			continue
		}
		identity := ""
		if len(stmt.Lhs) == len(stmt.Rhs) {
			identity, _ = syntaxExprIdentity(stmt.Rhs[i], imports, scope)
		}
		if stmt.Tok == token.DEFINE {
			binding, exists := scope.bindings[name.Name]
			if !exists {
				binding = &aliasBinding{}
				scope.bindings[name.Name] = binding
			}
			binding.identity = identity
			state.noteAssignment(binding, identity)
			continue
		}
		binding := scope.assign(name.Name, identity)
		state.noteAssignment(binding, identity)
	}
}

func applyRangeBindings(stmt *ast.RangeStmt, scope *aliasScope, state *syntaxFunctionState) {
	for _, expr := range []ast.Expr{stmt.Key, stmt.Value} {
		name, ok := expr.(*ast.Ident)
		if !ok || name.Name == "_" {
			continue
		}
		if stmt.Tok == token.DEFINE {
			binding := &aliasBinding{}
			scope.bindings[name.Name] = binding
			state.noteAssignment(binding, "")
		} else {
			binding := scope.assign(name.Name, "")
			state.noteAssignment(binding, "")
		}
	}
}

func syntaxExprIdentity(expr ast.Expr, imports map[string]string, scope *aliasScope) (string, bool) {
	if ident, ok := expr.(*ast.Ident); ok {
		return scope.identity(ident.Name)
	}
	return syntaxCallIdentity(expr, imports, nil)
}

func syntaxCallIdentity(expr ast.Expr, imports, aliases map[string]string) (string, bool) {
	switch expr := expr.(type) {
	case *ast.Ident:
		if identity, ok := aliases[expr.Name]; ok {
			return identity, true
		}
	case *ast.SelectorExpr:
		pkg, ok := expr.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		path := imports[pkg.Name]
		if path == "os" && (expr.Sel.Name == "Getenv" || expr.Sel.Name == "LookupEnv") {
			return "os." + expr.Sel.Name, true
		}
		if isKnownReader(path, expr.Sel.Name) {
			return path + "." + expr.Sel.Name, true
		}
	}
	return "", false
}

func isKnownReader(path, name string) bool {
	if path == "github.com/DataDog/dd-trace-go/v2/internal/env" || path == "github.com/DataDog/dd-trace-go/v2/instrumentation/env" {
		return name == "Get" || name == "Lookup"
	}
	if path == "github.com/DataDog/dd-trace-go/v2/internal" {
		switch name {
		case "BoolEnv", "BoolEnvNoDefault", "IntEnv", "FloatEnv", "DurationEnv", "DurationEnvWithUnit":
			return true
		}
	}
	if path == "github.com/DataDog/dd-trace-go/v2/internal/stableconfig" {
		switch name {
		case "Bool", "String", "Int", "Float":
			return true
		}
	}
	return false
}

func syntaxStringArg(expr ast.Expr, constants map[string]string) (string, bool) {
	switch expr := expr.(type) {
	case *ast.BasicLit:
		if expr.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(expr.Value)
		return value, err == nil
	case *ast.Ident:
		value, ok := constants[expr.Name]
		return value, ok
	case *ast.BinaryExpr:
		if expr.Op != token.ADD {
			return "", false
		}
		left, leftOK := syntaxStringArg(expr.X, constants)
		right, rightOK := syntaxStringArg(expr.Y, constants)
		if leftOK && rightOK {
			return left + right, true
		}
		if (leftOK && isConfigKeyPrefix(left)) || (rightOK && isConfigKeyPrefix(right)) {
			return "", false
		}
	}
	return "", false
}

func syntaxSuppressedLines(fset *token.FileSet, file *ast.File) map[int]bool {
	lines := map[int]bool{}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if hasNolintConfigaudit(comment.Text) {
				lines[fset.Position(comment.Pos()).Line] = true
			}
		}
	}
	return lines
}

func isConfigKey(key string) bool {
	return isConfigKeyPrefix(key)
}

func isConfigKeyPrefix(key string) bool {
	return strings.HasPrefix(key, "DD_") || strings.HasPrefix(key, "DD-") || strings.HasPrefix(key, "OTEL_")
}
