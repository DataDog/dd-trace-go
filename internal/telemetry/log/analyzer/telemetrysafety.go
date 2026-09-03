// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// telemetrysafety.go replaces the ruleguard rules formerly in
// rules/telemetry_rules.go (telemetryLogSmartSlogAny, telemetryLogStringErrorCall,
// telemetryLogRawErrorUsage), which required golangci-lint's gocritic/ruleguard
// integration. Folding them into this go/analysis pass means the SDK's own
// error-reporting API (this package) is checked by the same standalone
// `make lint/errlog` tool as the constant-message rule, with one less moving
// part in CI.
package analyzer

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const telemetrySafetyDoc = `telemetrysafety enforces PII-safety rules on internal/telemetry/log calls:

  - slog.Any(key, value): value must implement slog.LogValuer (e.g. SafeError,
    SafeSlice) or be a nil literal. A value that merely implements error is
    called out specifically: wrap it with NewSafeError first.
  - slog.String(key, err.Error()): forbidden when err implements error — the
    raw error message bypasses redaction. Use slog.Any(key, NewSafeError(err)).

These replace the ruleguard rules in the retired rules/telemetry_rules.go
(telemetryLogSmartSlogAny, telemetryLogStringErrorCall, telemetryLogRawErrorUsage).

An attr is checked whether it's built inline (slog.Any(...) as the log-call
argument) or hoisted into a variable first, as long as the assignment is in
the same package as the log call. Not followed, deliberately — the pass does
no dataflow analysis, so it never guesses which value reaches a log call:

  - attrs returned from a helper function: attr := buildAttr(x)
  - []slog.Attr slices, including spread calls: log.Error(msg, attrs...)
  - attrs read from struct fields, map/slice elements, or channels
  - attrs assigned in a different package from the log call
  - attrs copied through another variable: b := a; log.Error(msg, b)

Every assignment to a hoisted variable is checked independently, so an if/else
that only makes one branch unsafe still gets flagged. A //nolint suppressing a
hoisted attr must sit on the slog.Any/slog.String line, not the log-call line.`

// telemetryLogFuncNames are the message-emitting entry points checked: the
// package-level functions and the identically-named *Logger methods.
var telemetryLogFuncNames = map[string]bool{"Debug": true, "Warn": true, "Error": true}

// TelemetrySafetyAnalyzer is the production analyzer, scoped to
// internal/telemetry/log; it skips that package's own files (see New's doc).
var TelemetrySafetyAnalyzer = NewTelemetrySafety(telemetryLogPkg, telemetryLogPkg)

// NewTelemetrySafety returns an analyzer that checks slog.Any/slog.String
// arguments reaching logPkg's Debug/Warn/Error functions and Logger methods,
// inline or hoisted into a variable first — see telemetrySafetyDoc for the
// exact scope and its limitations. skipPkg's own files are not analyzed —
// internal/telemetry/log's
// implementation builds these slog.Attr values itself (e.g. forward.go,
// helpers.go) using NewSafeError directly rather than through logPkg's public
// entry points, so there is nothing for this analyzer to see there, but the
// skip keeps the intent explicit and matches Analyzer's convention.
func NewTelemetrySafety(logPkg, skipPkg string) *analysis.Analyzer {
	r := &telemetrySafetyRunner{logPkg: logPkg, skipPkg: skipPkg}
	return &analysis.Analyzer{
		Name:     "telemetrysafety",
		Doc:      telemetrySafetyDoc,
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run:      r.run,
	}
}

type telemetrySafetyRunner struct {
	logPkg  string
	skipPkg string
}

// safetyPass holds the state for a single run over one package. It cannot
// live on telemetrySafetyRunner: analysis.Analyzer.Run is invoked
// concurrently across packages by the multichecker driver, and
// telemetrySafetyRunner is one value shared by every call.
type safetyPass struct {
	pass           *analysis.Pass
	ins            *inspector.Inspector
	errIface       *types.Interface
	logValuerIface *types.Interface

	// attrAssigns maps a variable to every log/slog constructor call assigned
	// to it anywhere in this package. Built lazily by attrAssignments — most
	// packages never call the telemetry logger, so most never pay for the
	// extra traversal.
	attrAssigns map[types.Object][]*ast.CallExpr
	built       bool

	// checked marks the slog value expressions already inspected, so a
	// hoisted attr reaching more than one log call is reported once.
	checked map[ast.Expr]bool
}

