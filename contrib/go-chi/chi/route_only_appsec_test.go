// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi_test

// These tests check that the middleware gives the WAF the input that it gave
// before the route was reported, in every case where a path parameter that the
// middleware would predict or report could change the result of a request. They
// use the public API, a real HTTP server and the real WAF. Every expectation is the
// result of the code before the route was reported ("main"). Each test comment
// gives the result of the code that gave path parameters to the WAF before and
// after the handler ("29d38928"), where it differs.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chitrace "github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

const (
	roPathParams      = "server.request.path_params"
	roResponseHeaders = "server.response.headers.no_cookies"
)

func roCondition(address, key, value string) string {
	return fmt.Sprintf(`{"operator":"exact_match","parameters":{"inputs":[{"address":%q,"key_path":[%q]}],"list":[%q]}}`, address, key, value)
}

func roRule(id, action string, conditions ...string) string {
	actions := `[]`
	if action != "" {
		actions = fmt.Sprintf(`[%q]`, action)
	}
	return fmt.Sprintf(`{"id":%q,"name":%q,"tags":{"type":%q,"category":"attack_attempt"},"conditions":[%s],"on_match":%s}`,
		id, id, id, strings.Join(conditions, ","), actions)
}

// roStart starts AppSec with the rules and the given WAF timeout.
func roStart(t *testing.T, timeout string, rules ...string) *mocktracer.Tracer {
	t.Helper()
	ruleset := `{"version":"2.2","metadata":{"rules_version":"1"},"rules":[` + strings.Join(rules, ",") +
		`],"actions":[{"id":"block","type":"block_request","parameters":{"status_code":403,"type":"auto"}},` +
		`{"id":"redirect","type":"redirect_request","parameters":{"status_code":303,"location":"/blocked"}}]}`
	path := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(path, []byte(ruleset), 0o600))
	t.Setenv("DD_APPSEC_RULES", path)
	t.Setenv("DD_APPSEC_ENABLED", "true")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", timeout)
	testutils.StartTelemetryRecorder(t)
	testutils.StartAppSec(t)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	return &mt
}

// roServe serves one request with a real HTTP server. It does not follow redirects.
func roServe(t *testing.T, h http.Handler, target string) (int, string) {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Get(srv.URL + target)
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	require.NoError(t, err)
	<-done
	t.Logf("RESULT %s status=%d", t.Name(), res.StatusCode)
	return res.StatusCode, string(body)
}

// roEvents returns the ids of the rules that reported an event, with repetitions.
func roEvents(mt *mocktracer.Tracer) []string {
	spans := (*mt).FinishedSpans()
	ids := make([]string, 0, len(spans))
	for _, span := range spans {
		json, _ := span.Tag("_dd.appsec.json").(string)
		ids = append(ids, roRuleIDs(json)...)
	}
	slices.Sort(ids)
	return ids
}

// roRouter returns a router with the middleware before routing and one endpoint.
func roRouter(route string, ran *bool) chi.Router {
	r := chi.NewRouter()
	r.Use(chitrace.Middleware())
	r.Get(route, func(w http.ResponseWriter, _ *http.Request) {
		*ran = true
		w.Header().Set("X-Stage", "ready")
	})
	return r
}

// roSpan is the span of an enclosing integration: it keeps the AppSec tags.
type roSpan struct{ json *string }

func (s roSpan) SetTag(key string, value any) {
	if key == "_dd.appsec.json" {
		*s.json, _ = value.(string)
	}
}

// roRuleIDs returns the ids of the rules in an "_dd.appsec.json" tag.
func roRuleIDs(json string) []string {
	parts := strings.Split(json, `"rule":{"id":"`)[1:]
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		ids = append(ids, part[:strings.Index(part, `"`)])
	}
	slices.Sort(ids)
	return ids
}

// roEnclosing wraps next in the operation of an enclosing integration that
// already gives the path parameters to the WAF (httpsec.BeforeHandle).
func roEnclosing(next http.Handler, params map[string]string, tag *string) http.Handler {
	return httpsec.WrapHandler(next, roSpan{tag}, &httpsec.Config{Framework: "enclosing", Route: "/x/{id}", RouteParams: params})
}

