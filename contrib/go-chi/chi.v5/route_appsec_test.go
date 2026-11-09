// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

func TestRouteParams(t *testing.T) {
	ctx := func(keys, values []string) *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Keys, rctx.URLParams.Values = keys, values
		return rctx
	}

	assert.Nil(t, routeParams(ctx(nil, nil)), "no parameters")
	assert.Nil(t, routeParams(ctx([]string{"a"}, nil)), "the lengths differ")
	assert.Nil(t, routeParams(ctx([]string{"a", "b"}, []string{"1"})), "the lengths differ")
	assert.Equal(t, map[string]string{"a": "1", "b": "2"}, routeParams(ctx([]string{"a", "b"}, []string{"1", "2"})))
	// The last value wins, like chi.URLParam.
	rctx := ctx([]string{"a", "*", "a"}, []string{"1", "x", "3"})
	assert.Equal(t, map[string]string{"a": "3", "*": "x"}, routeParams(rctx))
	assert.Equal(t, "3", rctx.URLParam("a"))

	// The result is a new map.
	rctx = ctx([]string{"a"}, []string{"1"})
	params := routeParams(rctx)
	rctx.URLParams.Values[0] = "changed"
	assert.Equal(t, "1", params["a"])
}

func TestLookupRoute(t *testing.T) {
	handler := func(http.ResponseWriter, *http.Request) {}
	cfg := new(config)
	defaults(cfg)

	child := chi.NewRouter()
	child.Get("/attack", handler)
	child.Get("/users/{id}", handler)
	child.Get("/files/*", handler)
	child.Get("/deep/{a}/{b}", handler)

	grandchild := chi.NewRouter()
	grandchild.Get("/leaf/{id}", handler)
	middle := chi.NewRouter()
	middle.Mount("/inner", grandchild)

	root := chi.NewRouter()
	root.Get("/", handler)
	root.Get("/items/{id}", handler)
	root.Get("/only-get", handler)
	root.Mount("/api", child)
	root.Mount("/mid", middle)
	root.Mount("/opaque", opaqueHandler(child))
	root.Mount("/t/{tid}", child)

	for _, tt := range []struct {
		name       string
		method     string
		target     string
		prepare    func(*chi.Context)
		wantRoute  string
		wantParams map[string]string
		wantFound  bool
	}{
		{name: "param leaf", target: "/items/42", wantRoute: "/items/{id}", wantParams: map[string]string{"id": "42"}, wantFound: true},
		{name: "root path", target: "/", wantRoute: "/", wantFound: true},
		{name: "no match", target: "/nothing", wantFound: false},
		{name: "method not allowed", method: http.MethodPost, target: "/only-get", wantFound: false},
		{name: "unknown method", method: "BREW", target: "/items/42", wantFound: false},
		{name: "literal leaf in a mount", target: "/api/attack", wantRoute: "/api/attack", wantParams: map[string]string{"*": ""}, wantFound: true},
		{name: "param leaf in a mount", target: "/api/users/7", wantRoute: "/api/users/{id}", wantParams: map[string]string{"*": "", "id": "7"}, wantFound: true},
		{name: "wildcard leaf in a mount", target: "/api/files/a/b", wantRoute: "/api/files/*", wantParams: map[string]string{"*": "a/b"}, wantFound: true},
		{name: "2 parameters", target: "/api/deep/x/y", wantRoute: "/api/deep/{a}/{b}", wantParams: map[string]string{"*": "", "a": "x", "b": "y"}, wantFound: true},
		{name: "nested mounts", target: "/mid/inner/leaf/9", wantRoute: "/mid/inner/leaf/{id}", wantParams: map[string]string{"*": "", "id": "9"}, wantFound: true},
		{name: "mount behind a wrapper", target: "/opaque/users/7", wantRoute: "/opaque/*", wantParams: map[string]string{"*": "users/7"}, wantFound: true},
		{name: "route path", target: "/ignored", prepare: func(rctx *chi.Context) { rctx.RoutePath = "/items/5" },
			wantRoute: "/items/{id}", wantParams: map[string]string{"id": "5"}, wantFound: true},
		{name: "route method", method: http.MethodHead, target: "/only-get", prepare: func(rctx *chi.Context) { rctx.RouteMethod = http.MethodGet },
			wantRoute: "/only-get", wantFound: true},
		{name: "already routed: the whole path of the request", target: "/api/users/7",
			prepare:   func(rctx *chi.Context) { rctx.RoutePath, rctx.RoutePatterns = "/users/7", []string{"/api/*"} },
			wantRoute: "/api/users/{id}", wantParams: map[string]string{"*": "", "id": "7"}, wantFound: true},
		{name: "contradicts the routes that chi matched", target: "/api/users/7",
			prepare: func(rctx *chi.Context) { rctx.RoutePatterns = []string{"/other/*"} }, wantFound: false},
		{name: "same patterns and parameters as chi matched", target: "/t/clean/users/7",
			prepare: func(rctx *chi.Context) {
				rctx.RoutePatterns = []string{"/t/{tid}/*"}
				rctx.URLParams.Add("tid", "clean")
				rctx.URLParams.Add("*", "")
			},
			wantRoute: "/t/{tid}/users/{id}", wantParams: map[string]string{"tid": "clean", "*": "", "id": "7"}, wantFound: true},
		{name: "same patterns, another value of a parameter", target: "/t/clean/users/7",
			prepare: func(rctx *chi.Context) {
				rctx.RoutePatterns = []string{"/t/{tid}/*"}
				rctx.URLParams.Add("tid", "attack")
				rctx.URLParams.Add("*", "")
			},
			wantFound: false},
		{name: "route is final", target: "/items/42", prepare: func(rctx *chi.Context) { rctx.RoutePatterns = []string{"/items/{id}"} }, wantFound: false},
		{name: "raw path", target: "/items/a%2Fb", wantRoute: "/items/{id}", wantParams: map[string]string{"id": "a%2Fb"}, wantFound: true},
		{name: "empty path", target: "", wantRoute: "/", wantFound: true},
		{name: "no router", target: "/items/42", prepare: func(rctx *chi.Context) { rctx.Routes = nil }, wantFound: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			rctx := chi.NewRouteContext()
			rctx.Routes = root
			if tt.prepare != nil {
				tt.prepare(rctx)
			}
			req := httptest.NewRequest(method, "/", nil)
			if tt.target == "" {
				req.URL.Path, req.URL.RawPath = "", "" // chi routes it as "/"
			} else {
				req = httptest.NewRequest(method, tt.target, nil)
			}
			route, params, found := lookupRoute(rctx, req, cfg)
			assert.Equal(t, tt.wantFound, found)
			assert.Equal(t, tt.wantRoute, route)
			assert.Equal(t, tt.wantParams, params)
		})
	}

	t.Run("modify resource name", func(t *testing.T) {
		cfg := new(config)
		defaults(cfg)
		cfg.modifyResourceName = func(s string) string { return "x:" + s }
		route, _, found := lookupRoute(&chi.Context{Routes: root}, httptest.NewRequest(http.MethodGet, "/items/1", nil), cfg)
		assert.True(t, found)
		assert.Equal(t, "x:/items/{id}", route)
	})
}