func (r *telemetrySafetyRunner) run(pass *analysis.Pass) (any, error) {
	if pass.Pkg.Path() == r.skipPkg {
		return nil, nil
	}

	sp := &safetyPass{
		pass:           pass,
		ins:            pass.ResultOf[inspect.Analyzer].(*inspector.Inspector),
		errIface:       errorInterface(),
		logValuerIface: lookupInterface(pass.Pkg, "log/slog", "LogValuer"),
		checked:        make(map[ast.Expr]bool),
	}

	sp.ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		fn, pkg := resolveFunc(pass, call)
		if pkg != r.logPkg || !telemetryLogFuncNames[fn] || len(call.Args) < 2 {
			return
		}

		// Args[0] is the message; the rest are structured attrs — either an
		// inline slog.Any/slog.String call, or a variable holding one.
		for _, arg := range call.Args[1:] {
			r.checkAttrArg(sp, arg)
		}
	})

	return nil, nil
}

// checkAttrArg inspects one attr argument of a telemetry log call. An inline
// slog.Any/slog.String call is checked directly; a bare identifier is
// resolved through this package's assignment index so hoisted attrs are
// covered too. Anything else (selector expressions, helper-call results,
// slice spreads) is out of scope — see NewTelemetrySafety's doc comment.
func (r *telemetrySafetyRunner) checkAttrArg(sp *safetyPass, arg ast.Expr) {
	switch e := ast.Unparen(arg).(type) {
	case *ast.CallExpr:
		r.checkSlogCall(sp, e)
	case *ast.Ident:
		obj := sp.pass.TypesInfo.ObjectOf(e)
		if obj == nil {
			return
		}
		for _, call := range sp.attrAssignments()[obj] {
			r.checkSlogCall(sp, call)
		}
	}
}

// checkSlogCall applies the slog.Any/slog.String rules to call, which is
// either an inline log-call argument or the RHS of an assignment to a
// hoisted attr variable.
func (r *telemetrySafetyRunner) checkSlogCall(sp *safetyPass, call *ast.CallExpr) {
	fn, pkg := resolveFunc(sp.pass, call)
	if pkg != "log/slog" || len(call.Args) < 2 {
		return
	}
	value := call.Args[1]
	if sp.checked[value] {
		return // hoisted attr reaching more than one log call: report once
	}
	sp.checked[value] = true

	switch fn {
	case "Any":
		r.checkSlogAny(sp.pass, value, sp.errIface, sp.logValuerIface)
	case "String":
		r.checkSlogString(sp.pass, value, sp.errIface)
	}
}

// attrAssignments returns the lazily built index from a variable to every
// log/slog call assigned to it anywhere in this package.
//
// Only assignments whose RHS is literally a slog constructor call are
// recorded — a deliberately shallow stand-in for dataflow analysis, so the
// pass can never be wrong about which value reaches a log call. Every
// assignment is recorded, so both branches of
//
//	var errAttr slog.Attr
//	if err, ok := r.(error); ok {
//		errAttr = slog.Any("panic", NewSafeError(err))
//	} else {
//		errAttr = slog.Any("panic", r)
//	}
//
// are checked independently.
func (sp *safetyPass) attrAssignments() map[types.Object][]*ast.CallExpr {
	if sp.built {
		return sp.attrAssigns
	}
	sp.built = true
	sp.attrAssigns = make(map[types.Object][]*ast.CallExpr)

	record := func(lhs, rhs ast.Expr) {
		ident, ok := lhs.(*ast.Ident)
		if !ok || ident.Name == "_" {
			return
		}
		call, ok := ast.Unparen(rhs).(*ast.CallExpr)
		if !ok {
			return
		}
		if _, pkg := resolveFunc(sp.pass, call); pkg != "log/slog" {
			return
		}
		obj := sp.pass.TypesInfo.ObjectOf(ident)
		if obj == nil {
			return
		}
		sp.attrAssigns[obj] = append(sp.attrAssigns[obj], call)
	}

	sp.ins.Preorder([]ast.Node{(*ast.AssignStmt)(nil), (*ast.ValueSpec)(nil)}, func(n ast.Node) {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if node.Tok != token.ASSIGN && node.Tok != token.DEFINE {
				return // compound assignment (+=, etc.) can't produce an attr
			}
			if len(node.Lhs) != len(node.Rhs) {
				return // multi-value RHS (a, b := f()) has no per-name expression
			}
			for i, lhs := range node.Lhs {
				record(lhs, node.Rhs[i])
			}
		case *ast.ValueSpec:
			if len(node.Names) != len(node.Values) {
				return // `var x slog.Attr` (no value) or multi-value RHS
			}
			for i, name := range node.Names {
				record(name, node.Values[i])
			}
		}
	})

	return sp.attrAssigns
}

