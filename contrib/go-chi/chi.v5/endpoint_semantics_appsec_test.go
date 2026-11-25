// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2/internal/endpointhook"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func endpointRule(id, address, key, value, action string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"tags":{"type":%q,"category":"attack_attempt"},"conditions":[{"operator":"exact_match","parameters":{"inputs":[{"address":%q,"key_path":[%q]}],"list":[%q]}}],"on_match":%s}`, id, id, id, address, key, value, action)
}

func TestEndpointHookPendingAction(t *testing.T) {
	for _, response := range []bool{false, true} {
		t.Run(strconv.FormatBool(response), func(t *testing.T) {
			t.Cleanup(endpointhook.SetWovenForTest(true))
			redirect := rulePathRedirect
			if response {
				redirect = endpointRule("response-redirect", "server.response.headers.no_cookies", "x-response-stage", "ready", `["redirect"]`)
			}
			startAppSecRules(t, endpointRule("body-block", "server.request.body", "key", "attack", `["block"]`), redirect)
			mt := mocktracer.Start()
			t.Cleanup(mt.Stop)
			r := chi.NewRouter()
			r.Use(Middleware())
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					require.Error(t, httpsec.MonitorParsedBody(req.Context(), map[string]string{"key": "attack"}))
					next.ServeHTTP(w, req)
				})
			})
			r.Method("GET", "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Response-Stage", "ready") }), false))
			target := "/users/attack"
			if response {
				target = "/users/42"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
			require.Equal(t, http.StatusSeeOther, w.Code)
		})
	}
}

func TestEndpointHookEventCap(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	rules := make([]string, 0, 11)
	rules = append(rules, endpointRule("path-monitor", "server.request.path_params", "id", "attack", `[]`), endpointRule("header-monitor", "server.response.headers.no_cookies", "x-response-stage", "ready", `[]`))
	query := make([]string, 0, 9)
	for i := range 9 {
		rules = append(rules, endpointRule(fmt.Sprintf("query-%d", i), "server.request.query", fmt.Sprintf("k%d", i), "attack", `[]`))
		query = append(query, fmt.Sprintf("k%d=attack", i))
	}
	startAppSecRules(t, rules...)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	r := chi.NewRouter()
	r.Use(Middleware())
	r.Method("GET", "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Response-Stage", "ready") }), false))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/users/attack?"+strings.Join(query, "&"), nil))
	require.Equal(t, 200, w.Code)
	require.Len(t, mt.FinishedSpans(), 1)
	events, ok := mt.FinishedSpans()[0].Tag("_dd.appsec.json").(string)
	require.True(t, ok)
	for i := range 9 {
		require.Contains(t, events, fmt.Sprintf(`"id":"query-%d"`, i))
	}
	require.Contains(t, events, `"id":"path-monitor"`)
	require.NotContains(t, events, `"id":"header-monitor"`)
}

func TestEndpointHookFiniteBudget(t *testing.T) {
	t.Cleanup(endpointhook.SetWovenForTest(true))
	rules := make([]string, 0, 5001)
	rules = append(rules, ruleHeaderBlock)
	for i := range 5000 {
		rules = append(rules, fmt.Sprintf(`{"id":"path-%d","name":"path-%d","tags":{"type":"path-%d","category":"attack_attempt"},"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["id"]}],"regex":"never%d[0-9]+"}}],"on_match":[]}`, i, i, i, i))
	}
	metrics := startEndpointAppSecRules(t, "500us", rules...)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	before := metrics.snapshot()
	r := chi.NewRouter()
	r.Use(Middleware())
	r.Method("GET", "/users/{id}", endpointhook.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Response-Stage", "ready") }), false))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/users/"+strings.Repeat("a", 4096), nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, float64(1), count(metrics.since(before), "waf.requests", "waf_timeout:true"))
}
