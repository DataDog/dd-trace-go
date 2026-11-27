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

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
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
// runs before chi routes the request. The WAF gets the input that chi has when the
// middleware starts, and the route that chi selected is reported after the handler.
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
		// With Use before routing, chi knows no path parameter when the middleware
		// starts: the WAF gets none, and a rule on a path parameter does not match (the
		// result before the route was reported). The route is the route that chi
		// selected.
		{fixtureRootUse(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}"}},
		// chi knows the "*" of the mount when the sub-router middleware starts.
		{fixtureSubRouterUse(),
			clean{[]string{"/api/users/{id}"}, nil, 0},
			attack{route: "/api/users/{id}", earlyRoute: "/api/*", earlyParams: pp("*", "")}},
		// With the middleware on the endpoint, chi knows the parameters: an early block.
		{fixtureWith(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{early: true, route: "/users/{id}", events: pathBlock, spanRoute: "/users/{id}",
				earlyRoute: "/users/{id}", earlyParams: pp("id", "attack-1")}},
		{fixtureStripSlashes(false),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}"}},
		{fixtureStripSlashes(true),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}"}},
		{fixtureGetHead(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}"}},
		{fixtureDirectMount(),
			clean{[]string{"/api/users/{id}"}, nil, 0},
			attack{route: "/api/users/{id}"}},
		{fixtureWrappedMount(),
			clean{[]string{"/api/users/{id}"}, nil, 0},
			attack{route: "/api/users/{id}"}},
		{fixtureRewrite("name"),
			clean{[]string{"/files/{name}"}, nil, 0},
			attack{route: "/files/{name}"}},
		{fixtureTwoTracers(),
			clean{[]string{"/api/users/{id}", "/api/users/{id}"}, nil, 0},
			attack{route: "/api/users/{id}"}},
		// The tracer wraps the mount handler: chi has not reset the "*" of the mount
		// yet, so the WAF gets the "*" that chi knows (a rule on "*" matches, as before
		// the route was reported). The route is the route that chi selected.
		{fixtureWithMount(),
			clean{[]string{"/api/users/{id}"}, []string{"wildcard-value"}, 0},
			attack{route: "/api/users/{id}", events: []string{"wildcard-value"},
				earlyRoute: "/api/*", earlyParams: pp("*", "users/attack-1")}},
		{fixtureLiteralLeaf(),
			clean{[]string{"/api/attack"}, nil, 0},
			attack{route: "/api/attack"}},
		{fixtureReplacedContext(),
			clean{[]string{"/users/{id}"}, nil, 0},
			attack{route: "/users/{id}"}},
		// chi matched "/api/*" (the parent rewrote the route path).
		{fixtureParentRewrite(),
			clean{[]string{"/api/files/{id}"}, nil, 0},
			attack{route: "/api/files/{id}", earlyRoute: "/api/*", earlyParams: pp("*", "")}},
		// chi matched "/api/{id}/*" with "id" (the parent routed the mount with a
		// parameter): the request is blocked as before the route was reported.
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
					assert.Zero(t, count(o.metrics, "waf.requests", "block_failure:true"))
				}
			})
		})
	}
}

// TestAppSecRouteSetupsAnyParam uses a rule on any path parameter. With Use before
// routing, the WAF gets no path parameter from these setups: the request is not
// blocked, the handler runs, and the route is the route that chi selected.
func TestAppSecRouteSetupsAnyParam(t *testing.T) {
	rec := startAppSecRules(t, rulePathAny)
	capture := newOpCapture()
	for _, c := range []struct {
		f     routerFixture
		route string
	}{
		{fixtureStripSlashes(true), "/users/{id}"},
		{fixtureWrappedMount(), "/api/users/{id}"},
		{fixtureRewrite("name"), "/files/{name}"},
		{fixtureOrdinaryMount(), "/files/*"},
	} {
		t.Run(c.f.name, func(t *testing.T) {
			o := serveFixture(t, rec, capture, c.f, "attack-1")
			assert.Equal(t, http.StatusOK, o.status)
			assert.True(t, o.ran)
			assert.Equal(t, []string{c.route}, o.routes)
			assert.Empty(t, o.events)
			assert.Zero(t, count(o.metrics, "waf.requests", "request_blocked:true"))
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
		assert.Empty(t, o.events, "the WAF gets no path parameter with Use before routing")
		assert.Zero(t, count(o.metrics, "api_security.missing_route"))
	})
}

// schemaRouter returns a router with 2 endpoints that have a path parameter, and
// the middleware before routing.
func schemaRouter() http.Handler {
	router := chi.NewRouter()
	router.Use(Middleware())
	router.Get("/schema-a/{id}", okHandler)
	router.Get("/schema-b/{id}", okHandler)
	return router
}

// schemaEndpointRouter returns a router with the middleware on the endpoint.
func schemaEndpointRouter() http.Handler {
	router := chi.NewRouter()
	router.With(Middleware()).Get("/schema-c/{id}", okHandler)
	return router
}

// requestTags serves a request and returns the tags of its span.
func requestTags(t *testing.T, router http.Handler, target string) map[string]any {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0].Tags()
}

