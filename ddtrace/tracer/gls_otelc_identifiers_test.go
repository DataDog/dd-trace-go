// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tracer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGLSOtelcReceiverNames guards the receiver names that otelc.yaml writes
// literally into this package.
//
// Arguments resolve through otelc's function template variables and survive a
// rename. The receiver does not: FuncArgument skips it and there is no
// FuncReceiver, so span_gls_finish and span_gls_clear spell it as `s`.
//
// A rename normally breaks the otelc build anyway. What this test buys is where
// the failure shows up: a plain `go test` naming the yaml and the receiver,
// instead of a compile error in the otelc CI lane. It also covers the one case
// that would not fail loudly, where an unrelated `s` happens to be in scope at
// the injection site.
//
// Keep in sync with ddtrace/tracer/otelc.yaml.
func TestGLSOtelcReceiverNames(t *testing.T) {
	for _, tc := range []struct {
		file string
		recv string
		fn   string
		want string
	}{
		// orchestrion.GLSDeactivate(&s.__dd_glsDone, &s.__dd_glsPop)
		{file: "span.go", recv: "*Span", fn: "Finish", want: "s"},
		// orchestrion.GLSReset(&s.__dd_glsDone, &s.__dd_glsPop)
		{file: "span.go", recv: "*Span", fn: "clear", want: "s"},
	} {
		name := "(" + tc.recv + ")." + tc.fn
		t.Run(name, func(t *testing.T) {
			decl := findFuncDecl(t, tc.file, tc.recv, tc.fn)
			require.NotEmpty(t, decl.Recv.List[0].Names, "%s has an unnamed receiver", name)
			assert.Equalf(t, tc.want, decl.Recv.List[0].Names[0].Name,
				"otelc.yaml injects code into %s that refers to the receiver as %q; "+
					"rename it there too, or the injected code stops compiling", name, tc.want)
		})
	}
}

// findFuncDecl locates a method declaration in one of this package's source
// files. Individual files are parsed rather than the whole directory to avoid
// the deprecated ast.Package.
func findFuncDecl(t *testing.T, file, recv, name string) *ast.FuncDecl {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	require.NoError(t, err)

	for _, d := range parsed.Decls {
		decl, ok := d.(*ast.FuncDecl)
		if !ok || decl.Name.Name != name || receiverType(decl) != recv {
			continue
		}
		return decl
	}
	t.Fatalf("no declaration of %q with receiver %q in %s", name, recv, file)
	return nil
}

// receiverType renders a method's receiver type the way a rule's `where.recv`
// selector spells it (e.g. "*Span"), and returns "" for a plain function.
func receiverType(decl *ast.FuncDecl) string {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return ""
	}
	switch typ := decl.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := typ.X.(*ast.Ident); ok {
			return "*" + id.Name
		}
	case *ast.Ident:
		return typ.Name
	}
	return ""
}
