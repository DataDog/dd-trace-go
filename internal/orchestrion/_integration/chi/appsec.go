// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chi

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	chitrace "github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2"
	libddwaf "github.com/DataDog/go-libddwaf/v5"
	"github.com/go-chi/chi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseAppSec struct {
	server  *http.Server
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
	h := func(w http.ResponseWriter, _ *http.Request) { tc.handled.Add(1); w.WriteHeader(http.StatusOK) }
	root.Get("/users/{id}", h)
	root.Route("/routed/{tenant}", func(r chi.Router) { r.Get("/items/{id}", h) })
	child := chi.NewRouter()
	child.Get("/items/{id}", h)
	root.Mount("/mounted/{tenant}", child)
	root.Mount("/files", http.HandlerFunc(h))
	api := chi.NewRouter()
	api.Get("/items/{id}", h)
	root.Mount("/w/{tenant}", http.HandlerFunc(api.ServeHTTP))
	root.With(chitrace.Middleware(chitrace.WithIgnoreRequest(func(*http.Request) bool { return true }))).Get("/ignore/{id}", h)
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
		header  bool
		handled bool
	}{
		{"/users/attack", 403, false, false}, {"/users/42", 200, false, true},
		{"/routed/acme/items/attack", 403, false, false}, {"/routed/attack", 404, false, false},
		{"/mounted/acme/items/attack", 403, false, false}, {"/files/attack", 200, false, true},
		{"/w/attack/items/42", 403, false, false}, {"/w/acme/items/attack", 403, false, false},
		{"/ignore/attack", 200, false, true}, {"/ignore/42", 403, true, false},
		{"/manual/attack", 403, false, false},
	} {
		t.Run(c.path, func(t *testing.T) {
			before := tc.handled.Load()
			req, err := http.NewRequest(http.MethodGet, "http://"+tc.server.Addr+c.path, nil)
			require.NoError(t, err)
			if c.header {
				req.Header.Set("X-Block", "yes")
			}
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
}

func (*TestCaseAppSec) ExpectedSpanCount() int { return 35 }

func (*TestCaseAppSec) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "http.request", "resource": "GET /users/attack"}, Meta: map[string]string{"component": "net/http", "span.kind": "client"}, Children: trace.Traces{{Meta: map[string]string{"component": "net/http", "span.kind": "server"}, Children: trace.Traces{{Meta: map[string]string{"component": "go-chi/chi", "http.status_code": "403"}}}}}}}
}