// TestAppSecSchemaPerEndpoint checks that each endpoint gets an API Security
// schema in the same delay. The schema has the path parameters only when chi knows
// them when the middleware starts (the middleware is on the endpoint): with Use
// before routing, the WAF gets no path parameters. The schema extraction needs a WAF
// budget that CI load cannot exhaust, so the test sets its own timeout (the default
// is 2 ms). TestAppSecSamplingPerEndpoint checks the sampling decision.
func TestAppSecSchemaPerEndpoint(t *testing.T) {
	t.Setenv("DD_API_SECURITY_SAMPLE_DELAY", "30")
	t.Setenv("DD_API_SECURITY_ENABLED", "true")
	t.Setenv("DD_APPSEC_ENABLED", "true")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	testutils.StartAppSec(t) // the default rules, which load the schema processors

	router := schemaRouter()
	for _, target := range []string{"/schema-a/1", "/schema-b/1"} {
		got := requestTags(t, router, target)
		assert.Contains(t, got, "_dd.appsec.s.req.headers", target)
		assert.NotContains(t, got, "_dd.appsec.s.req.params", "%s: Use before routing gives no path parameters", target)
	}
	got := requestTags(t, schemaEndpointRouter(), "/schema-c/1")
	assert.Contains(t, got, "_dd.appsec.s.req.headers")
	assert.Contains(t, got, "_dd.appsec.s.req.params", "the middleware on the endpoint knows the path parameters")
}

// TestAppSecSamplingPerEndpoint checks that each endpoint has its own API Security
// sampling key: with the route in the key, 2 endpoints are kept in the same delay.
// The third request (same endpoint as the first) is the negative control. With APM
// tracing disabled, the listener keeps a sampled request before the schema
// extraction, so the test reads the sampling priority and does not depend on the
// schema: it passes also when the WAF times out.
func TestAppSecSamplingPerEndpoint(t *testing.T) {
	t.Setenv("DD_API_SECURITY_SAMPLE_DELAY", "30")
	t.Setenv("DD_API_SECURITY_ENABLED", "true")
	t.Setenv("DD_APPSEC_ENABLED", "true")
	t.Setenv("DD_APM_TRACING_ENABLED", "false")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1ns")
	testutils.StartAppSec(t)

	router := schemaRouter()
	const priority = "_sampling_priority_v1"
	assert.EqualValues(t, 2, requestTags(t, router, "/schema-a/1")[priority], "kept")
	assert.EqualValues(t, 2, requestTags(t, router, "/schema-b/1")[priority], "another endpoint has its own sampling key")
	assert.NotEqualValues(t, 2, requestTags(t, router, "/schema-a/2")[priority], "the same endpoint is sampled once in the delay")
}

// TestAppSecHandlerPanic checks that the route report after the handler does not
// run in a panic, and that the panic reaches the recovering middleware unchanged.
// StripSlashes removes the slash after the tracer, so chi selects another route
// than the one that it had when the tracer started.
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

