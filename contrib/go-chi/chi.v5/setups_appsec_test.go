// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
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

	earlyRoute  string            // the route that the first operation started with
	earlyParams map[string]string // the path parameters that it started with
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
	if len(capture.args) > 0 {
		o.earlyRoute = capture.args[0].RequestRoute
		if len(capture.args[0].PathParams) > 0 {
			o.earlyParams = capture.args[0].PathParams
		}
	}
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
	slices.Sort(o.events)
	o.metrics = rec.since(before)
	return o
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
		earlyRoute  string   // the route that the first operation started with
		earlyParams map[string]string
	}
	pp := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	pathBlock := []string{"path-block"}
	cases := []struct {
		f      routerFixture
		clean  clean
		attack attack
	}{
		{fixtureRootUse(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{early: true, route: "/users/{id}", events: pathBlock, spanRoute: "",
				earlyRoute: "/users/{id}", earlyParams: pp("id", "attack-1")}},
		{fixtureSubRouterUse(),
			clean{[]string{"/api/users/{id}"}, nil, 0},
			attack{early: true, route: "/api/users/{id}", events: pathBlock, spanRoute: "/api/*",
				earlyRoute: "/api/users/{id}", earlyParams: pp("*", "", "id", "attack-1")}},
		{fixtureWith(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{early: true, route: "/users/{id}", events: pathBlock, spanRoute: "/users/{id}",
				earlyRoute: "/users/{id}", earlyParams: pp("id", "attack-1")}},
		{fixtureStripSlashes(false),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}", events: pathBlock, failure: true}},
		{fixtureStripSlashes(true),
			clean{[]string{"/users/{id}"}, []string{"wildcard-value"}, 0},
			attack{route: "/users/{id}", events: []string{"path-block", "wildcard-value"}, failure: true,
				earlyRoute: "/*", earlyParams: pp("*", "users/attack-1/")}},
		{fixtureGetHead(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}", events: pathBlock, failure: true}},
		{fixtureDirectMount(),
			clean{[]string{"/api/users/{id}"}, nil, 0},
			attack{early: true, route: "/api/users/{id}", events: pathBlock, spanRoute: "",
				earlyRoute: "/api/users/{id}", earlyParams: pp("*", "", "id", "attack-1")}},
		{fixtureWrappedMount(),
			clean{[]string{"/api/users/{id}"}, []string{"wildcard-value"}, 0},
			attack{route: "/api/users/{id}", events: []string{"path-block", "wildcard-value"}, failure: true,
				earlyRoute: "/api/*", earlyParams: pp("*", "users/attack-1")}},
		{fixtureRewrite("name"),
			clean{[]string{"/files/{name}"}, []string{"wildcard-value"}, 0},
			attack{route: "/files/{name}", events: []string{"wildcard-value"},
				earlyRoute: "/*", earlyParams: pp("*", "old/attack-1")}},
		{fixtureTwoTracers(),
			clean{[]string{"/api/users/{id}", "/api/users/{id}"}, nil, 0},
			attack{early: true, route: "/api/users/{id}", events: pathBlock, spanRoute: "",
				earlyRoute: "/api/users/{id}", earlyParams: pp("*", "", "id", "attack-1")}},
		// The tracer wraps the mount handler: chi has not reset the "*" of the mount
		// yet. The lookup would change the known "*", so it is dropped: AppSec keeps the
		// input that it had before the lookup existed (a rule on "*" blocks as before),
		// and reports only the final route.
		{fixtureWithMount(),
			clean{[]string{"/api/users/{id}"}, []string{"wildcard-value"}, 0},
			attack{route: "/api/users/{id}", events: []string{"wildcard-value"},
				earlyRoute: "/api/*", earlyParams: pp("*", "users/attack-1")}},
		{fixtureLiteralLeaf(),
			clean{[]string{"/api/attack"}, nil, 0},
			attack{route: "/api/attack", earlyRoute: "/api/attack", earlyParams: pp("*", "")}},
		{fixtureReplacedContext(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}", events: pathBlock, failure: true}},
		// chi matched "/api/*" (the parent rewrote the route path); the lookup of the
		// URL of the request gives "/*", which does not extend it: it is dropped.
		{fixtureParentRewrite(),
			clean{[]string{"/api/files/{id}"}, nil, 0},
			attack{route: "/api/files/{id}", earlyRoute: "/api/*", earlyParams: pp("*", "")}},
		// chi matched "/api/{id}/*" with "id" (the parent routed the mount with a
		// parameter): the lookup does not extend it, so it is dropped and the request is
		// blocked as before the lookup existed.
		{fixtureNamedMountRewrite(),
			clean{[]string{"/api/{id}/items/{item}"}, nil, 0},
			attack{early: true, route: "/api/{id}/*", events: pathBlock, spanRoute: "/api/{id}/*",
				earlyRoute: "/api/{id}/*", earlyParams: pp("*", "", "id", "attack-1")}},
		// The same patterns, with another value for "id": chi matched "attack-1".
		{fixtureSamePatternRewrite(false),
			clean{[]string{"/api/{id}/items/{item}"}, nil, 0},
			attack{early: true, route: "/api/{id}/*", events: pathBlock, spanRoute: "/api/{id}/*",
				earlyRoute: "/api/{id}/*", earlyParams: pp("*", "", "id", "attack-1")}},
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
				assert.Equal(t, a.earlyRoute, o.earlyRoute, "the route that the WAF operation started with")
				assert.Equal(t, a.earlyParams, o.earlyParams, "the path parameters that the WAF got before the handler")
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
		{fixtureOrdinaryMount(), "/files/*"},
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
			assert.Equal(t, []string{""}, o.routes)
			assert.EqualValues(t, 1, count(o.metrics, "api_security.missing_route"))
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

// TestAppSecHandlerPanic checks that the late WAF run does not run in a panic,
// and that the panic reaches the recovering middleware unchanged. The request
// is one that the lookup cannot match (StripSlashes removes the slash after the
// tracer), so a late run would have given the route and the attack to the WAF.
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
	}, Middleware(), middleware.StripSlashes)
	router.Get("/users/{id}", func(http.ResponseWriter, *http.Request) { panic(sentinel) })

	mt := mocktracer.Start()
	defer mt.Stop()
	before := rec.snapshot()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/attack-1/", nil))

	assert.Equal(t, sentinel, recovered)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Empty(t, eventRules(spans[0].Tag("_dd.appsec.json")), "no late run: the WAF never got the parameter")
	assert.Equal(t, []string{""}, capture.routes(), "no late run: the route is the early route")
	assert.Zero(t, count(rec.since(before), "waf.requests", "request_blocked:true"))
	assert.Zero(t, count(rec.since(before), "waf.requests", "block_failure:true"))
	assert.Zero(t, count(rec.since(before), "api_security.missing_route"), "not a 2xx response")
}

// TestAppSecModifyResourceName checks that AppSec reports the same route as the
// http.route tag, also when WithModifyResourceName changes it.
func TestAppSecModifyResourceName(t *testing.T) {
	startAppSecRules(t, rulePathBlock)
	capture := newOpCapture()

	mt := mocktracer.Start()
	defer mt.Stop()
	router := chi.NewRouter()
	router.Use(capture.middleware, Middleware(WithModifyResourceName(func(s string) string {
		return strings.ReplaceAll(s, "{id}", ":id")
	})))
	router.Get("/user/{id}", okHandler)
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/user/123", nil))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "/user/:id", spans[0].Tag(ext.HTTPRoute))
	assert.Equal(t, []string{"/user/:id"}, capture.routes())
}

// TestAppSecSubrouterRoutePatterns checks that AppSec reports the route that the
// span reports, with Datadog and OpenTelemetry semantics. With OpenTelemetry
// semantics a request that stops at a mount has no route.
func TestAppSecSubrouterRoutePatterns(t *testing.T) {
	for _, semantics := range []struct{ name, value string }{{"otel", "true"}, {"datadog", "false"}} {
		t.Run(semantics.name, func(t *testing.T) {
			setChiHTTPConfig(t, semantics.value)
			t.Setenv("DD_TRACE_RESOURCE_RENAMING_ENABLED", "true")
			httptrace.ResetCfg()
			startAppSecRules(t, rulePathBlock)
			capture := newOpCapture()
			mt := mocktracer.Start()
			defer mt.Stop()

			for _, target := range []string{"/api/users/123", "/api/missing", "/custom/missing", "/static/missing", "/"} {
				t.Run(target, func(t *testing.T) {
					mt.Reset()
					capture.reset()
					router := newSubrouterTestRouter(t)
					capture.middleware(router).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))

					spans := mt.FinishedSpans()
					require.Len(t, spans, 1)
					want := ""
					if route, ok := spans[0].Tags()[ext.HTTPRoute]; ok {
						want = route.(string)
					}
					assert.Equal(t, []string{want}, capture.routes())
				})
			}
		})
	}
}

