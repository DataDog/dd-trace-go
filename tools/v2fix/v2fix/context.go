// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2fix

type contextKey string

const (
	argsKey             contextKey = "args"           // Stores function call arguments as []ast.Expr
	declaredTypeKey     contextKey = "declared_type"  // Stores the declared type as types.Type after type checking
	endKey              contextKey = "end"            // Stores the end position as token.Pos of the AST node
	fnKey               contextKey = "fn"             // Stores the function object as *types.Func being called
	pkgNameKey          contextKey = "pkg_name"       // Stores the package name as string
	pkgPrefixKey        contextKey = "pkg_prefix"     // Stores the package prefix/alias used in selector expressions (e.g., "tracer", "tr")
	pkgPathKey          contextKey = "pkg_path"       // Stores the full package import path as string
	posKey              contextKey = "pos"            // Stores the starting position as token.Pos of the AST node
	typeKey             contextKey = "type"           // Stores the reflect type string representation
	callExprKey         contextKey = "call_expr"      // Stores the call expression AST node as *ast.CallExpr
	typeExprStrKey      contextKey = "type_expr_str"  // Stores the formatted type expression string to preserve original qualifier/alias
	typePrefixKey       contextKey = "type_prefix"    // Stores modifiers like "*", "[]", "[N]" for composite types
	skipFixKey          contextKey = "skip_fix"       // Set to true when a fix cannot be safely applied
	childOfParentKey    contextKey = "childof_parent" // Stores the parent expression string for ChildOf transformation
	childOfOtherOptsKey contextKey = "childof_other"  // Stores other options (excluding ChildOf) for StartSpan
)
