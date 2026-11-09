// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

// TestExistingPathBlocks checks requests that AppSec blocked before the route
// lookup existed, with a rule on a path parameter. They need no rewrite, hidden
// writer or flush: the parameters are known to chi when the tracer starts. They
// must still be blocked before the handler. These tests use only the public API
// and pass on the code before the lookup.
func TestExistingPathBlocks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    string // the parameter that the rule checks
		target string
		// wantBlocked is false when the code before the lookup did not block either.
		wantBlocked bool
		build       func(h http.HandlerFunc) http.Handler
	}{
		{
			// chi knows "id" (the mount); the child endpoint has another "id" later.
			name: "known-mount-id", key: "id", target: "/tenant/attack/items/clean", wantBlocked: true,
			build: func(h http.HandlerFunc) http.Handler {
				root, child := chi.NewRouter(), chi.NewRouter()
				child.Use(Middleware())
				child.Get("/items/{id}", h)
				root.Mount("/tenant/{id}", child)
				return root
			},
		},
		{
			// The route is final (no lookup), the name is used twice.
			name: "endpoint-duplicate-id", key: "id", target: "/tenant/attack/items/clean", wantBlocked: existingDuplicateBlock,
			build: func(h http.HandlerFunc) http.Handler {
				root, child := chi.NewRouter(), chi.NewRouter()
				child.With(Middleware()).Get("/items/{id}", h)
				root.Mount("/tenant/{id}", child)
				return root
			},
		},
		{
			// The tracer wraps the mount handler: chi has not reset the "*" yet.
			name: "with-mount-wildcard", key: "*", target: "/api/attack", wantBlocked: true,
			build: func(h http.HandlerFunc) http.Handler {
				root, child := chi.NewRouter(), chi.NewRouter()
				child.Get("/attack", h)
				root.With(Middleware()).Mount("/api", child)
				return root
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			startAppSecRules(t, pathRule("existing-path-block", "path-test", tc.key, "block"))
			ran := false
			router := tc.build(func(w http.ResponseWriter, _ *http.Request) {
				ran = true
				w.WriteHeader(http.StatusOK)
			})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.target, nil))
			if tc.wantBlocked {
				assert.Equal(t, http.StatusForbidden, w.Code)
				assert.False(t, ran, "blocked before the handler")
			} else {
				assert.Equal(t, http.StatusOK, w.Code)
				assert.True(t, ran)
			}
		})
	}
}

// existingDuplicateBlock: chi read the last value of a name that is used at
// several levels before the lookup existed, so it did not block here.
const existingDuplicateBlock = false

// TestNoCombinedPathParams checks that the early check never mixes the values of
// the lookup with the values that chi matched: the rule needs id = attack (the
// mount) AND op = danger (the endpoint), but the endpoint has its own "id" = clean.
// No request has this combination, so it must not be blocked.
func TestNoCombinedPathParams(t *testing.T) {
	startAppSecRules(t, `{"id":"combined","name":"combined","tags":{"type":"combined-test","category":"attack_attempt"},
		"conditions":[
			{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["id"]}],"regex":"^attack$"}},
			{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["op"]}],"regex":"^danger$"}}],
		"on_match":["block"]}`)
	root, child := chi.NewRouter(), chi.NewRouter()
	ran := false
	child.Use(Middleware())
	child.Get("/users/{id}/{op}", func(w http.ResponseWriter, _ *http.Request) {
		ran = true
		w.WriteHeader(http.StatusOK)
	})
	root.Mount("/tenants/{id}", child)

	mt := mocktracer.Start()
	defer mt.Stop()
	w := httptest.NewRecorder()
	root.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tenants/attack/users/clean/danger", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, ran)
	spans := mt.FinishedSpans()
	require.NotEmpty(t, spans)
	for _, span := range spans {
		assert.Nil(t, span.Tag("_dd.appsec.json"), "no event: the rule never matched")
	}
}

// pathCondition and headerCondition build conditions of a compatibility rule.
func pathCondition(key, value string) string {
	return fmt.Sprintf(`{"operator":"exact_match","parameters":{"inputs":[{"address":"server.request.path_params","key_path":[%q]}],"list":[%q]}}`, key, value)
}

