// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package oncereset provides a go/analysis pass that forbids resetting a
// sync.Once except inside reset*ForTesting or mark*ForTesting helpers.
//
// Assigning sync.Once{} while another goroutine may be in Once.Do is a data
// race. Tests that need a fresh Once must go through a named helper so the
// reset can be serialized with Do.
package oncereset

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const Doc = `oncereset forbids assigning sync.Once{} except in reset*ForTesting or mark*ForTesting helpers.

Resetting a sync.Once by assignment races with concurrent Once.Do. Put test
resets in a helper named reset*ForTesting or mark*ForTesting so they can take
the same mutex as Do. Production singleton re-init may use //nolint:oncereset.`

const Name = "oncereset"

// Analyzer is the production oncereset analyzer.
var Analyzer = &analysis.Analyzer{
	Name:     Name,
	Doc:      Doc,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	nodeFilter := []ast.Node{(*ast.AssignStmt)(nil)}
	ins.WithStack(nodeFilter, func(n ast.Node, push bool, stack []ast.Node) bool {
		if !push {
			return true
		}
		assign := n.(*ast.AssignStmt)
		if assign.Tok != token.ASSIGN {
			return true
		}
		if allowedHelper(enclosingFuncName(stack)) {
			return true
		}
		for _, lhs := range assign.Lhs {
			if !isSyncOnce(pass.TypesInfo.TypeOf(lhs)) {
				continue
			}
			if nolintSuppressed(pass, lhs.Pos()) {
				continue
			}
			pass.Reportf(lhs.Pos(), "reset sync.Once outside reset*ForTesting or mark*ForTesting")
		}
		return true
	})
	return nil, nil
}

func isSyncOnce(t types.Type) bool {
	if t == nil {
		return false
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == "sync" && obj.Name() == "Once"
}

func enclosingFuncName(stack []ast.Node) string {
	for i := len(stack) - 1; i >= 0; i-- {
		if fn, ok := stack[i].(*ast.FuncDecl); ok {
			return fn.Name.Name
		}
	}
	return ""
}

func allowedHelper(name string) bool {
	if !strings.HasSuffix(name, "ForTesting") {
		return false
	}
	return strings.HasPrefix(name, "reset") ||
		strings.HasPrefix(name, "Reset") ||
		strings.HasPrefix(name, "mark") ||
		strings.HasPrefix(name, "Mark")
}

func nolintSuppressed(pass *analysis.Pass, pos token.Pos) bool {
	file := fileForPos(pass, pos)
	if file == nil {
		return false
	}
	line := pass.Fset.Position(pos).Line
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			cLine := pass.Fset.Position(c.Pos()).Line
			if cLine != line && cLine != line-1 {
				continue
			}
			if nolintMatches(c.Text) {
				return true
			}
		}
	}
	return false
}

func fileForPos(pass *analysis.Pass, pos token.Pos) *ast.File {
	tokFile := pass.Fset.File(pos)
	if tokFile == nil {
		return nil
	}
	for _, f := range pass.Files {
		if pass.Fset.File(f.Pos()) == tokFile {
			return f
		}
	}
	return nil
}

func nolintMatches(commentText string) bool {
	text := strings.TrimSpace(commentText)
	if !strings.HasPrefix(text, "//") {
		return false
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "//"))
	if text == "nolint" || strings.HasPrefix(text, "nolint ") {
		return true
	}
	if !strings.HasPrefix(text, "nolint:") {
		return false
	}
	list := strings.TrimSpace(strings.TrimPrefix(text, "nolint:"))
	if idx := strings.Index(list, " "); idx >= 0 {
		list = list[:idx]
	}
	for part := range strings.SplitSeq(list, ",") {
		if strings.EqualFold(strings.TrimSpace(part), Name) {
			return true
		}
	}
	return false
}