// TestEnclosingIntegrationInput: an enclosing integration gives id=alpha, and chi
// routes /items/beta. The middleware must not replace or add to that input. Result of
// 29d38928: 200 for header-first and routematched (the prediction replaced id=alpha).
func TestEnclosingIntegrationInput(t *testing.T) {
	t.Run("header-first", func(t *testing.T) {
		roStart(t, "1s", roRule("rule", "block", roCondition(roResponseHeaders, "x-stage", "ready"), roCondition(roPathParams, "id", "alpha")))
		var ran bool
		var tag string
		code, _ := roServe(t, roEnclosing(roRouter("/items/{id}", &ran), map[string]string{"id": "alpha"}, &tag), "/items/beta")
		assert.Equal(t, http.StatusForbidden, code)
		assert.True(t, ran, "the handler runs: the rule needs a response header")
		assert.Equal(t, []string{"rule"}, roRuleIDs(tag))
	})
	t.Run("path-first", func(t *testing.T) {
		roStart(t, "1s", roRule("rule", "block", roCondition(roPathParams, "id", "alpha"), roCondition(roResponseHeaders, "x-stage", "ready")))
		var ran bool
		var tag string
		code, _ := roServe(t, roEnclosing(roRouter("/items/{id}", &ran), map[string]string{"id": "alpha"}, &tag), "/items/beta")
		assert.Equal(t, http.StatusForbidden, code)
		assert.True(t, ran)
		assert.Equal(t, []string{"rule"}, roRuleIDs(tag))
	})
	t.Run("routematched", func(t *testing.T) {
		// The enclosing integration gives id=alpha with RouteMatched, as the
		// dd-trace-go ServeMux does.
		roStart(t, "1s", roRule("rule", "block", roCondition(roResponseHeaders, "x-stage", "ready"), roCondition(roPathParams, "id", "alpha")))
		var ran bool
		var tag string
		router := roRouter("/items/{id}", &ran)
		h := httpsec.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.NoError(t, httpsec.RouteMatched(r.Context(), "/x/{id}", map[string]string{"id": "alpha"}))
			router.ServeHTTP(w, r)
		}), roSpan{&tag}, &httpsec.Config{Framework: "enclosing"})
		code, _ := roServe(t, h, "/items/beta")
		assert.Equal(t, http.StatusForbidden, code)
		assert.True(t, ran)
		assert.Equal(t, []string{"rule"}, roRuleIDs(tag))
	})
}

// TestEnclosingIntegrationCombination: an enclosing integration gives id=alpha, chi
// routes op=delete. No single input has both values. Result of 29d38928: 403.
func TestEnclosingIntegrationCombination(t *testing.T) {
	roStart(t, "1s", roRule("rule", "block", roCondition(roPathParams, "id", "alpha"), roCondition(roPathParams, "op", "delete")))
	var ran bool
	var tag string
	code, _ := roServe(t, roEnclosing(roRouter("/items/{id}/{op}", &ran), map[string]string{"id": "alpha"}, &tag), "/items/beta/delete")
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ran)
	assert.Empty(t, roRuleIDs(tag))
}

// TestRewriteAfterTheMiddleware: a middleware rewrites /items/alpha/read into
// /items/beta/delete after this middleware. The rule needs id=alpha and op=delete,
// which no routing stage has. Result of 29d38928: 403.
func TestRewriteAfterTheMiddleware(t *testing.T) {
	mt := roStart(t, "1s", roRule("rule", "block", roCondition(roPathParams, "id", "alpha"), roCondition(roPathParams, "op", "delete")))
	var ran bool
	root := chi.NewRouter()
	root.Use(chitrace.Middleware(), func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chi.RouteContext(r.Context()).RoutePath = "/items/beta/delete"
			next.ServeHTTP(w, r)
		})
	})
	root.Get("/items/{id}/{op}", func(http.ResponseWriter, *http.Request) { ran = true })
	code, _ := roServe(t, root, "/items/alpha/read")
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ran)
	assert.Empty(t, roEvents(mt))
}

