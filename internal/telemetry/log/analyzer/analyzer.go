// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package analyzer provides a go/analysis pass that enforces constant first
// arguments on selected logging functions.
//
// Protected functions (configurable via [Analyzer].FuncNames):
//   - github.com/DataDog/dd-trace-go/v2/internal/log.Error
//   - github.com/DataDog/dd-trace-go/v2/internal/log.Warn
//   - github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.ReportError
//   - github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.ReportPanic
//
// Rationale: the first argument (or second, for ReportPanic) is used as a
// constant dedup key for telemetry and must never carry PII-bearing runtime
// values.  Non-constant strings break deduplication and risk leaking sensitive
// information to the Error Tracking backend.
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

The message argument of log.Error, log.Warn, telemetrylog.ReportError, and
telemetrylog.ReportPanic must be a compile-time constant string literal. Using
a non-constant first argument (fmt.Sprintf result, variable, err.Error() call,
etc.) breaks telemetry dedup and risks leaking PII to Error Tracking.`

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

// DefaultFuncs is the set of functions checked by the default Analyzer.
var DefaultFuncs = []FuncSpec{
	{
		PkgPath:     "github.com/DataDog/dd-trace-go/v2/internal/log",
		FuncName:    "Error",
		MsgArgIndex: 0,
	},
	{
		PkgPath:     "github.com/DataDog/dd-trace-go/v2/internal/log",
		FuncName:    "Warn",
		MsgArgIndex: 0,
	},
	{
		PkgPath:     "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log",
		FuncName:    "ReportError",
		MsgArgIndex: 0,
	},
	{
		PkgPath:     "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log",
		FuncName:    "ReportPanic",
		MsgArgIndex: 1, // ReportPanic(recovered any, msg string)
	},
}

// Analyzer is the configured analysis.Analyzer.
var Analyzer = New(DefaultFuncs)

// New returns an analysis.Analyzer configured with the given function specs.
// Use this to build a test-scoped analyzer with fake package paths.
func New(funcs []FuncSpec) *analysis.Analyzer {
	r := &runner{funcs: funcs}
	return &analysis.Analyzer{
		Name:     "constantlogmsg",
		Doc:      doc,
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run:      r.run,
	}
}

type runner struct {
	funcs []FuncSpec
}

func (r *runner) run(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// Build a lookup: "pkgPath.FuncName" → MsgArgIndex.
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
func resolveFunc(pass *analysis.Pass, call *ast.CallExpr) (fnName, pkgPath string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		// Direct call (no selector): e.g., Error("msg").
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return "", ""
		}
		obj := pass.TypesInfo.Uses[ident]
		if obj == nil {
			return "", ""
		}
		if obj.Pkg() == nil {
			return "", ""
		}
		return obj.Name(), obj.Pkg().Path()
	}

	obj := pass.TypesInfo.Uses[sel.Sel]
	if obj == nil {
		return "", ""
	}
	if obj.Pkg() == nil {
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

	// Slow path: ask the type checker.
	tv, ok := pass.TypesInfo.Types[expr]
	if !ok {
		return false
	}
	if tv.Value == nil {
		return false
	}
	return tv.Value.Kind() == constant.String
}

// describeExpr returns a short description of an expression for diagnostics.
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
