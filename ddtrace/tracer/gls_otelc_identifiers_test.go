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

// TestGLSOtelcInjectedIdentifiers guards the identifiers that otelc.yaml
// writes literally into this package.
//
// otelc's inject_code takes a raw string and has no template variables, so unlike
// orchestrion (which resolves parameters positionally through .Function.Argument)
// the rules must name the receiver and parameters exactly as the source does.
//
// A rename normally breaks the otelc build anyway. What this test buys is where
// the failure shows up: a plain `go test` naming the yaml and the parameter,
// instead of a compile error in the otelc CI lane. It also covers the one case
// that would not fail loudly, where an unrelated symbol of the same name happens
// to be in scope at the injection site.
//
// Keep in sync with ddtrace/tracer/otelc.yaml.
func TestGLSOtelcInjectedIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		file string
		recv string // receiver type as written, "" for a plain function
		fn   string
		// param is the index of the parameter whose name the rules reference, or
		// -1 when the rules reference the receiver instead.
		param int
		want  string
	}{
		// orchestrion.GLSActivate(nil, internal.ActiveSpanKey, s, &s.__dd_glsPop, &s.__dd_glsDone)
		{file: "context.go", fn: "ContextWithSpan", param: 1, want: "s"},
		// ctx = orchestrion.WrapContext(ctx)
		{file: "context.go", fn: "SpanFromContext", param: 0, want: "ctx"},
		// orchestrion.GLSDeactivate(&s.__dd_glsDone, &s.__dd_glsPop)
		{file: "span.go", recv: "*Span", fn: "Finish", param: -1, want: "s"},
		// orchestrion.GLSReset(&s.__dd_glsDone, &s.__dd_glsPop)
		{file: "span.go", recv: "*Span", fn: "clear", param: -1, want: "s"},
	} {
		name := tc.fn
		if tc.recv != "" {
			name = "(" + tc.recv + ")." + tc.fn
		}
		t.Run(name, func(t *testing.T) {
			decl := findFuncDecl(t, tc.file, tc.recv, tc.fn)

			if tc.param < 0 {
				require.NotNil(t, decl.Recv, "expected a method")
				assert.Equalf(t, tc.want, fieldNames(decl.Recv.List)[0],
					"otelc.yaml injects code into %s that refers to the receiver as %q; "+
						"rename it there too, or the injected code stops compiling", name, tc.want)
				return
			}

			params := fieldNames(decl.Type.Params.List)
			require.Greaterf(t, len(params), tc.param,
				"%s no longer has a parameter at index %d", name, tc.param)
			assert.Equalf(t, tc.want, params[tc.param],
				"otelc.yaml injects code into %s that refers to parameter %d as %q; "+
					"rename it there too, or the injected code stops compiling", name, tc.param, tc.want)
		})
	}
}

// findFuncDecl locates a function or method declaration in one of this package's
// source files. Individual files are parsed rather than the whole directory to
// avoid the deprecated ast.Package.
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

// fieldNames flattens a field list into positional names, so that grouped
// declarations such as (a, b string) yield one entry each.
func fieldNames(fields []*ast.Field) []string {
	var names []string
	for _, f := range fields {
		if len(f.Names) == 0 { // unnamed (e.g. `_ int` is named, `int` is not)
			names = append(names, "")
			continue
		}
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
	}
	return names
}
