// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

// outcome is what one request produced.
type outcome struct {
	status    int
	body      string
	ran       bool     // the endpoint handler ran
	routes    []string // op.Route() of each AppSec operation
	spanRoute any      // http.route of the outermost span
	spanCode  any      // http.status_code of the outermost span
	events    []string // rule ids in the _dd.appsec.json tags of all spans
	metrics   map[string]float64
}

// serveFixture sends a request through the router of f. value replaces ATTACK.
func serveFixture(t *testing.T, rec *metricsRecorder, capture *opCapture, f routerFixture, value string) outcome {
	t.Helper()
	return serveFixtureWith(t, rec, capture, f, value, true)
}

// serveFixtureWith is serveFixture. When writes is false, the endpoint handler
// writes nothing.
func serveFixtureWith(t *testing.T, rec *metricsRecorder, capture *opCapture, f routerFixture, value string, writes bool) outcome {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()
	capture.reset()
	before := rec.snapshot()

	var o outcome
	body := "clean-body"
	if strings.HasPrefix(value, "attack") {
		body = "application-body"
	}
	router := f.build(capture.middleware, func(w http.ResponseWriter, _ *http.Request) {
		o.ran = true
		if writes {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		}
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(f.method, strings.Replace(f.target, "ATTACK", value, 1), nil))

	o.status, o.body = w.Code, w.Body.String()
	o.routes = capture.routes()
	spans := mt.FinishedSpans()
	require.NotEmpty(t, spans)
	outer := spans[len(spans)-1]
	o.spanRoute, o.spanCode = outer.Tag("http.route"), outer.Tag("http.status_code")
	seen := map[string]bool{}
	for _, s := range spans {
		for _, id := range eventRules(s.Tag("_dd.appsec.json")) {
			seen[id] = true
		}
	}
	for id := range seen {
		o.events = append(o.events, id)
	}
	sortStrings(o.events)
	o.metrics = rec.since(before)
	return o
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestAppSecRouteSetups checks the AppSec route, the path parameters that the WAF
// gets and the blocking behavior, for the router setups where the middleware
// runs before chi routes the request.
func TestAppSecRouteSetups(t *testing.T) {
	rec := startAppSecRules(t, rulePathBlock, ruleWildcardValue)
	capture := newOpCapture()

	type clean struct {
		routes  []string // op.Route() of each operation
		events  []string
		missing float64
	}
	type attack struct {
		early       bool     // blocked before the handler
		route       string   // op.Route()
		events      []string // rule ids
		failure     bool     // the late block could not replace the response
		spanRoute   string   // http.route of the outer span, when the request is blocked early
		skipEvents  bool
		wantsStatus int
	}
	cases := []struct {
		f      routerFixture
		clean  clean
		attack attack
	}{
		{fixtureRootUse(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{early: true, route: "/users/{id}", events: []string{"path-block"}, spanRoute: ""}},
		{fixtureSubRouterUse(),
			clean{[]string{"/api/users/{id}"}, nil, 0},
			attack{early: true, route: "/api/users/{id}", events: []string{"path-block"}, spanRoute: "/api/*"}},
		{fixtureWith(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{early: true, route: "/users/{id}", events: []string{"path-block"}, spanRoute: "/users/{id}"}},
		{fixtureStripSlashes(false),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}", events: []string{"path-block"}, failure: true}},
		{fixtureStripSlashes(true),
			clean{[]string{"/users/{id}"}, []string{"wildcard-value"}, 0},
			attack{route: "/users/{id}", events: []string{"path-block", "wildcard-value"}, failure: true}},
		{fixtureGetHead(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}", events: []string{"path-block"}, failure: true}},
		{fixtureDirectMount(),
			clean{[]string{"/api/users/{id}"}, nil, 0},
			attack{early: true, route: "/api/users/{id}", events: []string{"path-block"}, spanRoute: ""}},
		{fixtureWrappedMount(),
			clean{[]string{"/api/users/{id}"}, []string{"wildcard-value"}, 0},
			attack{route: "/api/users/{id}", events: []string{"path-block", "wildcard-value"}, failure: true}},
		{fixtureRewrite("name"),
			clean{[]string{"/files/{name}"}, []string{"wildcard-value"}, 0},
			attack{route: "/files/{name}", events: []string{"wildcard-value"}}},
		{fixtureTwoTracers(),
			clean{[]string{"/api/users/{id}", "/api/users/{id}"}, nil, 0},
			attack{early: true, route: "/api/users/{id}", events: []string{"path-block"}, spanRoute: ""}},
		{fixtureWithMount(),
			clean{[]string{"/api/users/{id}"}, nil, 0},
			attack{early: true, route: "/api/users/{id}", events: []string{"path-block"}, spanRoute: "/api/*"}},
		{fixtureLiteralLeaf(),
			clean{[]string{"/api/attack"}, nil, 0},
			attack{route: "/api/attack"}},
		{fixtureReplacedContext(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}", events: []string{"path-block"}, failure: true}},
		{fixtureParentRewrite(),
			clean{[]string{"/api/files/{id}"}, []string{"wildcard-value"}, 0},
			attack{route: "/api/files/{id}", events: []string{"wildcard-value"}}},
	}

	for _, c := range cases {
		t.Run(c.f.name, func(t *testing.T) {
			t.Run("clean", func(t *testing.T) {
				o := serveFixture(t, rec, capture, c.f, "ok-1")
				assert.Equal(t, http.StatusOK, o.status)
				assert.Equal(t, c.clean.routes, o.routes)
				assert.Equal(t, c.clean.events, o.events)
				assert.Equal(t, c.clean.missing, count(o.metrics, "api_security.missing_route"))
				assert.Equal(t, "200", o.spanCode)
				assert.True(t, o.ran)
				// For a dispatched request, AppSec and the span report the same route.
				assert.Equal(t, o.spanRoute, o.routes[len(o.routes)-1])
				assert.Zero(t, count(o.metrics, "waf.requests", "request_blocked:true"))
				assert.Zero(t, count(o.metrics, "waf.requests", "block_failure:true"))
			})
			t.Run("attack", func(t *testing.T) {
				o := serveFixture(t, rec, capture, c.f, "attack-1")
				a := c.attack
				assert.Equal(t, []string{a.route}, o.routes[len(o.routes)-1:])
				assert.Equal(t, a.events, o.events)
				switch {
				case a.early:
					assert.Equal(t, http.StatusForbidden, o.status)
					assert.Contains(t, o.body, `"errors"`)
					assert.False(t, o.ran, "the handler must not run")
					assert.Len(t, o.routes, 1, "an inner tracer must not run after an early block")
					assert.EqualValues(t, 1, count(o.metrics, "waf.requests", "request_blocked:true"))
					assert.Zero(t, count(o.metrics, "waf.requests", "block_failure:true"))
					// The span route of a request that was blocked early is the route
					// that chi had selected at that point.
					assert.Equal(t, a.spanRoute, o.spanRoute)
				default:
					assert.Equal(t, http.StatusOK, o.status)
					assert.True(t, o.ran)
					if c.f.method != "HEAD" {
						assert.Equal(t, "application-body", o.body)
					}
					assert.Zero(t, count(o.metrics, "waf.requests", "request_blocked:true"))
					want := 0.0
					if a.failure {
						want = 1
					}
					assert.Equal(t, want, count(o.metrics, "waf.requests", "block_failure:true"))
				}
			})
		})
	}
}

// TestAppSecRouteSetupsAnyParam uses a rule on any path parameter: when the
// early lookup gives the wrong parameters, the rule blocks on them.
func TestAppSecRouteSetupsAnyParam(t *testing.T) {
	rec := startAppSecRules(t, rulePathAny)
	capture := newOpCapture()
	for _, c := range []struct {
		f     routerFixture
		route string
	}{
		{fixtureStripSlashes(true), "/*"},
		{fixtureWrappedMount(), "/api/*"},
		{fixtureRewrite("name"), "/*"},
	} {
		t.Run(c.f.name, func(t *testing.T) {
			o := serveFixture(t, rec, capture, c.f, "attack-1")
			assert.Equal(t, http.StatusForbidden, o.status)
			assert.False(t, o.ran)
			assert.Equal(t, []string{c.route}, o.routes)
			assert.Equal(t, []string{"path-any"}, o.events)
		})
	}
}

// TestAppSecMissingRoute covers the 2xx responses that have no chi route.
func TestAppSecMissingRoute(t *testing.T) {
	rec := startAppSecRules(t, ruleWildcardValue)
	capture := newOpCapture()

	notFound := func(flush bool) routerFixture {
		return routerFixture{"C", "GET", "/missing", func(first func(http.Handler) http.Handler, _ http.HandlerFunc) http.Handler {
			r := chi.NewRouter()
			r.Use(first, Middleware())
			r.Get("/registered", okHandler)
			r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
				if flush {
					// The status is 200 for the client, but chi reports 0.
					w.(http.Flusher).Flush()
					return
				}
				w.WriteHeader(http.StatusOK)
			})
			return r
		}}
	}
	for name, flush := range map[string]bool{"C": false, "C-flush": true} {
		t.Run(name, func(t *testing.T) {
			o := serveFixture(t, rec, capture, notFound(flush), "ok-1")
			// chi v1.5.5 builds the custom NotFound handler with the middlewares of the
			// router, so the tracer runs twice for such a request (v5 does not).
			assert.Equal(t, []string{"", ""}, o.routes)
			assert.EqualValues(t, 2, count(o.metrics, "api_security.missing_route"))
		})
	}

	t.Run("P", func(t *testing.T) {
		f := routerFixture{"P", "GET", "/files/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
			r := chi.NewRouter()
			r.Use(first, Middleware())
			r.Get("/files/*", h)
			return r
		}}
		o := serveFixture(t, rec, capture, f, "a")
		assert.Equal(t, []string{"/files/*"}, o.routes)
		assert.Equal(t, "/files/*", o.spanRoute)
		assert.Equal(t, []string{"wildcard-value"}, o.events)
		assert.Zero(t, count(o.metrics, "api_security.missing_route"))
	})
}

// TestAppSecSchemaPerEndpoint checks that each endpoint has its own API Security
// sampling key: with the route in the key, 2 endpoints get a schema in the same
// delay. The third request (same endpoint as the first) is the negative control.
func TestAppSecSchemaPerEndpoint(t *testing.T) {
	t.Setenv("DD_API_SECURITY_SAMPLE_DELAY", "30")
	t.Setenv("DD_API_SECURITY_ENABLED", "true")
	t.Setenv("DD_APPSEC_ENABLED", "true")
	testutils.StartAppSec(t) // the default rules, which load the schema processors

	router := chi.NewRouter()
	router.Use(Middleware())
	router.Get("/schema-a/{id}", okHandler)
	router.Get("/schema-b/{id}", okHandler)

	tags := func(target string) map[string]any {
		mt := mocktracer.Start()
		defer mt.Stop()
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		return spans[0].Tags()
	}

	first := tags("/schema-a/1")
	assert.Contains(t, first, "_dd.appsec.s.req.headers")
	assert.Contains(t, first, "_dd.appsec.s.req.params", "the schema has the path parameters")
	second := tags("/schema-b/1")
	assert.Contains(t, second, "_dd.appsec.s.req.headers", "another endpoint has its own sampling key")
	assert.Contains(t, second, "_dd.appsec.s.req.params")
	third := tags("/schema-a/2")
	assert.NotContains(t, third, "_dd.appsec.s.req.headers", "the same endpoint is sampled once in the delay")
}

// TestAppSecHandlerPanic checks that the late WAF run does not run in a panic
// and that the panic reaches the recovering middleware unchanged.
func TestAppSecHandlerPanic(t *testing.T) {
	rec := startAppSecRules(t, rulePathBlock)
	capture := newOpCapture()

	sentinel := "sentinel panic"
	var recovered any
	router := chi.NewRouter()
	router.Use(capture.middleware, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered = recover(); recovered != nil {
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}, Middleware())
	router.Get("/users/{id}", func(http.ResponseWriter, *http.Request) { panic(sentinel) })

	mt := mocktracer.Start()
	defer mt.Stop()
	before := rec.snapshot()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/ok-1", nil))

	assert.Equal(t, sentinel, recovered)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Len(t, mt.FinishedSpans(), 1)
	assert.Zero(t, count(rec.since(before), "api_security.missing_route"), "not a 2xx response")
}
