// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2fix

import (
	"bytes"
	"context"
	"go/ast"
	"go/printer"
	"go/types"
	"reflect"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

type Probe func(context.Context, ast.Node, *analysis.Pass) (context.Context, bool)

// getTypeNameFromType extracts the TypeName object from Named or Alias types.
func getTypeNameFromType(t types.Type) *types.TypeName {
	switch t := t.(type) {
	case *types.Named:
		return t.Obj()
	case *types.Alias:
		return t.Obj()
	}
	return nil
}

// DeclaresType returns true if the node declares a type of the given generic type.
// The type use in the generic signature is stored in the context as "type".
// The reflected type is stored in the context as "declared_type".
// The formatted type expression string is stored as "type_expr_str".
// Handles both *types.Named and *types.Alias (Go 1.22+ type aliases).
func DeclaresType[T any]() Probe {
	return func(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
		var (
			typ     = ctx.Value(typeKey)
			typDecl ast.Expr
			varType types.Type
		)
		switch typ {
		case "*ast.ValueSpec":
			spec := n.(*ast.ValueSpec)
			if len(spec.Names) == 0 {
				return ctx, false
			}
			typDecl = spec.Type
			// Try to get type from object first (works for named vars)
			obj := pass.TypesInfo.ObjectOf(spec.Names[0])
			if obj != nil {
				varType = obj.Type()
			} else if typDecl != nil {
				// For blank identifiers, get type from the type expression.
				varType = getTypeFromTypeExpr(typDecl, pass)
			}
		case "*ast.Field":
			field := n.(*ast.Field)
			if len(field.Names) == 0 {
				return ctx, false
			}
			typDecl = field.Type
			obj := pass.TypesInfo.ObjectOf(field.Names[0])
			if obj != nil {
				varType = obj.Type()
			} else if typDecl != nil {
				varType = getTypeFromTypeExpr(typDecl, pass)
			}
		default:
			return ctx, false
		}
		if typDecl == nil {
			return ctx, false
		}
		if varType == nil {
			return ctx, false
		}

		// Extract type object info, handling both *types.Named and *types.Alias
		typeObj := getTypeNameFromType(varType)
		if typeObj == nil {
			return ctx, false
		}
		ctx = context.WithValue(ctx, declaredTypeKey, varType)

		ctx = context.WithValue(ctx, posKey, typDecl.Pos())
		ctx = context.WithValue(ctx, endKey, typDecl.End())

		// Store formatted type expression string to preserve original qualifier/alias
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, pass.Fset, typDecl); err == nil {
			ctx = context.WithValue(ctx, typeExprStrKey, buf.String())
		}

		v := new(T)
		e := reflect.TypeOf(v).Elem()
		if typeObj.Pkg() == nil {
			return ctx, false
		}
		ctx = context.WithValue(ctx, pkgNameKey, typeObj.Pkg().Name())
		if typeObj.Pkg().Path() != e.PkgPath() {
			return ctx, false
		}
		if typeObj.Name() != e.Name() {
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
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		// It might be a non-selector expression, in which case we don't know the package prefix.
		return ctx, true
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		// It might be a non-selector expression, in which case we don't know the package prefix.
		return ctx, true
	}
	ctx = context.WithValue(ctx, pkgPrefixKey, ident.Name)
	return ctx, true
}

// IsImport returns true if the node is an import statement.
// The import path is stored in the context as "pkg_path".
// The pos/end keys are set to the import path literal position (not the alias).
func IsImport(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
	imp, ok := n.(*ast.ImportSpec)
	if !ok {
		return ctx, false
	}
	// Use strconv.Unquote to properly handle both regular and raw string imports
	path, err := strconv.Unquote(imp.Path.Value)
	if err != nil {
		return ctx, false
	}
	ctx = context.WithValue(ctx, pkgPathKey, path)
	// Set pos/end to the import path literal position so V1ImportURL edits only the string literal
	ctx = context.WithValue(ctx, posKey, imp.Path.Pos())
	ctx = context.WithValue(ctx, endKey, imp.Path.End())
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
// It checks both the resolved type's package path AND the AST import path (for type aliases).
// It also sets declaredTypeKey in the context when a named type is found.
// Handles composite types (pointers, slices, arrays) by unwrapping to the base type
// and storing the type prefix (e.g., "*", "[]") in typePrefixKey.
func ImportedFrom(pkgPath string) Probe {
	return func(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
		var (
			typ     = ctx.Value(typeKey)
			varType types.Type
			typDecl ast.Expr
		)
		switch typ {
		case "*ast.ValueSpec":
			spec := n.(*ast.ValueSpec)
			if len(spec.Names) == 0 {
				return ctx, false
			}
			typDecl = spec.Type
			obj := pass.TypesInfo.ObjectOf(spec.Names[0])
			if obj != nil {
				varType = obj.Type()
			} else if typDecl != nil {
				varType = getTypeFromTypeExpr(typDecl, pass)
			}
		case "*ast.Field":
			field := n.(*ast.Field)
			if len(field.Names) == 0 {
				return ctx, false
			}
			typDecl = field.Type
			obj := pass.TypesInfo.ObjectOf(field.Names[0])
			if obj != nil {
				varType = obj.Type()
			} else if typDecl != nil {
				varType = getTypeFromTypeExpr(typDecl, pass)
			}
		default:
			return ctx, false
		}

		// Set pos/end to the type expression position for accurate fix targeting
		if typDecl != nil {
			ctx = context.WithValue(ctx, posKey, typDecl.Pos())
			ctx = context.WithValue(ctx, endKey, typDecl.End())
		}

		// Unwrap composite types (pointer, slice, array) to get the base type
		// and store the prefix for use in fixes
		var typePrefix string
		var prefixValid bool
		baseTypDecl := typDecl
		if typDecl != nil {
			baseTypDecl, typePrefix, prefixValid = unwrapTypeExpr(typDecl)
			if typePrefix != "" {
				if !prefixValid {
					// Array length couldn't be rendered; skip this fix to avoid
					// corrupting the type (e.g., turning [N+1]T into []T)
					ctx = context.WithValue(ctx, skipFixKey, true)
				}
				ctx = context.WithValue(ctx, typePrefixKey, typePrefix)
				// Also get the base type from the unwrapped expression
				varType = getTypeFromTypeExpr(baseTypDecl, pass)
			}
		}

		// Store the resolved type in context for use by later probes
		// Support both *types.Named and *types.Alias (Go 1.22+)
		if varType != nil && getTypeNameFromType(varType) != nil {
			ctx = context.WithValue(ctx, declaredTypeKey, varType)
		}

		// For type aliases, the resolved type may be from a different package.
		// Check the AST type expression's import path first (more reliable for aliases).
		// Use the unwrapped base type expression for the lookup.
		if baseTypDecl != nil {
			if importPath := importPathFromTypeExpr(baseTypDecl, pass, n); importPath != "" {
				if strings.HasPrefix(importPath, pkgPath) {
					return ctx, true
				}
			}
		}

		// Then check the resolved type's package path
		if t, ok := varType.(*types.Named); ok {
			if pkg := t.Obj().Pkg(); pkg != nil && strings.HasPrefix(pkg.Path(), pkgPath) {
				return ctx, true
			}
		}
		return ctx, false
	}
}

// unwrapTypeExpr unwraps pointer, slice, and array type expressions to get the base type.
// It returns the base type expression, a prefix string (e.g., "*", "[]", "[N]") to prepend,
// and a boolean indicating whether the prefix is valid (false if array length couldn't be safely rendered).
// When prefixValid is false, the prefix contains "[?]" as a placeholder and the fix should be skipped,
// but the diagnostic should still be emitted.
func unwrapTypeExpr(typDecl ast.Expr) (ast.Expr, string, bool) {
	var prefix strings.Builder
	valid := true
	for {
		switch t := typDecl.(type) {
		case *ast.StarExpr:
			prefix.WriteByte('*')
			typDecl = t.X
		case *ast.ArrayType:
			if t.Len == nil {
				// Slice type - safe to render
				prefix.WriteString("[]")
			} else if lit, isLit := t.Len.(*ast.BasicLit); isLit {
				// Literal array length (e.g., [5]) - safe to include in fix
				prefix.WriteByte('[')
				prefix.WriteString(lit.Value)
				prefix.WriteByte(']')
			} else {
				// Non-literal array length (identifier, expression, etc.)
				// Skip fix to preserve original formatting, but continue to detect type
				prefix.WriteString("[?]")
				valid = false
			}
			typDecl = t.Elt
		default:
			return typDecl, prefix.String(), valid
		}
	}
}

// getTypeFromTypeExpr extracts the type from a type expression.
// This handles various cases including blank identifiers and type aliases.
func getTypeFromTypeExpr(typDecl ast.Expr, pass *analysis.Pass) types.Type {
	// Try TypeOf first (works for value expressions)
	if t := pass.TypesInfo.TypeOf(typDecl); t != nil {
		return t
	}
	// Try Types map (works for type expressions)
	if tv, ok := pass.TypesInfo.Types[typDecl]; ok && tv.Type != nil {
		return tv.Type
	}
	// For SelectorExpr like pkg.Type, look up the type directly
	if sel, ok := typDecl.(*ast.SelectorExpr); ok {
		// Look up the selector (the type name) in Uses
		if obj := pass.TypesInfo.Uses[sel.Sel]; obj != nil {
			if tn, ok := obj.(*types.TypeName); ok {
				return tn.Type()
			}
		}
		// Fallback: check ObjectOf
		if obj := pass.TypesInfo.ObjectOf(sel.Sel); obj != nil {
			if tn, ok := obj.(*types.TypeName); ok {
				return tn.Type()
			}
		}
	}
	return nil
}

// importPathFromTypeExpr extracts the import path from a type expression like "pkg.Type".
// It looks up the package identifier in pass.TypesInfo.Uses to find the imported package.
func importPathFromTypeExpr(typDecl ast.Expr, pass *analysis.Pass, n ast.Node) string {
	sel, ok := typDecl.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	// Look up the identifier to find the package it refers to.
	// Uses maps identifiers to the objects they denote.
	if obj, ok := pass.TypesInfo.Uses[ident]; ok {
		if pkgName, ok := obj.(*types.PkgName); ok {
			return pkgName.Imported().Path()
		}
	}
	// Fallback: for package identifiers, ObjectOf might work
	if obj := pass.TypesInfo.ObjectOf(ident); obj != nil {
		if pkgName, ok := obj.(*types.PkgName); ok {
			return pkgName.Imported().Path()
		}
	}
	// Last resort: search the current file's imports for a matching name.
	// Find the file that contains this node.
	nodePos := n.Pos()
	for _, file := range pass.Files {
		if file.Pos() <= nodePos && nodePos < file.End() {
			for _, imp := range file.Imports {
				name := ""
				if imp.Name != nil {
					name = imp.Name.Name
				} else {
					// Use the last part of the path as the default name
					path, err := strconv.Unquote(imp.Path.Value)
					if err != nil {
						continue
					}
					parts := strings.Split(path, "/")
					name = parts[len(parts)-1]
				}
				if name == ident.Name {
					path, err := strconv.Unquote(imp.Path.Value)
					if err != nil {
						continue
					}
					return path
				}
			}
			break // Found the file, no need to continue
		}
	}
	return ""
}

// HasBaseType returns true if the declared base type (after unwrapping composite types)
// matches the given generic type T. This is useful for checking types wrapped in
// pointers, slices, or arrays (e.g., *T, []T, [N]T).
// It expects declaredTypeKey to be set by ImportedFrom (which stores the unwrapped type).
func HasBaseType[T any]() Probe {
	return func(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
		declaredType := ctx.Value(declaredTypeKey)
		if declaredType == nil {
			return ctx, false
		}
		varType, ok := declaredType.(types.Type)
		if !ok {
			return ctx, false
		}
		typeObj := getTypeNameFromType(varType)
		if typeObj == nil {
			return ctx, false
		}
		if typeObj.Pkg() == nil {
			return ctx, false
		}

		v := new(T)
		e := reflect.TypeOf(v).Elem()
		if typeObj.Pkg().Path() != e.PkgPath() {
			return ctx, false
		}
		if typeObj.Name() != e.Name() {
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

// HasV1PackagePath returns true if the function's package path starts with the v1 prefix.
// This is used to reduce false positives by ensuring we only match dd-trace-go v1 packages.
// It expects the pkgPathKey to be set by IsFuncCall.
func HasV1PackagePath(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
	pkgPath, ok := ctx.Value(pkgPathKey).(string)
	if !ok {
		return ctx, false
	}
	return ctx, strings.HasPrefix(pkgPath, "gopkg.in/DataDog/dd-trace-go.v1")
}

// IsV1Import returns true if the import path is a v1 dd-trace-go import.
// Matches both the root import "gopkg.in/DataDog/dd-trace-go.v1" and subpath imports.
// It expects pkgPathKey to be set by IsImport.
func IsV1Import(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
	pkgPath, ok := ctx.Value(pkgPathKey).(string)
	if !ok {
		return ctx, false
	}
	const v1Root = "gopkg.in/DataDog/dd-trace-go.v1"
	// Match exact root or subpath (with trailing slash)
	return ctx, pkgPath == v1Root || strings.HasPrefix(pkgPath, v1Root+"/")
}

// HasChildOfOption returns true if the StartSpan call has a ChildOf option.
// It extracts the parent expression and stores it in childOfParentKey.
// Other options (excluding ChildOf) are stored in childOfOtherOptsKey.
func HasChildOfOption(ctx context.Context, n ast.Node, pass *analysis.Pass) (context.Context, bool) {
	args, ok := ctx.Value(argsKey).([]ast.Expr)
	if !ok || len(args) < 2 {
		return ctx, false
	}
	callExpr, _ := ctx.Value(callExprKey).(*ast.CallExpr)
	hasEllipsis := callExpr != nil && callExpr.Ellipsis.IsValid()

	var parentExpr string
	var otherOpts []string
	foundChildOf := false
	skipFix := false

	isChildOfCall := func(arg ast.Expr) bool {
		call, ok := arg.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		return sel.Sel.Name == "ChildOf"
	}

	// Check all args after the first one (operation name) for ChildOf calls
	for _, arg := range args[1:] {
		call, ok := arg.(*ast.CallExpr)
		if !ok {
			if opt := exprToString(arg); opt != "" {
				otherOpts = append(otherOpts, opt)
			} else {
				skipFix = true
			}
			continue
		}

		// Check if this is a ChildOf call
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			if opt := exprToString(arg); opt != "" {
				otherOpts = append(otherOpts, opt)
			} else {
				skipFix = true
			}
			continue
		}

		if sel.Sel.Name == "ChildOf" {
			foundChildOf = true
			// Extract the parent expression from ChildOf(parent.Context()) or ChildOf(parentCtx)
			if len(call.Args) > 0 {
				parentArg := call.Args[0]
				// Check if it's a parent.Context() call - we want to use just "parent"
				if callExpr, ok := parentArg.(*ast.CallExpr); ok {
					if selExpr, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
						if selExpr.Sel.Name == "Context" {
							parentExpr = exprToString(selExpr.X)
							continue
						}
					}
				}
				// Otherwise use the full expression
				parentExpr = exprToString(parentArg)
			}
		} else {
			// This is not ChildOf, collect it as another option
			if opt := exprToString(arg); opt != "" {
				otherOpts = append(otherOpts, opt)
			} else {
				skipFix = true
			}
		}
	}

	if !foundChildOf || parentExpr == "" {
		return ctx, false
	}
	// Preserve ellipsis on the last argument if present.
	if hasEllipsis {
		lastArg := args[len(args)-1]
		if isChildOfCall(lastArg) {
			// Cannot preserve ellipsis if it applies to ChildOf itself.
			return ctx, false
		}
		if len(otherOpts) == 0 {
			return ctx, false
		}
		otherOpts[len(otherOpts)-1] = otherOpts[len(otherOpts)-1] + "..."
	}

	if skipFix {
		ctx = context.WithValue(ctx, skipFixKey, true)
	}
	ctx = context.WithValue(ctx, childOfParentKey, parentExpr)
	ctx = context.WithValue(ctx, childOfOtherOptsKey, otherOpts)
	return ctx, true
}

// exprToString converts an AST expression to a string representation.
// This is a simplified version that handles common cases.
func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		x := exprToString(e.X)
		if x == "" {
			return ""
		}
		return x + "." + e.Sel.Name
	case *ast.CallExpr:
		fun := exprToString(e.Fun)
		if fun == "" {
			return ""
		}
		args := exprListToString(e.Args)
		if args == "" && len(e.Args) > 0 {
			return ""
		}
		return fun + "(" + args + ")"
	case *ast.BasicLit:
		return e.Value
	case *ast.IndexExpr:
		return exprToString(e.X) + "[" + exprToString(e.Index) + "]"
	case *ast.StarExpr:
		return "*" + exprToString(e.X)
	case *ast.UnaryExpr:
		return e.Op.String() + exprToString(e.X)
	case *ast.ParenExpr:
		return "(" + exprToString(e.X) + ")"
	case *ast.BinaryExpr:
		left := exprToString(e.X)
		right := exprToString(e.Y)
		if left == "" || right == "" {
			return ""
		}
		return left + " " + e.Op.String() + " " + right
	case *ast.SliceExpr:
		x := exprToString(e.X)
		if x == "" {
			return ""
		}
		low, high := "", ""
		if e.Low != nil {
			low = exprToString(e.Low)
		}
		if e.High != nil {
			high = exprToString(e.High)
		}
		if e.Slice3 && e.Max != nil {
			return x + "[" + low + ":" + high + ":" + exprToString(e.Max) + "]"
		}
		return x + "[" + low + ":" + high + "]"
	case *ast.CompositeLit:
		typ := ""
		if e.Type != nil {
			typ = exprToString(e.Type)
			if typ == "" {
				return ""
			}
		}
		elts := exprListToString(e.Elts)
		if elts == "" && len(e.Elts) > 0 {
			return ""
		}
		return typ + "{" + elts + "}"
	}
	return ""
}

func exprListToString(exprs []ast.Expr) string {
	var parts []string
	for _, e := range exprs {
		s := exprToString(e)
		if s == "" {
			return ""
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}