// TestAppSecSameTypeMonitorRules documents the detection loss of the early
// lookup: the WAF does not report a second rule of the same tags.type that has
// no action. When the early lookup gives wrong parameters that match a detection
// rule, the event of the rule that matches the correct parameters is hidden. A
// rule with a block action is not hidden.
func TestAppSecSameTypeMonitorRules(t *testing.T) {
	fixtures := []routerFixture{
		fixtureStripSlashes(true), fixtureWrappedMount(), fixtureRewrite("id"), fixtureWrappedEndpoint(),
	}
	t.Run("same type", func(t *testing.T) {
		rec := startAppSecRules(t, pathRule("mon-wild", "mon-test", "*", ""), pathRule("mon-id", "mon-test", "id", ""))
		capture := newOpCapture()
		for _, f := range fixtures {
			t.Run(f.name, func(t *testing.T) {
				o := serveFixtureWith(t, rec, capture, f, "attack-1", false)
				assert.Equal(t, []string{"mon-wild"}, o.events, "mon-id is hidden by the event on the early data")
			})
		}
	})

	t.Run("different types", func(t *testing.T) {
		rec := startAppSecRules(t, pathRule("mon-wild", "mon-wild-test", "*", ""), pathRule("mon-id", "mon-test", "id", ""))
		capture := newOpCapture()
		for _, f := range fixtures {
			t.Run(f.name, func(t *testing.T) {
				o := serveFixtureWith(t, rec, capture, f, "attack-1", false)
				assert.Equal(t, []string{"mon-id", "mon-wild"}, o.events)
			})
		}
	})

	t.Run("blocking rule of the same type", func(t *testing.T) {
		rec := startAppSecRules(t, pathRule("mon-wild", "mon-test", "*", ""), pathRule("blk-id", "mon-test", "id", "block"))
		capture := newOpCapture()
		for _, f := range fixtures {
			t.Run(f.name, func(t *testing.T) {
				o := serveFixtureWith(t, rec, capture, f, "attack-1", false)
				assert.Equal(t, http.StatusForbidden, o.status, "blk-id still matches")
				assert.Contains(t, o.events, "blk-id")
				assert.Contains(t, o.events, "mon-wild", "the early data matched the detection rule")
				// The block replaces the response after the handler (nothing was
				// sent), except when the endpoint has its own tracer: it gets "id"
				// before its handler.
				assert.Equal(t, !strings.HasPrefix(f.name, "14"), o.ran)
			})
		}
	})
}

