// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package endpointhook

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
)

type reporter struct{}

func (reporter) ReportedRoute(*chi.Context) string { return "/test" }

func TestWrapGateAndParams(t *testing.T) {
	for _, tc := range []struct {
		name         string
		marker       bool
		disabled     bool
		depth        int
		wildcard     bool
		keys, values []string
		want         map[string]string
	}{
		{name: "no marker", keys: []string{"id"}, values: []string{"42"}},
		{name: "disabled", marker: true, disabled: true, keys: []string{"id"}, values: []string{"42"}},
		{name: "equal depth", marker: true, depth: 1, keys: []string{"id"}, values: []string{"42"}},
		{name: "empty", marker: true},
		{name: "last wins", marker: true, keys: []string{"id", "*", "id"}, values: []string{"1", "tail", "2"}, want: map[string]string{"id": "2"}},
		{name: "wildcard", marker: true, wildcard: true, keys: []string{"*", "*"}, values: []string{"a", "b"}, want: map[string]string{"*": "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore := SetWovenForTest(true)
			t.Cleanup(restore)
			var got map[string]string
			calls, handled := 0, 0
			t.Cleanup(SetRouteMatchedForTest(func(_ context.Context, route string, p map[string]string) error {
				calls++
				if route != "/test" {
					t.Errorf("route = %q", route)
				}
				got = p
				return nil
			}))
			h := Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { handled++ }), tc.wildcard)
			rctx := chi.NewRouteContext()
			rctx.RoutePatterns = []string{"/test"}
			rctx.URLParams.Keys, rctx.URLParams.Values = tc.keys, tc.values
			ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
			if tc.marker {
				ctx = With(ctx, Marker{Depth: tc.depth, Route: reporter{}})
				if tc.disabled {
					ctx = With(ctx, Marker{Disabled: true})
				}
			}
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(ctx))
			wantCalls := 0
			if tc.want != nil {
				wantCalls = 1
			}
			if calls != wantCalls || handled != 1 || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("calls=%d handled=%d params=%v", calls, handled, got)
			}
		})
	}
}

func TestWrapNilAndWoven(t *testing.T) {
	restore := SetWovenForTest(false)
	defer restore()
	if Woven() {
		t.Fatal("woven before Wrap")
	}
	if Wrap(nil, false) != nil || !Woven() {
		t.Fatal("nil or woven state")
	}
}

func TestWrapZeroValueState(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "wrap_appsec.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == "init" {
				t.Error("init found")
			}
			if d.Name.Name == "Wrap" {
				ast.Inspect(d.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						t.Errorf("unexpected call in Wrap: %T", call.Fun)
						return true
					}
					id, ok := sel.X.(*ast.Ident)
					if !ok || id.Name != "woven" || sel.Sel.Name != "Store" {
						t.Errorf("unexpected selector in Wrap")
					}
					return true
				})
			}
		case *ast.GenDecl:
			if d.Tok == token.VAR {
				for _, spec := range d.Specs {
					if len(spec.(*ast.ValueSpec).Values) != 0 {
						t.Error("initialized package variable")
					}
				}
			}
		}
	}
}
