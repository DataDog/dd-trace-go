// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2/internal/endpointhook"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/go-chi/chi"
	"github.com/stretchr/testify/require"
)

func recordEndpointLogs(t *testing.T) *metricsRecorder {
	t.Helper()
	var check func()
	t.Cleanup(func() { check() })
	rec := testutils.StartTelemetryRecorder(t)
	check = func() {
		for _, line := range rec.Logs {
			require.NotContains(t, line.Text, "RouteMatched called without an active HandlerOperation")
		}
	}
	return &metricsRecorder{snap: func() map[string]float64 {
		out := map[string]float64{}
		for key, handle := range rec.Metrics {
			if value := handle.Get(); value != 0 {
				out[key.Name+"{"+key.Tags+"}"] = value
			}
		}
		return out
	}}
}

func startEndpointAppSecRules(t *testing.T, timeout string, rules ...string) *metricsRecorder {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(path, []byte(rulesJSON(rules...)), 0o600))
	t.Setenv("DD_APPSEC_RULES", path)
	t.Setenv("DD_APPSEC_ENABLED", "true")
	t.Setenv("DD_API_SECURITY_ENABLED", "true")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", timeout)
	metrics := recordEndpointLogs(t)
	testutils.StartAppSec(t)
	return metrics
}

func TestEndpointHookRootUse(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	startAppSecRules(t, rulePathBlock)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	var calls atomic.Int32
	capture := newOpCapture()
	r := chi.NewRouter()
	r.Use(capture.middleware)
	r.Use(Middleware())
	r.Method(http.MethodGet, "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}), false))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/users/attack")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.EqualValues(t, 0, calls.Load())
	require.Len(t, mt.FinishedSpans(), 1)
	require.Equal(t, "/users/{id}", mt.FinishedSpans()[0].Tag("http.route"))
	require.Equal(t, []string{"/users/{id}"}, capture.routes())
	require.Equal(t, []string{"path-block"}, eventRules(mt.FinishedSpans()[0].Tag("_dd.appsec.json")))
}

func TestEndpointHookOptOuts(t *testing.T) {
	const headerRule = `{"id":"header-block","name":"header-block","tags":{"type":"header-test","category":"attack_attempt"},"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.headers.no_cookies","key_path":["x-block"]}],"regex":"^yes$"}}],"on_match":["block"]}`
	for _, mode := range []string{"ignore"} {
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
			var option Option
			option = WithIgnoreRequest(func(*http.Request) bool { return true })
			r.With(Middleware(option)).Method(http.MethodGet, "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), false))
			for _, tc := range []struct {
				path          string
				blockedHeader bool
				status        int
			}{{"/users/attack", false, 200}, {"/users/42", true, 403}} {
				req := httptest.NewRequest(http.MethodGet, tc.path, nil)
				if tc.blockedHeader {
					req.Header.Set("X-Block", "yes")
				}
				mt.Reset()
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, tc.status, w.Code)
				for _, span := range mt.FinishedSpans() {
					require.NotContains(t, eventRules(span.Tag("_dd.appsec.json")), "path-block")
				}
			}
			require.Zero(t, calls)
		})
	}
}

type observingWriter struct {
	http.ResponseWriter
	written bool
}