func TestLookupParams(t *testing.T) {
	tmp := chi.NewRouteContext()
	assert.Nil(t, lookupParams(tmp))
	tmp.URLParams.Keys, tmp.URLParams.Values = []string{"id"}, nil
	assert.Nil(t, lookupParams(tmp), "the lengths differ")
}

func TestEarlyDataSameAs(t *testing.T) {
	params := map[string]string{"id": "1"}
	early := snapshotEarly("/items/{id}", params)
	assert.True(t, early.sameAs("/items/{id}", map[string]string{"id": "1"}))
	assert.False(t, early.sameAs("/items", map[string]string{"id": "1"}), "another route")
	assert.False(t, early.sameAs("/items/{id}", map[string]string{"id": "2"}), "another value")
	assert.False(t, early.sameAs("/items/{id}", nil), "no parameters")
	assert.False(t, early.sameAs("/items/{id}", map[string]string{"id": "1", "x": ""}), "another key")

	// A change of the map that the WAF got does not change the comparison.
	params["id"] = "changed"
	assert.True(t, early.sameAs("/items/{id}", map[string]string{"id": "1"}))

	// nil and empty are the same.
	assert.True(t, snapshotEarly("/", nil).sameAs("/", map[string]string{}))
	assert.True(t, snapshotEarly("/", map[string]string{}).sameAs("/", nil))
}

// BenchmarkLookupRoute measures the route lookup that runs before the handler:
// 20 routes with 2 parameters, and a mount.
func BenchmarkLookupRoute(b *testing.B) {
	handler := func(http.ResponseWriter, *http.Request) {}
	cfg := new(config)
	defaults(cfg)

	child := chi.NewRouter()
	child.Get("/users/{id}/orders/{order}", handler)
	root := chi.NewRouter()
	for i := range 18 {
		root.Get(fmt.Sprintf("/resource%d/{id}/items/{item}", i), handler)
	}
	root.Mount("/api", child)

	for name, target := range map[string]string{
		"leaf":  "/resource9/42/items/7",
		"mount": "/api/users/42/orders/7",
		"miss":  "/missing/42",
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			req := httptest.NewRequest(http.MethodGet, target, nil)
			for i := 0; i < b.N; i++ {
				lookupRoute(&chi.Context{Routes: root}, req, cfg)
			}
		})
	}
}

func TestKnownParams(t *testing.T) {
	rctx := chi.NewRouteContext()
	assert.Nil(t, knownParams(rctx))
	rctx.URLParams.Keys, rctx.URLParams.Values = []string{"id", "*", "id"}, []string{"parent", "x", "child"}
	assert.Equal(t, map[string]string{"id": "parent", "*": "x"}, knownParams(rctx))
	assert.Equal(t, map[string]string{"id": "child", "*": "x"}, routeParams(rctx), "the final report: the last value wins")
	rctx.URLParams.Values = rctx.URLParams.Values[:2]
	assert.Nil(t, knownParams(rctx), "the lengths differ")
}

func TestAddPrediction(t *testing.T) {
	// Nothing known: the prediction.
	got, ok := addPrediction(nil, map[string]string{"id": "1"})
	assert.True(t, ok)
	assert.Equal(t, map[string]string{"id": "1"}, got)

	// The prediction adds names, and keeps the known values.
	got, ok = addPrediction(map[string]string{"tid": "t"}, map[string]string{"tid": "t", "id": "1"})
	assert.True(t, ok)
	assert.Equal(t, map[string]string{"tid": "t", "id": "1"}, got)

	// Another value for a known name: the prediction contradicts chi.
	_, ok = addPrediction(map[string]string{"id": "attack"}, map[string]string{"id": "clean"})
	assert.False(t, ok)

	// The "*" of a mount that the tracer wraps is not reset yet: the known value stays.
	got, ok = addPrediction(map[string]string{"*": "rest"}, map[string]string{"*": "", "id": "1"})
	assert.True(t, ok)
	assert.Equal(t, map[string]string{"*": "rest", "id": "1"}, got)

	// The input maps do not change.
	known, predicted := map[string]string{"a": "1"}, map[string]string{"b": "2"}
	_, _ = addPrediction(known, predicted)
	assert.Equal(t, map[string]string{"a": "1"}, known)
	assert.Equal(t, map[string]string{"b": "2"}, predicted)
}