func headerCondition(name, value string) string {
	return fmt.Sprintf(`{"operator":"exact_match","parameters":{"inputs":[{"address":"server.response.headers.no_cookies","key_path":[%q]}],"list":[%q]}}`, name, value)
}

// TestResponsePhaseCompatibility checks the result of rules whose conditions are
// on a path parameter AND on the response, or on 2 path parameters, in setups
// where chi knows a parameter before the handler and the final value is another
// one. Every expectation is the result of the code before the lookup existed
// (the test uses only the public API and passes there). The handlers write
// nothing unless stated: a rule on the response header is evaluated after the
// handler. The WAF keeps the match of a condition between its runs, so the
// parameters that AppSec gives at each step must not mix the values of the
// routing stages.
func TestResponsePhaseCompatibility(t *testing.T) {
	const legacy = true // chi v1 read the last value of a name before the lookup
	alpha, beta := pathCondition("id", "alpha"), pathCondition("id", "beta")
	stage := headerCondition("x-stage", "ready")

	for _, tc := range []struct {
		name       string
		conditions []string
		target     string
		build      func(h http.HandlerFunc) http.Handler
		writes     bool // the handler sends the status 200
		wantBlock  bool // 403 before the lookup existed
	}{
		{
			// chi matched id=alpha; the endpoint has id=beta. The response header is the
			// first condition, so the path condition is evaluated in the response phase.
			name: "response-first-mount", conditions: []string{stage, alpha}, wantBlock: true,
			target: "/tenant/alpha/items/beta", build: mountWithTracerOnChild("/items/{id}"),
		},
		{
			name: "response-first-endpoint", conditions: []string{stage, alpha}, wantBlock: !legacy,
			target: "/tenant/alpha/items/beta", build: mountWithTracerOnEndpoint("/items/{id}"),
		},
		{
			name: "path-first-mount", conditions: []string{alpha, stage}, wantBlock: true,
			target: "/tenant/alpha/items/beta", build: mountWithTracerOnChild("/items/{id}"),
		},
		{
			// No request has id = alpha AND id = beta.
			name: "two-values-mount", conditions: []string{alpha, beta},
			target: "/tenant/alpha/items/beta", build: mountWithTracerOnChild("/items/{id}"),
		},
		{
			name: "two-values-endpoint", conditions: []string{alpha, beta},
			target: "/tenant/alpha/items/beta", build: mountWithTracerOnEndpoint("/items/{id}"),
		},
		{
			// chi never has id = alpha and op = gamma: id is beta when op is known.
			name: "dropped-prediction-combination", conditions: []string{alpha, pathCondition("op", "gamma")},
			target: "/tenant/alpha/users/beta/gamma", build: mountWithTracerOnChild("/users/{id}/{op}"),
		},
		{
			name: "written-combination", conditions: []string{alpha, pathCondition("op", "gamma")}, writes: true,
			target: "/tenant/alpha/users/beta/gamma", build: mountWithTracerOnChild("/users/{id}/{op}"),
		},
		{
			// With the tracer on the mount, chi knows "*" = "items/beta" and the endpoint
			// gets "*" = "" with id = beta: no request has "*" = "items/beta" and id = beta.
			name: "wildcard-combination", conditions: []string{pathCondition("*", "items/beta"), beta},
			target: "/api/items/beta", build: func(h http.HandlerFunc) http.Handler {
				root, child := chi.NewRouter(), chi.NewRouter()
				child.Get("/items/{id}", h)
				root.With(Middleware()).Mount("/api", child)
				return root
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := fmt.Sprintf(`{"id":"compatibility","name":"compatibility","tags":{"type":"compat-test","category":"attack_attempt"},
				"conditions":[%s],"on_match":["block"]}`, strings.Join(tc.conditions, ","))
			startAppSecRules(t, rule)
			mt := mocktracer.Start()
			defer mt.Stop()
			router := tc.build(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Stage", "ready")
				if tc.writes {
					w.WriteHeader(http.StatusOK)
				}
			})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.target, nil))

			spans := mt.FinishedSpans()
			require.NotEmpty(t, spans)
			events := false
			for _, span := range spans {
				events = events || span.Tag("_dd.appsec.json") != nil
			}
			if tc.wantBlock {
				assert.Equal(t, http.StatusForbidden, w.Code)
				assert.True(t, events, "the rule matched")
			} else {
				assert.Equal(t, http.StatusOK, w.Code)
				assert.False(t, events, "the rule must not match a mix of the values of 2 routing stages")
			}
		})
	}
}