// TestParentNameOmitted: an enclosing route has id=alpha, and chi routes another
// path with op=delete. Result of 29d38928: 403.
func TestParentNameOmitted(t *testing.T) {
	roStart(t, "1s", roRule("rule", "block", roCondition(roPathParams, "id", "alpha"), roCondition(roPathParams, "op", "delete")))
	var ran bool
	r := chi.NewRouter()
	r.Use(chitrace.Middleware())
	r.Get("/ops/{op}", func(http.ResponseWriter, *http.Request) { ran = true })
	rewrite := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.URL.Path = "/ops/delete"
		r.ServeHTTP(w, req)
	})
	var tag string
	h := httpsec.WrapHandler(rewrite, roSpan{&tag}, &httpsec.Config{Framework: "parent", Route: "/items/{id}", RouteParams: map[string]string{"id": "alpha"}})
	code, _ := roServe(t, h, "/items/alpha")
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ran)
	assert.Empty(t, roRuleIDs(tag))
}

// TestPendingAction: an action that the handler stored is not replaced by a path
// rule after the handler. Result of 29d38928: 303 for the pending block, 403 for
// the pending redirect.
func TestPendingAction(t *testing.T) {
	for _, tc := range []struct {
		name, bodyAction, pathAction string
		want                         int
	}{
		{"block-before-redirect", "block", "redirect", http.StatusForbidden},
		{"redirect-before-block", "redirect", "block", http.StatusSeeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := roStart(t, "1s",
				roRule("body-rule", tc.bodyAction, roCondition("server.request.body", "key", "attack")),
				roRule("path-rule", tc.pathAction, roCondition(roPathParams, "id", "trigger")))
			var ran bool
			r := chi.NewRouter()
			r.Use(chitrace.Middleware(), middleware.StripSlashes)
			r.Get("/items/{id}", func(_ http.ResponseWriter, req *http.Request) {
				ran = true
				// The WAF stores the action. The wrapper applies it after the handler.
				assert.Error(t, httpsec.MonitorParsedBody(req.Context(), map[string]string{"key": "attack"}))
			})
			code, _ := roServe(t, r, "/items/trigger/")
			assert.Equal(t, tc.want, code)
			assert.True(t, ran)
			assert.Equal(t, []string{"body-rule"}, roEvents(mt))
		})
	}
}

// TestRepeatedReports: 12 requests of one operation go through the middleware, and
// the response rule must still report its event: the middleware gives no path
// parameters, so its path rule never reports. Result of 29d38928: path-monitor
// reports first.
func TestRepeatedReports(t *testing.T) {
	mt := roStart(t, "1s",
		roRule("path-monitor", "", roCondition(roPathParams, "id", "attack")),
		roRule("header-monitor", "", roCondition(roResponseHeaders, "x-stage", "ready")))
	router := chi.NewRouter()
	router.Use(chitrace.Middleware())
	router.Get("/items/{id}/{n}", func(http.ResponseWriter, *http.Request) {})
	ids := []string{}
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		tw, tr, after, handled := httpsec.BeforeHandle(w, req, nil, &httpsec.Config{Framework: "parent"})
		if !handled {
			for n := range 12 {
				child := tr.Clone(tr.Context())
				child.URL.Path = fmt.Sprintf("/items/attack/%d", n)
				router.ServeHTTP(tw, child)
			}
			tw.Header().Set("X-Stage", "ready")
		}
		after()
		op, ok := dyngo.FindOperation[httpsec.HandlerOperation](tr.Context())
		if !assert.True(t, ok, "the operation") {
			return
		}
		for _, event := range op.Events() {
			if m, ok := event.(map[string]any); ok {
				if rule, ok := m["rule"].(map[string]any); ok {
					ids = append(ids, fmt.Sprint(rule["id"]))
				}
			}
		}
	})
	code, _ := roServe(t, h, "/")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{"header-monitor"}, ids)
	_ = mt
}

