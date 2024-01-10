// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"errors"
	"go/ast"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

var analyzer = &analysis.Analyzer{
	Name:     "v2check",
	Doc:      "Migration tool to assist with the dd-trace-go v2 upgrade",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	ins, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, errors.New("analyzer is not type *inspector.Inspector")
	}
	filter := []ast.Node{
		(*ast.CallExpr)(nil),
		(*ast.ImportSpec)(nil),
	}
	ins.Preorder(filter, func(n ast.Node) {
		var k *knownChange
		for _, c := range knownChanges {
			if c.eval(n, pass) {
				k = c
				break
			}
		}
		if k == nil {
			return
		}
		pass.Report(analysis.Diagnostic{
			Pos:            n.Pos(),
			End:            n.End(),
			Category:       "",
			Message:        k.message(),
			URL:            "",
			SuggestedFixes: nil,
			Related:        nil,
		})
	})
	return nil, nil
}
