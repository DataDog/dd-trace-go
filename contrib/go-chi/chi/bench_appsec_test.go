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
// The request has the route lookup, the commit tracker, the context value and,
// when chi selects another route, the late run. Path parameters are new WAF
// input: "no-params" shows the cost without them. A chain of 16 writers is too
// long for the tracker (it is not complete, and does not run the lookup).
func BenchmarkMiddlewareAppSec(b *testing.B) {
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

	router := chi.NewRouter()
	router.Use(Middleware())
	router.Get("/users/{id}/orders/{order}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
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
	} {
		depth := c.depth
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			req := httptest.NewRequest(http.MethodGet, c.target, nil)
			for i := 0; i < b.N; i++ {
				var w http.ResponseWriter = httptest.NewRecorder()
				for range depth {
					w = benchUnwrapper{w}
				}
				router.ServeHTTP(w, req)
				if i%1000 == 0 {
					mt.Reset()
				}
			}
		})
	}
}