func (r *telemetrySafetyRunner) checkSlogAny(pass *analysis.Pass, value ast.Expr, errIface, logValuerIface *types.Interface) {
	if isNilLiteral(pass, value) || nolintSuppressed(pass, value.Pos(), "gocritic", "telemetrysafety") {
		return
	}
	t := pass.TypesInfo.TypeOf(value)
	if t == nil {
		return
	}
	if logValuerIface != nil && types.Implements(t, logValuerIface) {
		// Only exempt when the exact type passed implements LogValuer.
		// A pointer-receiver LogValue method only satisfies the interface on
		// *T, not T: slog.Any boxes exactly the type passed, so if the caller
		// passed a bare T, slog cannot dispatch LogValue at runtime and falls
		// back to reflecting over the raw value. Checking types.NewPointer(t)
		// here would wrongly exempt that case just because *T happens to
		// implement the interface — the caller must pass a pointer explicitly.
		return // already safe: SafeError, SafeSlice, or a caller-provided LogValuer
	}
	if errIface != nil && types.Implements(t, errIface) {
		pass.Reportf(value.Pos(),
			"telemetry logging: raw error value (%s) passed to slog.Any exposes its message via reflection; wrap it first: slog.Any(key, NewSafeError(err))", t.String())
		return
	}
	pass.Reportf(value.Pos(),
		"telemetry logging: slog.Any value of type %s does not implement slog.LogValuer and may leak data via reflection; use an explicit slog.<Type>() helper or implement LogValuer", t.String())
}

func (r *telemetrySafetyRunner) checkSlogString(pass *analysis.Pass, value ast.Expr, errIface *types.Interface) {
	call, ok := value.(*ast.CallExpr)
	if !ok {
		return
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Error" || len(call.Args) != 0 {
		return
	}
	recvType := pass.TypesInfo.TypeOf(sel.X)
	if recvType == nil || errIface == nil || !types.Implements(recvType, errIface) {
		return
	}
	if nolintSuppressed(pass, value.Pos(), "gocritic", "telemetrysafety") {
		return
	}
	pass.Reportf(value.Pos(),
		"telemetry logging: slog.String with err.Error() exposes the raw error message; use slog.Any(key, NewSafeError(err)) instead")
}

// isNilLiteral reports whether e is the predeclared nil identifier — not
// merely an identifier spelled "nil", which Go permits shadowing (e.g.
// `nil := customerData`).
func isNilLiteral(pass *analysis.Pass, e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	if !ok || ident.Name != "nil" {
		return false
	}
	return pass.TypesInfo.Uses[ident] == types.Universe.Lookup("nil")
}

// errorInterface returns the predeclared "error" interface type.
func errorInterface() *types.Interface {
	iface, _ := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
	return iface
}

// lookupInterface finds the named interface type in the package identified by
// importPath, searching pkg's import graph (direct and transitive). Returns
// nil if the package or interface can't be found — callers treat that as "no
// LogValuer-style check possible" rather than failing.
func lookupInterface(pkg *types.Package, importPath, name string) *types.Interface {
	target := findImportedPkg(pkg, importPath, map[*types.Package]bool{})
	if target == nil {
		return nil
	}
	obj := target.Scope().Lookup(name)
	if obj == nil {
		return nil
	}
	iface, _ := obj.Type().Underlying().(*types.Interface)
	return iface
}

func findImportedPkg(pkg *types.Package, importPath string, seen map[*types.Package]bool) *types.Package {
	if pkg == nil || seen[pkg] {
		return nil
	}
	seen[pkg] = true
	if pkg.Path() == importPath {
		return pkg
	}
	for _, imp := range pkg.Imports() {
		if found := findImportedPkg(imp, importPath, seen); found != nil {
			return found
		}
	}
	return nil
}
