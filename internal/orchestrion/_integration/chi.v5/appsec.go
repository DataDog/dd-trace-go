// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chiv5

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	chitrace "github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2"
	libddwaf "github.com/DataDog/go-libddwaf/v5"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseAppSec struct {
	server  *http.Server
	root    *chi.Mux
	handled atomic.Int32
}

func (*TestCaseAppSec) PreBootstrap(_ context.Context, t *testing.T) {
	if ok, err := libddwaf.Usable(); !ok {
		t.Skip("WAF is not available:", err)
	}
	t.Setenv("DD_APPSEC_RULES", "../testdata/chi-path-params.json")
}

func (tc *TestCaseAppSec) Setup(_ context.Context, t *testing.T) {
	root := chi.NewRouter()
	tc.root = root
	h := func(w http.ResponseWriter, _ *http.Request) { tc.handled.Add(1); w.WriteHeader(http.StatusOK) }
	root.Get("/users/{id}", h)
	root.Get("/catch/*", h)
	newRouter := chi.NewRouter
	hx := newRouter()
	hx.Get("/hx/{tenant}/{name}", h)
	root.Handle("/hx/{tenant}/*", hx)
	root.Route("/routed/{tenant}", func(r chi.Router) { r.Get("/items/{id}", h) })
	child := chi.NewRouter()
	child.Get("/items/{id}", h)
	root.Mount("/mounted/{tenant}", child)
	root.Mount("/files", http.HandlerFunc(h))
	root.Mount("/terminal/{id}", http.HandlerFunc(h))
	api := chi.NewRouter()
	api.Get("/items/{id}", h)
	root.Mount("/w/{tenant}", http.HandlerFunc(api.ServeHTTP))
	root.With(chitrace.Middleware(chitrace.WithIgnoreRequest(func(*http.Request) bool { return true }))).Get("/ignore/{id}", h)
	root.With(chitrace.Middleware(chitrace.WithNoAppsec(true))).Get("/opt/{id}", h)
	root.With(chitrace.Middleware()).Get("/manual/{id}", h)
	ln := net.FreeListener(t)
	tc.server = &http.Server{Addr: ln.Addr().String(), Handler: root}
	go func() { assert.ErrorIs(t, tc.server.Serve(ln), http.ErrServerClosed) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.server.Shutdown(ctx))
	})
}

func (tc *TestCaseAppSec) Run(_ context.Context, t *testing.T) {
	for _, c := range []struct {
		path    string
		status  int
		handled bool
	}{
		{"/users/attack", 403, false}, {"/users/42", 200, true},
		{"/catch/attack", 403, false}, {"/hx/acme/attack", 200, true},
		{"/routed/acme/items/attack", 403, false}, {"/routed/attack", 404, false}, {"/routed/attack/", 404, false},
		{"/terminal/attack", 403, false}, {"/terminal/attack/", 403, false},
		{"/terminal/42", 200, true}, {"/terminal/42/", 200, true},
		{"/terminal/attack/child", 403, false}, {"/terminal/42/child", 200, true},
		{"/mounted/acme/items/attack", 403, false}, {"/files/attack", 200, true},
		{"/w/attack/items/42", 403, false}, {"/w/acme/items/attack", 403, false},
		{"/ignore/attack", 200, true},
		{"/opt/attack", 200, true},
		{"/manual/attack", 403, false},
	} {
		t.Run(c.path, func(t *testing.T) {
			before := tc.handled.Load()
			req, err := http.NewRequest(http.MethodGet, "http://"+tc.server.Addr+c.path, nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, c.status, resp.StatusCode)
			if c.handled {
				require.Equal(t, before+1, tc.handled.Load())
			} else {
				require.Equal(t, before, tc.handled.Load())
			}
		})
	}
	for _, path := range []string{"/ignore/42", "/opt/42"} {
		before := tc.handled.Load()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Block", "yes")
		w := httptest.NewRecorder()
		tc.root.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
		require.Equal(t, before, tc.handled.Load())
	}
}