// TestEventCapBoundary: 9 query events, a path event on the chi parameter and a
// response header event. The middleware gives no path parameter, so the 10-event
// limit of a request still has room for the header event. Result of 29d38928: the
// header event is lost.
func TestEventCapBoundary(t *testing.T) {
	rules := make([]string, 0, 11)
	rules = append(rules,
		roRule("header-monitor", "", roCondition(roResponseHeaders, "x-stage", "ready")),
		roRule("path-monitor", "", roCondition(roPathParams, "id", "attack")))
	query := make([]string, 0, 9)
	for i := range 9 {
		rules = append(rules, roRule(fmt.Sprintf("query-%d", i), "", roCondition("server.request.query", fmt.Sprintf("k%d", i), "attack")))
		query = append(query, fmt.Sprintf("k%d=attack", i))
	}
	mt := roStart(t, "1s", rules...)
	var ran bool
	code, _ := roServe(t, roRouter("/items/{id}", &ran), "/items/attack?"+strings.Join(query, "&"))
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ran)
	events := roEvents(mt)
	assert.Contains(t, events, "header-monitor")
	assert.NotContains(t, events, "path-monitor")
	assert.Len(t, events, 10)
}

// TestFiniteBudget: 5,000 path rules that do not match, and a response header block
// rule. The middleware runs no path rule, so the budget of the request is the budget
// of the response rule. Result of 29d38928: 200 (the budget is spent).
func TestFiniteBudget(t *testing.T) {
	rules := make([]string, 0, 5001)
	rules = append(rules, roRule("response-block", "block", roCondition(roResponseHeaders, "x-stage", "ready")))
	for i := range 5000 {
		rules = append(rules, fmt.Sprintf(`{"id":"path-%d","name":"path-%d","tags":{"type":"path-%d","category":"attack_attempt"},"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":%q,"key_path":["id"]}],"regex":"never%d[0-9]+"}}],"on_match":[]}`, i, i, i, roPathParams, i))
	}
	for _, budget := range []string{"500us", "2ms"} {
		t.Run(budget, func(t *testing.T) {
			mt := roStart(t, budget, rules...)
			var ran bool
			code, _ := roServe(t, roRouter("/items/{id}", &ran), "/items/"+strings.Repeat("a", 4096))
			assert.Equal(t, http.StatusForbidden, code)
			assert.True(t, ran)
			assert.Equal(t, []string{"response-block"}, roEvents(mt))
		})
	}
}

// TestNoEarlyPathBlock: with Use before routing, a rule on a path parameter does not
// block before the handler: the WAF gets no path parameters. This is the behavior
// of the code before the route was reported. Result of 29d38928: 403, and the
// handler does not run. Use r.With(Middleware()) for rules on path parameters.
func TestNoEarlyPathBlock(t *testing.T) {
	mt := roStart(t, "1s", roRule("rule", "block", roCondition(roPathParams, "id", "attack")))
	var ran bool
	r := chi.NewRouter()
	r.Use(chitrace.Middleware())
	r.Get("/items/{id}", func(http.ResponseWriter, *http.Request) { ran = true })
	code, _ := roServe(t, r, "/items/attack")
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ran, "the handler runs")
	assert.Empty(t, roEvents(mt))
}

// TestEndpointMiddlewarePathBlock: with r.With(Middleware()), chi knows the path
// parameters when the middleware starts, so a rule on a path parameter blocks before
// the handler (same result as 29d38928 and main).
func TestEndpointMiddlewarePathBlock(t *testing.T) {
	mt := roStart(t, "1s", roRule("rule", "block", roCondition(roPathParams, "id", "attack")))
	var ran bool
	r := chi.NewRouter()
	r.With(chitrace.Middleware()).Get("/items/{id}", func(http.ResponseWriter, *http.Request) { ran = true })
	code, _ := roServe(t, r, "/items/attack")
	assert.Equal(t, http.StatusForbidden, code)
	assert.False(t, ran)
	assert.Equal(t, []string{"rule"}, roEvents(mt))
}

// TestNestedDetection: nested middlewares, and a detection rule on the mount
// parameter. The event is reported once.
func TestNestedDetection(t *testing.T) {
	mt := roStart(t, "1s", roRule("monitor", "", roCondition(roPathParams, "id", "attack")))
	root, child := chi.NewRouter(), chi.NewRouter()
	root.Use(chitrace.Middleware())
	child.Use(chitrace.Middleware())
	var ran bool
	child.Get("/items/{item}", func(http.ResponseWriter, *http.Request) { ran = true })
	root.Mount("/tenant/{id}", child)
	code, _ := roServe(t, root, "/tenant/attack/items/x")
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ran)
	assert.Equal(t, []string{"monitor"}, roEvents(mt))
}
