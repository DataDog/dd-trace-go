// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package dyngo

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGLSOtelcInjectedIdentifiers guards the identifiers that gls.otelc.yaml
// writes literally into this package. See the equivalent test in
// ddtrace/tracer for the full rationale; in short, otelc's inject_code has no
// template variables, so the rules spell these names out, and this test reports a
// rename as a plain `go test` failure naming the yaml instead of a compile error
// inside an instrumented copy of this package.
//
// Keep in sync with instrumentation/appsec/dyngo/gls.otelc.yaml. All three targets
// are plain functions, so only parameters need checking.
func TestGLSOtelcInjectedIdentifiers(t *testing.T) {
	const file = "operation.go"

	for _, tc := range []struct {
		fn    string
		param int
		want  string
	}{
		// orchestrion.GLSActivate(&ctx, contextKey{}, op, &__dd_o.__dd_glsPop, nil)
		{fn: "RegisterOperation", param: 0, want: "ctx"},
		{fn: "RegisterOperation", param: 1, want: "op"},
		// ctx = orchestrion.WrapContext(ctx)
		{fn: "FromContext", param: 0, want: "ctx"},
		// __dd_o := op.unwrap()
		{fn: "FinishOperation", param: 0, want: "op"},
	} {
		t.Run(fmt.Sprintf("%s/param%d", tc.fn, tc.param), func(t *testing.T) {
			params := funcParamNames(t, file, tc.fn)
			require.Greaterf(t, len(params), tc.param,
				"%s no longer has a parameter at index %d", tc.fn, tc.param)
			assert.Equalf(t, tc.want, params[tc.param],
				"gls.otelc.yaml injects code into %s that refers to parameter %d as %q; "+
					"rename it there too, or the injected code stops compiling", tc.fn, tc.param, tc.want)
		})
	}

	// The injected code also calls op.unwrap() and reads __dd_glsPop off the
	// result, so the method has to keep existing under that name.
	t.Run("unwrap", func(t *testing.T) {
		require.NotNil(t, findMethod(t, file, "unwrap"),
			"gls.otelc.yaml injects `op.unwrap()`; that method no longer exists")
	})
}

// funcParamNames returns the positional parameter names of a plain function,
// flattening grouped declarations such as (a, b string).
func funcParamNames(t *testing.T, file, name string) []string {
	t.Helper()

	for _, decl := range parseDecls(t, file) {
		if decl.Recv != nil || decl.Name.Name != name {
			continue
		}
		var names []string
		for _, f := range decl.Type.Params.List {
			if len(f.Names) == 0 {
				names = append(names, "")
				continue
			}
			for _, n := range f.Names {
				names = append(names, n.Name)
			}
		}
		return names
	}
	t.Fatalf("no function %q in %s", name, file)
	return nil
}

func findMethod(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()

	for _, decl := range parseDecls(t, file) {
		if decl.Recv != nil && decl.Name.Name == name {
			return decl
		}
	}
	return nil
}

// parseDecls parses a single file rather than the package directory, to avoid the
// deprecated ast.Package.
func parseDecls(t *testing.T, file string) []*ast.FuncDecl {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	require.NoError(t, err)

	var out []*ast.FuncDecl
	for _, d := range parsed.Decls {
		if decl, ok := d.(*ast.FuncDecl); ok {
			out = append(out, decl)
		}
	}
	return out
}
