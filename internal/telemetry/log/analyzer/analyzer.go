// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package analyzer provides a go/analysis pass that enforces constant first
// arguments on selected logging functions.
//
// Protected functions (configurable via [New]):
//   - github.com/DataDog/dd-trace-go/v2/internal/log.Error
//   - github.com/DataDog/dd-trace-go/v2/internal/log.Warn
//   - github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.Debug
//   - github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.Warn
//   - github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.Error
//   - github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.(*Logger).Debug
//   - github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.(*Logger).Warn
//   - github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.(*Logger).Error
//   - github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.ReportError
//   - github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.ReportPanic
//
// Because the package-level functions (Debug/Warn/Error) and their Logger
// method counterparts share the same name and package path, a single FuncSpec
// per name covers both call styles.
//
// The analyzer intentionally skips the internal/telemetry/log package itself
// (see [DefaultSkipPkgs]) to avoid false positives on internal delegation
// calls like:
//
//	func Error(message string, ...) { defaultLogger.Load().Error(message, ...) }
//
// This check is equivalent to the telemetryLogConstantMessage ruleguard rule
// in rules/telemetry_rules.go, extended to cover internal/log and the helpers
// ReportError/ReportPanic. The goal is to allow dropping the ruleguard rule
// once the analyzer is wired into all CI paths.
package analyzer

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const doc = `constantlogmsg enforces that the message argument of SDK logging functions is a constant string.

The message argument of log.Error, log.Warn, telemetrylog.Debug/Warn/Error
(package-level and Logger methods), telemetrylog.ReportError, and
telemetrylog.ReportPanic must be a compile-time constant string literal. Using
a non-constant first argument (fmt.Sprintf result, variable, err.Error() call,
etc.) breaks telemetry dedup and risks leaking PII to Error Tracking.

This check is equivalent to the telemetryLogConstantMessage ruleguard rule in
rules/telemetry_rules.go, extended to cover internal/log and the helper
functions ReportError/ReportPanic.`

const telemetryLogPkg = "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"

// DefaultFuncs is the set of functions checked by the default Analyzer.
//
// The telemetry/log package entries (Debug/Warn/Error) cover both package-level
// calls and Logger method calls — resolveFunc maps both to the same (pkg, name)
// key via pass.TypesInfo.Uses.
var DefaultFuncs = []FuncSpec{
	// internal/log — format-string logger; format is the dedup key.
	{PkgPath: "github.com/DataDog/dd-trace-go/v2/internal/log", FuncName: "Error", MsgArgIndex: 0},
	{PkgPath: "github.com/DataDog/dd-trace-go/v2/internal/log", FuncName: "Warn", MsgArgIndex: 0},

	// internal/telemetry/log — structured telemetry logger (pkg-level + Logger methods).
	{PkgPath: telemetryLogPkg, FuncName: "Debug", MsgArgIndex: 0},
	{PkgPath: telemetryLogPkg, FuncName: "Warn", MsgArgIndex: 0},
	{PkgPath: telemetryLogPkg, FuncName: "Error", MsgArgIndex: 0},

	// internal/telemetry/log — explicit helpers for non-log.Error call sites.
	{PkgPath: telemetryLogPkg, FuncName: "ReportError", MsgArgIndex: 0},
	{PkgPath: telemetryLogPkg, FuncName: "ReportPanic", MsgArgIndex: 1}, // func(recovered, msg)
}

// DefaultSkipPkgs is the set of package paths skipped by the default Analyzer.
//
// The telemetry/log package is skipped because its own implementation delegates
// through the same function names with a variable message parameter:
//
//	func Error(message string, ...) { defaultLogger.Load().Error(message, ...) }
//
// Flagging that would be a false positive; enforcement happens at call sites.
var DefaultSkipPkgs = []string{
	telemetryLogPkg,
}

// FuncSpec identifies a function and which argument index holds the constant message.
type FuncSpec struct {
	// PkgPath is the full import path of the package, e.g.
	// "github.com/DataDog/dd-trace-go/v2/internal/log".
	PkgPath string
	// FuncName is the unqualified function name, e.g. "Error".
	FuncName string
	// MsgArgIndex is the zero-based index of the message argument.
	MsgArgIndex int
}