// mountWithTracerOnChild: mount /tenant/{id}, the tracer in the child router.
func mountWithTracerOnChild(route string) func(h http.HandlerFunc) http.Handler {
	return func(h http.HandlerFunc) http.Handler {
		root, child := chi.NewRouter(), chi.NewRouter()
		child.Use(Middleware())
		child.Get(route, h)
		root.Mount("/tenant/{id}", child)
		return root
	}
}

// mountWithTracerOnEndpoint: mount /tenant/{id}, the tracer only on the endpoint.
func mountWithTracerOnEndpoint(route string) func(h http.HandlerFunc) http.Handler {
	return func(h http.HandlerFunc) http.Handler {
		root, child := chi.NewRouter(), chi.NewRouter()
		child.With(Middleware()).Get(route, h)
		root.Mount("/tenant/{id}", child)
		return root
	}
}

// nestedRouter has a tracer on the root router and on a child router that is
// mounted with a parameter. The root has a middleware that sets the response
// header after the child returned (or before it, when headerBefore is true), and
// StripSlashes: with a trailing slash the early lookups do not match.
func nestedRouter(endpoint string, headerBefore bool, h http.HandlerFunc) http.Handler {
	root, child := chi.NewRouter(), chi.NewRouter()
	root.Use(Middleware(), func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if headerBefore {
				w.Header().Set("X-Stage", "ready")
			}
			next.ServeHTTP(w, r)
			w.Header().Set("X-Stage", "ready") // the outer response-phase check sees it
		})
	}, middleware.StripSlashes)
	child.Use(Middleware())
	child.Get(endpoint, h)
	root.Mount("/tenant/{id}", child)
	return root
}

// TestNestedResponsePreservation checks 2 tracers of one request, where the outer
// one evaluates a response rule after the inner one returned. The tracers share
// the baseline input of the WAF: the outer tracer must not replace the value that
// the inner tracer kept ("alpha" instead of the final "beta"). Every expectation is
// the result of the code before the lookup existed.
func TestNestedResponsePreservation(t *testing.T) {
	alpha := pathCondition("id", "alpha")
	stage := headerCondition("x-stage", "ready")
	for _, tc := range []struct {
		name         string
		conditions   []string
		headerBefore bool
	}{
		{"outer-header-after-child", []string{stage, alpha}, false},
		{"header-before-child-control", []string{stage, alpha}, true},
		{"path-first-control", []string{alpha, stage}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := fmt.Sprintf(`{"id":"nested","name":"nested","tags":{"type":"nested-test","category":"attack_attempt"},
				"conditions":[%s],"on_match":["block"]}`, strings.Join(tc.conditions, ","))
			startAppSecRules(t, rule)
			mt := mocktracer.Start()
			defer mt.Stop()
			router := nestedRouter("/items/{id}", tc.headerBefore, func(http.ResponseWriter, *http.Request) {})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tenant/alpha/items/beta/", nil))
			assert.Equal(t, http.StatusForbidden, w.Code)
		})
	}
}

// TestNestedNewParams checks that nested tracers still give a new name to the WAF
// when it does not contradict the shared input: the endpoint has "item" (a name
// that no tracer knew when it started), and the response rule needs it.
func TestNestedNewParams(t *testing.T) {
	rule := fmt.Sprintf(`{"id":"nested-new","name":"nested-new","tags":{"type":"nested-new-test","category":"attack_attempt"},
		"conditions":[%s],"on_match":["block"]}`, headerCondition("x-stage", "ready")+","+pathCondition("item", "beta"))
	startAppSecRules(t, rule)
	router := nestedRouter("/items/{item}", false, func(http.ResponseWriter, *http.Request) {})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tenant/alpha/items/beta/", nil))
	assert.Equal(t, http.StatusForbidden, w.Code, "the new name reached the WAF")
}
