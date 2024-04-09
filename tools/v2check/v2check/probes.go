// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2check

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

// isFuncCall returns true if the node is a function call.
// The function call expression is stored in the context as "fn".
func isFuncCall(ctx context, n ast.Node, pass *analysis.Pass) bool {
	c, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	callee := typeutil.Callee(pass.TypesInfo, c)
	if callee == nil {
		return false
	}
	fn, ok := callee.(*types.Func)
	if !ok {
		return false
	}
	ctx["fn"] = fn
	return true
}

// hasPackagePrefix returns true if the selector expression has a package prefix.
func hasPackagePrefix(prefix string) func(context, ast.Node, *analysis.Pass) bool {
	return func(ctx context, n ast.Node, pass *analysis.Pass) bool {
		fn, ok := ctx["fn"].(*types.Func)
		if !ok {
			return false
		}
		return strings.HasPrefix(fn.Pkg().Path(), prefix)
	}
}