func (w *observingWriter) WriteHeader(status int) {
	w.written = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *observingWriter) Write(p []byte) (int, error) {
	w.written = true
	return w.ResponseWriter.Write(p)
}
func (w *observingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestEndpointHookEarlyBlockBody(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	metrics := startAppSecRules(t, rulePathBlock)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	before := metrics.snapshot()
	r := chi.NewRouter()
	r.Use(Middleware())
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			spy := &observingWriter{ResponseWriter: w}
			next.ServeHTTP(spy, r)
			if !spy.written {
				_, _ = spy.Write([]byte("default-body"))
			}
		})
	})
	r.Method(http.MethodGet, "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), false))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/users/attack")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	var payload struct {
		SecurityResponseID string `json:"security_response_id"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))
	require.NotEmpty(t, payload.SecurityResponseID)
	require.Equal(t, fmt.Sprintf(`{"errors":[{"title":"You've been blocked","detail":"Sorry, you cannot access this page. Please contact the customer service team. Security provided by Datadog."}],"security_response_id":%q}`, payload.SecurityResponseID), string(body))
	require.Equal(t, float64(1), count(metrics.since(before), "waf.requests", "request_blocked:true", "block_failure:false"))
}

func TestEndpointHookWrappedMount(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	startAppSecRules(t, rulePathBlock)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	var received []map[string]string
	t.Cleanup(endpointhook.SetRouteMatchedForTest(func(ctx context.Context, route string, params map[string]string) error {
		received = append(received, params)
		return httpsec.RouteMatched(ctx, route, params)
	}))
	r := chi.NewRouter()
	r.Use(Middleware())
	sub := chi.NewRouter()
	sub.Method(http.MethodGet, "/t/{tenant}/items/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), false))
	r.Handle("/t/{tenant}/*", endpointhook.Wrap(sub, false))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/t/acme/items/42", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []map[string]string{{"tenant": "acme"}, {"tenant": "acme", "id": "42"}}, received)
}

func TestEndpointHookCatchAll(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	startAppSecRules(t, pathRule("wildcard-block", "wildcard-block", "*", "block"))
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	var received []map[string]string
	t.Cleanup(endpointhook.SetRouteMatchedForTest(func(ctx context.Context, route string, params map[string]string) error {
		received = append(received, params)
		return httpsec.RouteMatched(ctx, route, params)
	}))
	r := chi.NewRouter()
	r.Use(Middleware())
	leaf := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/get/*", endpointhook.Wrap(leaf, true).ServeHTTP)
	r.Handle("/handle/*", endpointhook.Wrap(leaf, false))
	r.Route("/routed/{tenant}", func(sub chi.Router) { sub.Get("/files/*", endpointhook.Wrap(leaf, true).ServeHTTP) })
	for _, tc := range []struct {
		path   string
		status int
		params map[string]string
	}{
		{"/get/attack", http.StatusForbidden, map[string]string{"*": "attack"}},
		{"/handle/attack", http.StatusOK, nil},
		{"/routed/acme/files/attack", http.StatusForbidden, map[string]string{"tenant": "acme", "*": "attack"}},
	} {
		received = nil
		mt.Reset()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		require.Equal(t, tc.status, w.Code, tc.path)
		if tc.params == nil {
			require.Empty(t, received)
			for _, span := range mt.FinishedSpans() {
				require.Nil(t, span.Tag("_dd.appsec.json"))
			}
		} else {
			require.Equal(t, []map[string]string{tc.params}, received)
		}
	}
}

func TestEndpointHookRepeatedName(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	startAppSecRules(t, rulePathBlock)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	r := chi.NewRouter()
	r.Use(Middleware())
	r.Route("/m/{id}", func(sub chi.Router) {
		sub.Method(http.MethodGet, "/items/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), false))
	})
	for _, tc := range []struct {
		path   string
		status int
	}{{"/m/attack/items/42", 200}, {"/m/42/items/attack", 403}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		require.Equal(t, tc.status, w.Code, tc.path)
	}
}

func TestEndpointHookStacked(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	startAppSecRules(t, rulePathBlock)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	calls := 0
	t.Cleanup(endpointhook.SetRouteMatchedForTest(func(context.Context, string, map[string]string) error { calls++; return nil }))
	r := chi.NewRouter()
	r.Use(Middleware())
	handled := 0
	leaf := endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { handled++; w.WriteHeader(http.StatusOK) }), false)
	r.With(Middleware()).Method(http.MethodGet, "/users/{id}", leaf)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/attack", nil))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Zero(t, calls)
	require.Len(t, mt.FinishedSpans(), 2)
	mt.Reset()
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/42", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, handled)
	require.Len(t, mt.FinishedSpans(), 2)
	require.Zero(t, calls)
}

func TestEndpointHookSubrouters(t *testing.T) {
	for _, mounted := range []bool{false, true} {
		name := "route"
		if mounted {
			name = "mount"
		}
		t.Run(name, func(t *testing.T) {
			t.Cleanup(endpointhook.SetWovenForTest(true))
			startAppSecRules(t, rulePathBlock)
			mt := mocktracer.Start()
			t.Cleanup(mt.Stop)
			var seen []map[string]string
			t.Cleanup(endpointhook.SetRouteMatchedForTest(func(ctx context.Context, route string, params map[string]string) error {
				seen = append(seen, params)
				return httpsec.RouteMatched(ctx, route, params)
			}))
			r := chi.NewRouter()
			r.Use(Middleware())
			var calls atomic.Int32
			leaf := endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }), false)
			if mounted {
				sub := chi.NewRouter()
				sub.Use(Middleware())
				sub.Method(http.MethodGet, "/items/{id}", leaf)
				r.Mount("/routed/{tenant}", sub)
			} else {
				r.Route("/routed/{tenant}", func(sub chi.Router) { sub.Method(http.MethodGet, "/items/{id}", leaf) })
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/routed/acme/items/attack", nil))
			require.Equal(t, http.StatusForbidden, w.Code)
			require.Zero(t, calls.Load())
			require.Equal(t, []map[string]string{{"tenant": "acme", "id": "attack"}}, seen)
			spans := 1
			if mounted {
				spans = 2
			}
			require.Len(t, mt.FinishedSpans(), spans)
		})
	}
}
