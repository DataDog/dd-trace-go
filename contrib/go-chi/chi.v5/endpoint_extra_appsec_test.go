// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2/internal/endpointhook"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func TestEndpointHookSchema(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	t.Setenv("DD_API_SECURITY_SAMPLE_DELAY", "30")
	t.Setenv("DD_API_SECURITY_ENABLED", "true")
	t.Setenv("DD_APPSEC_ENABLED", "true")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	testutils.StartAppSec(t)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	r := chi.NewRouter()
	r.Use(Middleware())
	leaf := endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), false)
	r.Route("/routed/{tenant}", func(s chi.Router) { s.Method("GET", "/items/{id}", leaf) })
	sub := chi.NewRouter()
	sub.Use(Middleware())
	sub.Method("GET", "/items/{id}", leaf)
	r.Mount("/mounted/{tenant}", sub)
	r.Route("/catch/{tenant}", func(s chi.Router) {
		s.Method("GET", "/files/*", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), true))
	})
	for _, path := range []string{"/routed/acme/items/42", "/mounted/acme/items/42", "/catch/acme/files/report"} {
		mt.Reset()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 200, w.Code)
		var schema string
		for _, span := range mt.FinishedSpans() {
			if s, ok := span.Tag("_dd.appsec.s.req.params").(string); ok {
				schema = s
			}
		}
		require.Contains(t, schema, `"tenant"`, path)
		if path == "/catch/acme/files/report" {
			require.Contains(t, schema, `"*"`, path)
		} else {
			require.Contains(t, schema, `"id"`, path)
			require.NotContains(t, schema, `"*"`, path)
		}
	}
}

func TestEndpointManualComparisons(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	startAppSecRules(t, rulePathBlock, pathRule("wildcard-block", "wildcard-test", "*", "block"))
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	r := chi.NewRouter()
	r.Use(Middleware())
	r.With(Middleware()).Get("/files/*", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r.Route("/m/{id}", func(s chi.Router) {
		s.With(Middleware()).Get("/items/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	})
	for _, c := range []struct {
		path   string
		status int
	}{{"/files/attack", 403}, {"/m/attack/items/42", 403}, {"/m/42/items/attack", 200}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", c.path, nil))
		require.Equal(t, c.status, w.Code, c.path)
	}
}

func TestEndpointHookMountedOptOut(t *testing.T) {
	const headerRule = `{"id":"header-block","name":"header-block","tags":{"type":"header-test","category":"attack_attempt"},"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.headers.no_cookies","key_path":["x-block"]}],"regex":"^yes$"}}],"on_match":["block"]}`
	for _, mode := range []string{"ignore", "noappsec"} {
		t.Run(mode, func(t *testing.T) {
			t.Cleanup(endpointhook.SetWovenForTest(true))
			startEndpointAppSecRules(t, "1s", rulePathBlock, headerRule)
			mt := mocktracer.Start()
			t.Cleanup(mt.Stop)
			calls := 0
			t.Cleanup(endpointhook.SetRouteMatchedForTest(func(ctx context.Context, route string, params map[string]string) error {
				calls++
				return httpsec.RouteMatched(ctx, route, params)
			}))
			r := chi.NewRouter()
			r.Use(Middleware())
			sub := chi.NewRouter()
			if mode == "ignore" {
				sub.Use(Middleware(WithIgnoreRequest(func(*http.Request) bool { return true })))
			} else {
				sub.Use(Middleware(WithNoAppsec(true)))
			}
			sub.Method("GET", "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), false))
			r.Mount("/sub", sub)
			for _, c := range []struct {
				path   string
				header bool
				status int
			}{{"/sub/users/attack", false, 200}, {"/sub/users/42", true, 403}} {
				mt.Reset()
				req := httptest.NewRequest("GET", c.path, nil)
				if c.header {
					req.Header.Set("X-Block", "yes")
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, c.status, w.Code)
				for _, span := range mt.FinishedSpans() {
					require.NotContains(t, eventRules(span.Tag("_dd.appsec.json")), "path-block")
				}
			}
			require.Zero(t, calls)
		})
	}
}

func TestEndpointHookNoOperation(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	startEndpointAppSecRules(t, "1s", rulePathBlock)
	r := chi.NewRouter()
	r.Method("GET", "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), false))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/users/attack", nil))
	require.Equal(t, 200, w.Code)
}

type endpointTagSink struct{}

func (endpointTagSink) SetTag(string, any) {}

func TestEndpointHookNoChiMiddleware(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	startEndpointAppSecRules(t, "1s", rulePathBlock)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	calls := 0
	t.Cleanup(endpointhook.SetRouteMatchedForTest(func(context.Context, string, map[string]string) error { calls++; return nil }))
	r := chi.NewRouter()
	r.Method("GET", "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), false))
	h := httpsec.WrapHandler(r, endpointTagSink{}, &httpsec.Config{Framework: "outer"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/users/attack", nil))
	require.Equal(t, 200, w.Code)
	require.Zero(t, calls)
}

func TestEndpointHookAppSecOff(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	require.False(t, instr.AppSecEnabled())
	recordEndpointLogs(t)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	calls := 0
	t.Cleanup(endpointhook.SetRouteMatchedForTest(func(context.Context, string, map[string]string) error { calls++; return nil }))
	r := chi.NewRouter()
	r.Use(Middleware())
	r.Method("GET", "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), false))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/users/attack", nil))
	require.Equal(t, 200, w.Code)
	require.Zero(t, calls)
}

func TestEndpointHookLeafGroup(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	startAppSecRules(t, rulePathBlock)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	calls := 0
	t.Cleanup(endpointhook.SetRouteMatchedForTest(func(context.Context, string, map[string]string) error { calls++; return nil }))
	r := chi.NewRouter()
	r.Use(Middleware())
	handled := 0
	r.Group(func(g chi.Router) {
		g.Use(Middleware())
		g.Method("GET", "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { handled++; w.WriteHeader(200) }), false))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/users/attack", nil))
	require.Equal(t, 403, w.Code)
	require.Zero(t, calls)
	require.Len(t, mt.FinishedSpans(), 2)
	mt.Reset()
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/users/42", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, 1, handled)
	require.Len(t, mt.FinishedSpans(), 2)
	require.Zero(t, calls)
}
