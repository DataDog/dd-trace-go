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
(telemetryLogSmartSlogAny, telemetryLogStringErrorCall, telemetryLogRawErrorUsage).`

// telemetryLogFuncNames are the message-emitting entry points checked: the
// package-level functions and the identically-named *Logger methods.
var telemetryLogFuncNames = map[string]bool{"Debug": true, "Warn": true, "Error": true}

// TelemetrySafetyAnalyzer is the production analyzer, scoped to
// internal/telemetry/log; it skips that package's own files (see New's doc).
var TelemetrySafetyAnalyzer = NewTelemetrySafety(telemetryLogPkg, telemetryLogPkg)

// NewTelemetrySafety returns an analyzer that checks slog.Any/slog.String
// arguments passed directly to logPkg's Debug/Warn/Error functions and Logger
// methods. skipPkg's own files are not analyzed — internal/telemetry/log's
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

func (r *telemetrySafetyRunner) run(pass *analysis.Pass) (any, error) {
	if pass.Pkg.Path() == r.skipPkg {
		return nil, nil
	}

	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	errIface := errorInterface()
	logValuerIface := lookupInterface(pass.Pkg, "log/slog", "LogValuer")

	ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		fn, pkg := resolveFunc(pass, call)
		if pkg != r.logPkg || !telemetryLogFuncNames[fn] || len(call.Args) < 2 {
			return
		}

		// Args[0] is the message; only structured attrs (slog.Any/slog.String
		// calls passed directly as arguments) are inspected.
		for _, arg := range call.Args[1:] {
			inner, ok := arg.(*ast.CallExpr)
			if !ok {
				continue
			}
			innerFn, innerPkg := resolveFunc(pass, inner)
			if innerPkg != "log/slog" || len(inner.Args) < 2 {
				continue
			}
			switch innerFn {
			case "Any":
				r.checkSlogAny(pass, inner.Args[1], errIface, logValuerIface)
			case "String":
				r.checkSlogString(pass, inner.Args[1], errIface)
			}
		}
	})

	return nil, nil
}

func (r *telemetrySafetyRunner) checkSlogAny(pass *analysis.Pass, value ast.Expr, errIface, logValuerIface *types.Interface) {
	if isNilLiteral(value) || nolintSuppressed(pass, value.Pos(), "gocritic", "telemetrysafety") {
		return
	}
	t := pass.TypesInfo.TypeOf(value)
	if t == nil {
		return
	}
	if logValuerIface != nil && (types.Implements(t, logValuerIface) || types.Implements(types.NewPointer(t), logValuerIface)) {
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

func isNilLiteral(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == "nil"
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