// TestAppSecConcurrentRequests sends concurrent requests through one middleware
// instance: each request must have its own parameters, route and tracker.
func TestAppSecConcurrentRequests(t *testing.T) {
	startAppSecRules(t, rulePathBlock)
	mt := mocktracer.Start()
	defer mt.Stop()

	// The middleware is on the endpoint: chi knows the path parameters when it
	// starts, so a rule on a path parameter blocks before the handler.
	router := chi.NewRouter()
	router.With(Middleware()).Get("/users/{id}/orders/{order}", func(w http.ResponseWriter, r *http.Request) {
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
// rewrite: the tracer of the child router must still block with the "id" that chi
// matched when it started.
func TestAppSecSamePatternRewriteNested(t *testing.T) {
	rec := startAppSecRules(t, rulePathBlock)
	capture := newOpCapture()
	o := serveFixture(t, rec, capture, fixtureSamePatternRewrite(true), "attack-1")
	assert.Equal(t, http.StatusForbidden, o.status)
	assert.False(t, o.ran, "blocked before the handler")
	assert.Equal(t, []string{"path-block"}, o.events)
	assert.Len(t, o.routes, 2, "the tracers of the root and of the child router")
}

// TestAppSecNormalizedRoute checks that, with Use before routing, the normalized
// route has the catch-all of the route that chi selected only when it has a value.
// The path parameters reach API Security after the handler, without the WAF. In a
// mounted router, the catch-all is the last "*".
func TestAppSecNormalizedRoute(t *testing.T) {
	t.Setenv("DD_API_SECURITY_ENABLED", "true")
	t.Setenv("DD_APPSEC_ENABLED", "true")
	testutils.StartAppSec(t)

	router := chi.NewRouter()
	router.Use(Middleware())
	router.Get("/users/{id}", okHandler)
	router.Get("/files/*", okHandler)
	api := chi.NewRouter()
	api.Get("/", okHandler)
	api.Get("/static/*", okHandler)
	api.Get("/users/{id}/*", okHandler)
	api.Mount("/t/{tenant}", http.HandlerFunc(okHandler))
	router.Mount("/api", api)
	router.Mount("/opaque", http.HandlerFunc(okHandler))
	wild := chi.NewRouter()
	wild.Get("/*", okHandler)
	router.Mount("/wild", wild)
	for target, want := range map[string]string{
		"/users/1":         "/users/{id}",
		"/files/a/b.txt":   "/files/{param1}",
		"/files/":          "/files/",
		"/api":             "/api",
		"/api/":            "/api",
		"/api/static/a":    "/api/static/{param1}",
		"/api/static/":     "/api/static/",
		"/api/users/1/":    "/api/users/{id}/",
		"/api/users/1/a":   "/api/users/{id}/{param1}",
		"/api/t/acme/x":    "/api/t/{tenant}/{param1}",
		"/opaque/x":        "/opaque/{param1}",
		"/opaque/opaque/x": "/opaque/{param1}",
		"/wild/":           "/wild/",
		"/wild/a":          "/wild/{param1}",
		// A mounted chi router without a route for the request: the mount.
		"/api/nope": "/api/{param1}",
	} {
		assert.Equal(t, want, requestTags(t, router, target)["_dd.appsec.normalized_route"], target)
	}

	// A handler that is not a chi router mounted at the root.
	root := chi.NewRouter()
	root.Use(Middleware())
	root.Mount("/", http.HandlerFunc(okHandler))
	for target, want := range map[string]string{"/anything": "/{param1}", "/": "/"} {
		assert.Equal(t, want, requestTags(t, root, target)["_dd.appsec.normalized_route"], target)
	}

	// A middleware that rewrites the routing path: chi routes /files/.json as
	// /files/, and gives the opaque mount the rewritten rest.
	rewritten := chi.NewRouter()
	rewritten.Use(Middleware(), middleware.URLFormat)
	rewritten.Get("/files/*", okHandler)
	rewritten.Mount("/opaque", http.HandlerFunc(okHandler))
	rewritten.Mount("/t/{tenant}", http.HandlerFunc(okHandler))
	for target, want := range map[string]string{
		"/files/.json":    "/files/",
		"/files/a.json":   "/files/{param1}",
		"/opaque/x.json":  "/opaque/{param1}",
		"/opaque/opaque/": "/opaque/{param1}",
		"/t/acme/x.json":  "/t/{tenant}/{param1}",
	} {
		assert.Equal(t, want, requestTags(t, rewritten, target)["_dd.appsec.normalized_route"], target)
	}

	// A middleware that rewrites the path that chi routes.
	rewrite := func(from, to string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == from {
					chi.RouteContext(r.Context()).RoutePath = to
				}
				next.ServeHTTP(w, r)
			})
		}
	}
	for _, tc := range []struct{ from, to, want string }{
		{"/assets/readme", "/assets/", "/assets/"},
		{"/assets/", "/assets/readme", "/assets/{param1}"},
		{"/x", "/opaque/rest", "/opaque/{param1}"},
		{"/assets/assets/", "/assets/", "/assets/"},
		{"/y", "/opaque/opaque/", "/opaque/{param1}"},
	} {
		rerouted := chi.NewRouter()
		rerouted.Use(Middleware(), rewrite(tc.from, tc.to))
		rerouted.Get("/assets/*", okHandler)
		rerouted.Mount("/opaque", http.HandlerFunc(okHandler))
		assert.Equal(t, tc.want, requestTags(t, rerouted, tc.from)["_dd.appsec.normalized_route"], tc.from+" -> "+tc.to)
	}

	// A mount in a group with middlewares: its handler is not the one that Mount
	// registers, but RoutePath tells it from a route.
	grouped := chi.NewRouter()
	grouped.Use(Middleware())
	grouped.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler { return next })
		r.Mount("/grouped", http.HandlerFunc(okHandler))
	})
	assert.Equal(t, "/grouped/{param1}", requestTags(t, grouped, "/grouped/x")["_dd.appsec.normalized_route"])

	// An endpoint on the pattern of a mount serves its method instead of the
	// mounted router.
	shadowed := chi.NewRouter()
	shadowed.Use(Middleware())
	shadowed.Mount("/api", chi.NewRouter())
	shadowed.Get("/api/*", okHandler)
	assert.Equal(t, "/api/{param1}", requestTags(t, shadowed, "/api/x")["_dd.appsec.normalized_route"])
}