func (*TestCaseAppSec) ExpectedSpanCount() int { return 66 }

func (*TestCaseAppSec) ExpectedTraces() trace.Traces {
	cases := []struct {
		client, route, status string
		chiSpans              int
	}{
		{"GET /users/attack", "GET /users/{id}", "403", 1},
		{"GET /users/42", "GET /users/{id}", "200", 1},
		{"GET /catch/attack", "GET /catch/*", "403", 1},
		{"GET /hx/acme/attack", "GET /hx/{tenant}/hx/{tenant}/{name}", "200", 1},
		{"GET /routed/acme/items/attack", "GET /routed/{tenant}/items/{id}", "403", 1},
		{"GET /routed/attack", "GET /routed/{tenant}", "404", 1},
		{"GET /routed/attack/", "GET /routed/{tenant}", "404", 1},
		{"GET /terminal/attack", "GET /terminal/{id}", "403", 1},
		{"GET /terminal/attack/", "GET /terminal/{id}", "403", 1},
		{"GET /terminal/42", "GET /terminal/{id}", "200", 1},
		{"GET /terminal/42/", "GET /terminal/{id}", "200", 1},
		{"GET /terminal/attack/child", "GET /terminal/{id}/*", "403", 1},
		{"GET /terminal/42/child", "GET /terminal/{id}/*", "200", 1},
		{"GET /mounted/acme/items/attack", "GET /mounted/{tenant}/items/{id}", "403", 2},
		{"GET /files/attack", "GET /files/*", "200", 1},
		{"GET /w/attack/items/42", "GET /w/{tenant}/*", "403", 1},
		{"GET /w/acme/items/attack", "GET /w/{tenant}/items/{id}", "403", 2},
		{"GET /ignore/attack", "GET /ignore/{id}", "200", 1},
		{"GET /opt/attack", "GET /opt/{id}", "200", 2},
		{"GET /manual/attack", "GET /manual/{id}", "403", 2},
	}
	traces := make(trace.Traces, 0, len(cases))
	for _, c := range cases {
		root := &trace.Trace{Tags: map[string]any{"name": "http.request", "resource": c.client}, Meta: map[string]string{"component": "net/http", "span.kind": "client", "http.status_code": c.status}}
		server := &trace.Trace{Meta: map[string]string{"component": "net/http", "span.kind": "server", "http.status_code": c.status}}
		switch c.client {
		case "GET /users/attack", "GET /routed/acme/items/attack", "GET /mounted/acme/items/attack", "GET /w/acme/items/attack", "GET /manual/attack", "GET /terminal/attack", "GET /terminal/attack/", "GET /terminal/attack/child":
			server.MetaContains = map[string]string{"_dd.appsec.json": `"id":"id-block"`}
		case "GET /catch/attack":
			server.MetaContains = map[string]string{"_dd.appsec.json": `"id":"wildcard-block"`}
		case "GET /w/attack/items/42":
			server.MetaContains = map[string]string{"_dd.appsec.json": `"id":"tenant-block"`}
		default:
			server.MetaAbsent = []string{"_dd.appsec.json"}
		}
		root.Children = trace.Traces{server}
		parent := server
		for i := 0; i < c.chiSpans; i++ {
			chiSpan := &trace.Trace{Tags: map[string]any{"resource": c.route}, Meta: map[string]string{"component": "go-chi/chi.v5", "http.status_code": c.status}}
			parent.Children = trace.Traces{chiSpan}
			parent = chiSpan
		}
		traces = append(traces, root)
	}
	for _, path := range []string{"/ignore/42", "/opt/42"} {
		traces = append(traces, &trace.Trace{
			Meta:         map[string]string{"component": "go-chi/chi.v5", "http.status_code": "403"},
			MetaContains: map[string]string{"_dd.appsec.json": `"id":"header-block"`, "http.url": path},
		})
	}
	return traces
}
