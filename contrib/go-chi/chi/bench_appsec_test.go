// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2/internal/endpointhook"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

// benchUnwrapper is a response writer wrapper with Unwrap(). It does not use
// any other symbol of the tests, so that this file also builds without the
// commit tracker, to compare the cost against the code before it.
type benchUnwrapper struct{ http.ResponseWriter }

func (w benchUnwrapper) Unwrap() http.ResponseWriter { return w.ResponseWriter }

const benchRules = `{
	"version": "2.2",
	"metadata": {"rules_version": "1.0.0"},
	"rules": [{
		"id": "bench-path",
		"name": "bench-path",
		"tags": {"type": "bench", "category": "attack_attempt"},
		"conditions": [{"operator": "match_regex", "parameters": {"inputs": [{"address": "server.request.path_params", "key_path": ["id"]}], "regex": "attack"}}],
		"on_match": ["block"]
	}]
}`

// BenchmarkMiddlewareAppSec measures a request through Use(Middleware()) with
// AppSec enabled, for 0, 4 and 16 writers with Unwrap() under chi's writer.
// The request has the commit tracker and, when chi selects another route, the
// route report after the handler. The WAF gets no path parameters. A chain of 16
// writers is too long for the tracker to find an outer tracker.
func BenchmarkMiddlewareAppSec(b *testing.B) {
	for _, mode := range []string{"plain-appsec-off", "woven-appsec-off"} {
		b.Run(mode, func(b *testing.B) {
			if instr.AppSecEnabled() {
				b.Fatal("AppSec must be off")
			}
			b.Cleanup(endpointhook.SetWovenForTest(mode == "woven-appsec-off"))
			mt := mocktracer.Start()
			b.Cleanup(mt.Stop)
			r := chi.NewRouter()
			r.Use(Middleware())
			leaf := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			if mode == "woven-appsec-off" {
				r.Method(http.MethodGet, "/users/{id}", endpointhook.Wrap(leaf, false))
			} else {
				r.Method(http.MethodGet, "/users/{id}", leaf)
			}
			req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r.ServeHTTP(httptest.NewRecorder(), req)
				if i%1000 == 0 {
					mt.Reset()
				}
			}
		})
	}
	rules := filepath.Join(b.TempDir(), "rules.json")
	require.NoError(b, os.WriteFile(rules, []byte(benchRules), 0o600))
	b.Setenv("DD_APPSEC_ENABLED", "true")
	b.Setenv("DD_APPSEC_RULES", rules)
	testutils.StartAppSecBench(b)
	if !instr.AppSecEnabled() {
		b.Skip("could not enable AppSec: this platform is likely not supported")
	}
	mt := mocktracer.Start()
	defer mt.Stop()

	for _, mode := range []string{"plain-ignored", "woven-ignored"} {
		b.Run(mode, func(b *testing.B) {
			woven := mode == "woven-ignored"
			b.Cleanup(endpointhook.SetWovenForTest(woven))
			r := chi.NewRouter()
			r.Use(Middleware(WithIgnoreRequest(func(*http.Request) bool { return true })))
			leaf := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			if woven {
				r.Method(http.MethodGet, "/users/{id}", endpointhook.Wrap(leaf, false))
			} else {
				r.Method(http.MethodGet, "/users/{id}", leaf)
			}
			req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r.ServeHTTP(httptest.NewRecorder(), req)
				if i%1000 == 0 {
					mt.Reset()
				}
			}
		})
	}

	for _, mode := range []string{"woven-root", "manual-stacked"} {
		b.Run(mode, func(b *testing.B) {
			b.Cleanup(endpointhook.SetWovenForTest(mode == "woven-root"))
			r := chi.NewRouter()
			r.Use(Middleware())
			leaf := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			if mode == "woven-root" {
				r.Method(http.MethodGet, "/users/{id}", endpointhook.Wrap(leaf, false))
			} else {
				r.With(Middleware()).Get("/users/{id}", leaf)
			}
			req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r.ServeHTTP(httptest.NewRecorder(), req)
				if i%1000 == 0 {
					mt.Reset()
				}
			}
		})
	}

	router := chi.NewRouter()
	router.Use(Middleware())
	router.Get("/users/{id}/orders/{order}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// 4 routers in 3 levels of mounts, each with the tracer (what Orchestrion
	// does): each tracer wraps the writer in a commit tracker and, after the handler,
	// reads the route that chi selected and reports it when it is not its start
	// route. This shows the cost of nested tracers and of their route reports.
	nested := chi.NewRouter()
	nested.Use(Middleware())
	level := nested
	for _, prefix := range []string{"/a/{x}", "/b/{y}", "/c/{z}"} {
		child := chi.NewRouter()
		child.Use(Middleware())
		level.Mount(prefix, child)
		level = child
	}
	level.Get("/items/{id}/orders/{order}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, c := range []struct {
		name   string
		target string
		depth  int
	}{
		{"depth=0", "/users/42/orders/7", 0},
		{"depth=4", "/users/42/orders/7", 4},
		{"depth=16", "/users/42/orders/7", 16},
		{"no-params", "/health", 0},
		{"nested-4-tracers", "/a/1/b/2/c/3/items/42/orders/7", 0},
	} {
		depth := c.depth
		handler := http.Handler(router)
		if c.name == "nested-4-tracers" {
			handler = nested
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			req := httptest.NewRequest(http.MethodGet, c.target, nil)
			for i := 0; i < b.N; i++ {
				var w http.ResponseWriter = httptest.NewRecorder()
				for range depth {
					w = benchUnwrapper{w}
				}
				handler.ServeHTTP(w, req)
				if i%1000 == 0 {
					mt.Reset()
				}
			}
		})
	}
}
