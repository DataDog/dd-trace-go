// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2fix

import (
	"context"
	"go/ast"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

type Probe func(context.Context, ast.Node, *analysis.Pass) (context.Context, bool)

// DeclaresType returns true if the node declares a type of the given generic type.
// The type use in the generic signature is stored in the context as "type".
// The reflected type is stored in the context as "declared_type".
func DeclaresType[T any]() Probe {
	return func(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
		var (
			obj     types.Object
			typ     = ctx.Value(typeKey)
			typDecl ast.Expr
		)
		switch typ {
		case "*ast.ValueSpec":
			spec := n.(*ast.ValueSpec)
			if len(spec.Names) == 0 {
				return ctx, false
			}
			obj = pass.TypesInfo.ObjectOf(spec.Names[0])
			typDecl = spec.Type
		case "*ast.Field":
			field := n.(*ast.Field)
			if len(field.Names) == 0 {
				return ctx, false
			}
			obj = pass.TypesInfo.ObjectOf(field.Names[0])
			typDecl = field.Type
		default:
			return ctx, false
		}
		if typDecl == nil {
			return ctx, false
		}
		t, ok := obj.Type().(*types.Named)
		if !ok {
			return ctx, false
		}
		// We need to store the reflected type unconditionally
		// to be able to introspect it later, even if the probe
		// fails or is combined with Not.
		ctx = context.WithValue(ctx, declaredTypeKey, t)
		ctx = context.WithValue(ctx, posKey, typDecl.Pos())
		ctx = context.WithValue(ctx, endKey, typDecl.End())
		v := new(T)
		e := reflect.TypeOf(v).Elem()
		if t.Obj().Pkg() == nil {
			return ctx, false
		}
		ctx = context.WithValue(ctx, pkgNameKey, t.Obj().Pkg().Name())
		if t.Obj().Pkg().Path() != e.PkgPath() {
			return ctx, false
		}
		if t.Obj().Name() != e.Name() {
			return ctx, false
		}
		return ctx, true
	}
}

// Is returns true if the node is of type T.
// The type use in the generic signature is stored in the context as "type".
func Is[T any](ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
	v, ok := n.(T)
	if !ok {
		return ctx, false
	}
	ctx = context.WithValue(ctx, typeKey, reflect.TypeOf(v).String())
	return ctx, true
}

// IsFuncCall returns true if the node is a function call.
// The function call expression is stored in the context as "fn".
// The package path of the function is stored as "pkg_path".
// The parameters of the function are stored as "args".
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
	ctx = context.WithValue(ctx, callExprKey, c)
	ctx = context.WithValue(ctx, fnKey, fn)
	ctx = context.WithValue(ctx, argsKey, c.Args)
	pkg := fn.Pkg()
	if pkg == nil {
		// It could be a built-in function, in which case
		// we don't know the package path.
		return ctx, true
	}
	ctx = context.WithValue(ctx, pkgPathKey, pkg.Path())
	return ctx, true
}

// IsImport returns true if the node is an import statement.
// The import path is stored in the context as "pkg_path".
func IsImport(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
	imp, ok := n.(*ast.ImportSpec)
	if !ok {
		return ctx, false
	}
	path := strings.Trim(imp.Path.Value, `"`)
	ctx = context.WithValue(ctx, pkgPathKey, path)
	return ctx, true
}

// HasPackagePrefix returns true if the selector expression has a package prefix.
// The package path is expected in the context as "pkg_path".
func HasPackagePrefix(prefix string) Probe {
	return func(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
		pkgPath, ok := ctx.Value(pkgPathKey).(string)
		if !ok {
			return ctx, false
		}
		return ctx, strings.HasPrefix(pkgPath, prefix)
	}
}

// ImportedFrom returns true if the value is imported from the given package path prefix.
func ImportedFrom(pkgPath string) Probe {
	return func(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
		var (
			obj types.Object
			typ = ctx.Value(typeKey)
		)
		switch typ {
		case "*ast.ValueSpec":
			spec := n.(*ast.ValueSpec)
			if len(spec.Names) == 0 {
				return ctx, false
			}
			obj = pass.TypesInfo.ObjectOf(spec.Names[0])
		case "*ast.Field":
			field := n.(*ast.Field)
			if len(field.Names) == 0 {
				return ctx, false
			}
			obj = pass.TypesInfo.ObjectOf(field.Names[0])
		default:
			return ctx, false
		}
		t, ok := obj.Type().(*types.Named)
		if !ok {
			return ctx, false
		}
		if t.Obj().Pkg() == nil {
			return ctx, false
		}
		if !strings.HasPrefix(t.Obj().Pkg().Path(), pkgPath) {
			return ctx, false
		}
		return ctx, true
	}
}

func WithFunctionName(name string) Probe {
	return func(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
		fn, ok := ctx.Value(fnKey).(*types.Func)
		if !ok {
			return ctx, false
		}
		return ctx, fn.Name() == name
	}
}

// Not returns the inverse of the given probe.
func Not(p Probe) Probe {
	return func(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
		ctx, ok := p(ctx, n, pass)
		return ctx, !ok
	}
}

// Or returns a probe that is true if at least one of the given probes is true.
func Or(ps ...Probe) Probe {
	return func(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
		for _, p := range ps {
			ctx, ok := p(ctx, n, pass)
			if ok {
				return ctx, true
			}
		}
		return ctx, false
	}
}
