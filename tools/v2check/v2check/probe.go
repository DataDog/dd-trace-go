// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2check

import (
	"context"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

type Probe func(context.Context, ast.Node, *analysis.Pass) (context.Context, bool)

// IsFuncCall returns true if the node is a function call.
// The function call expression is stored in the context as "fn".
func IsFuncCall(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
	c, ok := n.(*ast.CallExpr)
	if !ok {
		return ctx, false
	}
	callee := typeutil.Callee(pass.TypesInfo, c)
	if callee == nil {
		return ctx, false
	}
	fn, ok := callee.(*types.Func)
	if !ok {
		return ctx, false
	}
	ctx = context.WithValue(ctx, "pkg_path", fn.Pkg().Path())
	return ctx, true
}

func IsImport(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
	imp, ok := n.(*ast.ImportSpec)
	if !ok {
		return ctx, false
	}
	path := strings.Trim(imp.Path.Value, `"`)
	ctx = context.WithValue(ctx, "pkg_path", path)
	return ctx, true
}

// HasPackagePrefix returns true if the selector expression has a package prefix.
func HasPackagePrefix(prefix string) Probe {
	return func(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
		pkgPath, ok := ctx.Value("pkg_path").(string)
		if !ok {
			return ctx, false
		}
		return ctx, strings.HasPrefix(pkgPath, prefix)
	}
}
