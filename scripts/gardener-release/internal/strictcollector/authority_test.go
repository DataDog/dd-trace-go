// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNoGenericLeasedAuthoritySurface guards the closed continuation algebra.
// Active assembly and spine request settlement must take only fixedRequest;
// roots and successors may take only opaque handles, never wire authority.
func TestNoGenericLeasedAuthoritySurface(t *testing.T) {
	const collector = "collector.go"
	content, err := os.ReadFile(collector)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"executeAssemblyRequest", "executeSpineRequest", "executeAssemblyRaw", "executeSpineRaw",
		"WithSpineLease", "stateV3ReadLease", "context.WithValue", "assemblyLease,",
	} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("%s restores forbidden authority surface %q", collector, forbidden)
		}
	}
	for _, name := range []string{"assembly.go", "operation.go"} {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"executeSpineRaw", "WithSpineLease", "context.WithValue", "assemblyLease,"} {
			if strings.Contains(string(content), forbidden) {
				t.Fatalf("%s restores forbidden authority surface %q", name, forbidden)
			}
		}
	}

	file, err := parser.ParseFile(token.NewFileSet(), collector, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "settleFixed" {
			continue
		}
		if function.Type.Params.NumFields() != 1 || exprName(function.Type.Params.List[0].Type) != "fixedRequest" {
			t.Fatalf("settleFixed must accept only fixedRequest, got %#v", function.Type.Params)
		}
		return
	}
	t.Fatal("settleFixed missing")
}

func exprName(expr ast.Expr) string {
	identifier, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}
