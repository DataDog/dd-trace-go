// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

type context map[string]any

// knownChange models code expressions that must be changed to migrate to v2.
// It is defined by a set of probes that must be true to report the analyzed expression.
// It also contains a message function that returns a string describing the change.
// The probes are evaluated in order, and the first one that returns false
// will cause the expression to be ignored.
// A predicate can store information in the context, which is available to the message function and
// to the following probes.
// It is possible to declare fixes that will be suggested to the user or applied automatically.
type knownChange struct {
	ctx     context
	probes  []func(context, ast.Node, *analysis.Pass) bool
	fixes   []analysis.SuggestedFix
	message func() string
}

func (c *knownChange) eval(n ast.Node, pass *analysis.Pass) bool {
	c.ctx = make(context)
	for _, p := range c.probes {
		if !p(c.ctx, n, pass) {
			return false
		}
	}
	return true
}

// newV1Usage detects the usage of any v1 function.
func newV1Usage() *knownChange {
	var d knownChange
	d.ctx = make(context)
	d.probes = []func(context, ast.Node, *analysis.Pass) bool{
		isFuncCall,
		hasPackagePrefix("gopkg.in/DataDog/dd-trace-go.v1/"),
	}
	d.message = func() string {
		fn, ok := d.ctx["fn"].(*types.Func)
		if !ok {
			return "unknown"
		}
		return fn.FullName()
	}
	return &d
}

var knownChanges = []*knownChange{
	newV1Usage(),
}

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
