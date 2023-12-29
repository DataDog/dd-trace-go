// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"golang.org/x/tools/go/analysis"
	"strings"
)

type context map[string]any

// knownChange models code expressions that must be changed to migrate to v2.
// It is defined by a set of predicates that must be true to diagnose the analyzed expression.
// It also contains a message function that returns a string describing the change.
// The predicates are evaluated in order, and the first one that returns false
// will cause the expression to be ignored.
// A predicate can store information in the context, which is available to the message function and
// to the following predicates.
// It is possible to declare fixes that will be suggested to the user or applied automatically.
type knownChange struct {
	ctx        context
	predicates []func(context, ast.Node, *analysis.Pass) bool
	fixes      []analysis.SuggestedFix
	message    func() string
}

func (c *knownChange) eval(n ast.Node, pass *analysis.Pass) bool {
	c.ctx = make(context)
	for _, p := range c.predicates {
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
	d.predicates = []func(context, ast.Node, *analysis.Pass) bool{
		isSelectorExpr(),
		hasPackagePrefix("gopkg.in/DataDog/dd-trace-go.v1/"),
	}
	d.message = func() string {
		fi, ok := d.ctx["tObj"].(*types.Func)
		if !ok {
			return "unknown"
		}
		return fmt.Sprintf("%s.%s", fi.Pkg().Path(), fi.Name())
	}
	return &d
}

var knownChanges = []*knownChange{
	newV1Usage(),
}

// isSelectorExpr returns true if the node is a selector expression.
// The node is stored in the context as "root".
func isSelectorExpr() func(context, ast.Node, *analysis.Pass) bool {
	return func(ctx context, n ast.Node, _ *analysis.Pass) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return false
		}
		s, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		ctx["root"] = s
		return true
	}
}

// hasPackagePrefix returns true if the selector expression has a package prefix.
// The package object is stored in the context as "tObj".
func hasPackagePrefix(prefix string) func(context, ast.Node, *analysis.Pass) bool {
	return func(ctx context, n ast.Node, pass *analysis.Pass) bool {
		s, ok := ctx["root"].(*ast.SelectorExpr)
		if !ok {
			return false
		}
		fi := pass.TypesInfo.Uses[s.Sel]
		if fi.Pkg() == nil {
			return false
		}
		if !strings.HasPrefix(fi.Pkg().Path(), prefix) {
			return false
		}
		ctx["tObj"] = fi
		return true
	}
}
