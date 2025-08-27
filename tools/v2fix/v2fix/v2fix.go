// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2fix

import (
	"errors"
	"go/ast"
	"log"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

type Checker struct {
	knownChanges []KnownChange
}

func (c Checker) Run(handler func(*analysis.Analyzer)) {
	analyzer := &analysis.Analyzer{
		Name:     "v2fix",
		Doc:      "Migration tool to assist with the dd-trace-go v2 upgrade",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run:      c.runner(),
	}
	if handler == nil {
		return
	}
	handler(analyzer)
}

func (c Checker) runner() func(*analysis.Pass) (interface{}, error) {
	log.Printf("Running v2fix with %d known changes", len(c.knownChanges))
	knownChanges := c.knownChanges

	return func(pass *analysis.Pass) (interface{}, error) {
		filter := []ast.Node{
			(*ast.CallExpr)(nil),
			(*ast.ImportSpec)(nil),
			(*ast.ValueSpec)(nil),
			(*ast.Field)(nil),
		}
		ins, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		if !ok {
			return nil, errors.New("analyzer is not type *inspector.Inspector")
		}
		ins.Preorder(filter, func(n ast.Node) {
			var k KnownChange
			for _, c := range knownChanges {
				if eval(c, n, pass) {
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
				Message:        k.String(),
				URL:            "",
				SuggestedFixes: k.Fixes(),
				Related:        nil,
			})
		})
		return nil, nil
	}
}

func NewChecker(knownChanges ...KnownChange) *Checker {
	return &Checker{knownChanges: knownChanges}
}