// TestAppSecConcurrentRequests sends concurrent requests through one middleware
// instance: each request must have its own parameters, route and tracker.
func TestAppSecConcurrentRequests(t *testing.T) {
	startAppSecRules(t, rulePathBlock)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := chi.NewRouter()
	router.Use(Middleware())
	router.Get("/users/{id}/orders/{order}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(chi.URLParam(r, "id") + "/" + chi.URLParam(r, "order")))
	})

	const workers, requests = 16, 40
	type result struct {
		attack bool
		id     string
		status int
		body   string
	}
	results := make([]result, workers*requests)
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			for i := range requests {
				id := fmt.Sprintf("u%d-%d", worker, i)
				attack := (worker+i)%4 == 0
				if attack {
					id = "attack-" + id
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/"+id+"/orders/7", nil))
				results[worker*requests+i] = result{attack, id, w.Code, w.Body.String()}
			}
		})
	}
	wg.Wait()

	for _, r := range results {
		if r.attack {
			assert.Equal(t, http.StatusForbidden, r.status, r.id)
			assert.Contains(t, r.body, `"errors"`, r.id)
		} else {
			assert.Equal(t, http.StatusOK, r.status, r.id)
			assert.Equal(t, r.id+"/7", r.body)
		}
	}
}

// TestAppSecSamePatternRewriteNested adds a tracer on the root router, before the
// rewrite: its prediction uses the URL of the request ("id" = "clean"), but the
// tracer of the child router must still block with the "id" that chi matched.
func TestAppSecSamePatternRewriteNested(t *testing.T) {
	rec := startAppSecRules(t, rulePathBlock)
	capture := newOpCapture()
	o := serveFixture(t, rec, capture, fixtureSamePatternRewrite(true), "attack-1")
	assert.Equal(t, http.StatusForbidden, o.status)
	assert.False(t, o.ran, "blocked before the handler")
	assert.Equal(t, []string{"path-block"}, o.events)
	assert.Len(t, o.routes, 2, "the tracers of the root and of the child router")
}