// Analyzer is the production analyzer configured with DefaultFuncs and DefaultSkipPkgs.
var Analyzer = New(DefaultFuncs, DefaultSkipPkgs...)

// New returns an analysis.Analyzer configured with the given function specs.
// skipPkgs lists package import paths whose files are not checked; use this
// to suppress false positives in packages that internally delegate through the
// protected function names (e.g. the telemetry/log implementation itself).
//
// Use New to build a test-scoped analyzer with fake package paths.
func New(funcs []FuncSpec, skipPkgs ...string) *analysis.Analyzer {
	skip := make(map[string]struct{}, len(skipPkgs))
	for _, p := range skipPkgs {
		skip[p] = struct{}{}
	}
	r := &runner{funcs: funcs, skipPkgs: skip}
	return &analysis.Analyzer{
		Name:     "constantlogmsg",
		Doc:      doc,
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run:      r.run,
	}
}

type runner struct {
	funcs    []FuncSpec
	skipPkgs map[string]struct{}
}

func (r *runner) run(pass *analysis.Pass) (any, error) {
	// Skip packages whose internal delegation would produce false positives.
	if _, skip := r.skipPkgs[pass.Pkg.Path()]; skip {
		return nil, nil
	}

	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// Build a lookup: {pkgPath, funcName} → MsgArgIndex.
	type key struct{ pkg, name string }
	lookup := make(map[key]int, len(r.funcs))
	for _, spec := range r.funcs {
		lookup[key{spec.PkgPath, spec.FuncName}] = spec.MsgArgIndex
	}

	ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return
		}

		fn, pkg := resolveFunc(pass, call)
		if fn == "" || pkg == "" {
			return
		}

		msgIdx, found := lookup[key{pkg, fn}]
		if !found {
			return
		}

		if len(call.Args) <= msgIdx {
			return
		}

		arg := call.Args[msgIdx]
		if !isConstantString(pass, arg) {
			pass.Reportf(arg.Pos(),
				"%s.%s: message argument (index %d) must be a constant string; got %s — non-constant messages break telemetry dedup and risk PII leakage",
				pkg, fn, msgIdx, describeExpr(pass, arg),
			)
		}
	})

	return nil, nil
}

// resolveFunc returns the unqualified function name and its package import path
// for a call expression, or empty strings if it cannot be resolved.
//
// Both package-level calls (telemetrylog.Error(...)) and method calls
// (logger.Error(...)) are handled: in both cases pass.TypesInfo.Uses[sel.Sel]
// returns the *types.Func with the defining package and unqualified name.
func resolveFunc(pass *analysis.Pass, call *ast.CallExpr) (fnName, pkgPath string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		// Unqualified call within the same package (e.g. Error("msg")).
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return "", ""
		}
		obj := pass.TypesInfo.Uses[ident]
		if obj == nil || obj.Pkg() == nil {
			return "", ""
		}
		return obj.Name(), obj.Pkg().Path()
	}

	obj := pass.TypesInfo.Uses[sel.Sel]
	if obj == nil || obj.Pkg() == nil {
		return "", ""
	}
	return obj.Name(), obj.Pkg().Path()
}

// isConstantString reports whether expr evaluates to a compile-time constant string.
func isConstantString(pass *analysis.Pass, expr ast.Expr) bool {
	// Fast path: basic string literal.
	if bl, ok := expr.(*ast.BasicLit); ok && bl.Kind == token.STRING {
		return true
	}
	// Slow path: ask the type checker (covers named constants).
	tv, ok := pass.TypesInfo.Types[expr]
	if !ok {
		return false
	}
	if tv.Value == nil {
		return false
	}
	return tv.Value.Kind() == constant.String
}

// describeExpr returns a short human-readable description of an expression for diagnostics.
func describeExpr(pass *analysis.Pass, expr ast.Expr) string {
	tv, ok := pass.TypesInfo.Types[expr]
	if !ok {
		return "unknown expression"
	}
	t := types.TypeString(tv.Type, types.RelativeTo(pass.Pkg))
	switch e := expr.(type) {
	case *ast.CallExpr:
		return t + " (function call result)"
	case *ast.Ident:
		return t + " variable " + e.Name
	case *ast.BinaryExpr:
		return t + " (string concatenation)"
	}
	return t + " expression"
}
