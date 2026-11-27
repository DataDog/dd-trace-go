// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// withAppsec returns the handler that runs the AppSec operation of a request.
// The handler expects the chi response writer that [Middleware] creates.
//
// With Use before routing, chi has not selected the route when this middleware
// starts. The WAF gets the route and the path parameters that chi knows at that
// time, as before the route was reported. They can block: an endpoint middleware
// (r.With(Middleware())) and a Use in a sub-router know the parameters of the route
// or of the parent mount, and a Use at the root knows none. The middleware adds no
// newly discovered path parameters, so a rule on path parameters does not match
// with a Use at the root.
//
// After the handler, it reports the route that chi selected when it is not the start
// route (RouteMatched without parameters): API Security uses the route as the
// sampling key, and api_security.missing_route counts the requests without it. The
// report sends only the route and causes no additional native WAF evaluation. The
// route as chi's patterns declare it and the path parameters of the selected route
// always go to API Security only (ReportMatchedRoute), for the normalized route; the
// WAF does not get them. Unlike the reported route, that route keeps a declared
// trailing "/" and is not renamed by WithModifyResourceName. The parameters can
// differ from what AppSec read before the handler even when the route is the same:
// with r.With(Middleware()) in a mounted router, the first "*" is the mount's. The
// route that chi knows at the start is reported too, as an early block finishes the
// operation without running the handler. A late block replaces the response only if
// it is not sent yet (see "Late blocks and route reports in HTTP contribs" in
// internal/appsec/README.md).
//
// The response writer is wrapped in a commitTracker (see writer_appsec.go) to know
// if the response is sent.
func withAppsec(next http.Handler, span trace.TagSetter, cfg *config, cache, mounts *sync.Map) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A copy for each request: httpsec.BeforeHandle writes in the config.
		appsecCfg := cfg.appsecConfig

		tw := w
		if ww, ok := w.(middleware.WrapResponseWriter); ok {
			tw = newCommitTracker(ww)
		}

		rctx := chi.RouteContext(r.Context())
		if rctx == nil {
			httpsec.WrapHandler(next, span, &appsecCfg).ServeHTTP(tw, r)
			return
		}

		// What chi knows now (for example with r.With(Middleware())): the input of
		// AppSec before the route was reported.
		route, params := cfg.modifyResourceName(rctx.RoutePattern()), knownParams(rctx)
		appsecCfg.Route, appsecCfg.RouteParams = route, params

		tw, tr, afterHandle, handled := httpsec.BeforeHandle(tw, r, span, &appsecCfg)
		defer afterHandle()
		reportDeclaredRoute(tr.Context(), rctx, cfg, mounts)
		if handled {
			return
		}
		// Deferred, so that the normalized route of a request whose handler panics
		// is the route that chi selected.
		defer reportDeclaredRoute(tr.Context(), rctx, cfg, mounts)
		next.ServeHTTP(tw, tr)

		if finalRoute := reportedRoute(rctx, cfg, cache); finalRoute != route {
			// The error is ignored: afterHandle applies the stored action, and the
			// commitTracker tells if the response can still be replaced.
			_ = httpsec.RouteMatched(tr.Context(), finalRoute, nil)
		}
	})
}

// reportDeclaredRoute reports the route that chi selected so far, as its patterns
// declare it, with its parameters, to API Security only. With OpenTelemetry
// semantics, a request that stops at a mounted chi router with no route for it
// gets no route, as it gets no http.route; an endpoint on the pattern of the mount
// that serves the request keeps its route.
func reportDeclaredRoute(ctx context.Context, rctx *chi.Context, cfg *config, mounts *sync.Map) {
	if cfg.otelEnabled && servedMount(rctx, mounts).router {
		httpsec.ReportMatchedRoute(ctx, httpsec.MatchedRoute{})
		return
	}
	route := declaredRoute(rctx)
	httpsec.ReportMatchedRoute(ctx, httpsec.MatchedRoute{Route: route, Params: selectedParams(rctx, route, mounts)})
}

// declaredRoute returns the route that chi selected, as its patterns declare it.
// Unlike RoutePattern, it keeps a declared trailing "/": /users/{id}/. The root
// route "/" of a router mounted without a trailing "/" adds none, as chi serves
// it for the mount path with and without "/". A mounted chi router that has no
// route for the request leaves the route of its mount.
func declaredRoute(rctx *chi.Context) string {
	n := len(rctx.RoutePatterns)
	if n == 0 {
		return ""
	}
	// A mount matches the patterns of the mounted router with its "*", from the
	// "/" that starts them: /api/* and /users/{id} are /api/users/{id}, and
	// /api/ and /* are /api/*.
	var route string
	for _, pattern := range rctx.RoutePatterns {
		route = strings.TrimSuffix(strings.TrimSuffix(route, "*"), "/") + pattern
	}
	if n > 1 && rctx.RoutePatterns[n-1] == "/" && !strings.HasSuffix(rctx.RoutePatterns[n-2], "*") {
		route = strings.TrimRight(route, "/")
	}
	if route == "" {
		return "/"
	}
	return route
}

// selectedParams returns the parameters of route, the route that chi selected,
// when it ends with "*": the normalized route needs no other value. The last
// value of a name that is used at several levels wins: the catch-all of a route
// in a mounted router is the last "*". A "*" that chi cleared for a mounted
// handler gets the rest of the path that chi gave that handler.
func selectedParams(rctx *chi.Context, route string, mounts *sync.Map) map[string]string {
	keys, values := rctx.URLParams.Keys, rctx.URLParams.Values
	if !strings.HasSuffix(route, "*") || len(keys) == 0 || len(keys) != len(values) {
		return nil
	}
	params := make(map[string]string, len(keys))
	for i, key := range keys {
		params[key] = values[i]
	}
	if params["*"] == "" && clearedWildcard(rctx, mounts) {
		params["*"] = strings.TrimPrefix(rctx.RoutePath, "/")
	}
	return params
}

// clearedWildcard reports that chi cleared the "*" that ends the route that it
// selected: the "*" of a mount whose handler is not a chi router. Mount gives that
// handler the rest of the routed path, after a "/", in RoutePath. A route that
// chi selected does not change RoutePath: it is empty at the root, or what the
// route matched with an empty "*", as a middleware can rewrite it. When a mount
// leaves the same text in RoutePath (/opaque/ for /opaque/opaque/), the route
// table tells which handler chi ran.
func clearedWildcard(rctx *chi.Context, mounts *sync.Map) bool {
	last := strings.TrimSuffix(rctx.RoutePatterns[len(rctx.RoutePatterns)-1], "*")
	params := 0
	for i := 0; i < len(last); i++ {
		if last[i] == '{' {
			params++
			i = closingBrace(last, i)
		}
	}
	// The parameters of the last pattern are the last ones that chi bound.
	values := rctx.URLParams.Values
	start := len(values) - 1 - params
	if start < 0 {
		return false
	}
	if expandPattern(last, values[start:len(values)-1]) != rctx.RoutePath {
		return true
	}
	return servedMount(rctx, mounts).mount
}

// mountServed tells, for the last pattern that chi matched, whether chi served
// the request with the handler that Mount registers, and whether that mount is a
// chi router that has no route for the request.
type mountServed struct{ mount, router bool }

// servedMount returns what served the last pattern that chi matched, as the route
// tables of rctx.Routes and of the routers mounted in it tell. A mount ends its
// pattern with "*", or is an alias that leaves "/" in RoutePath. The answer is
// cached per method and patterns: Routes builds the route table of a router on
// each call.
func servedMount(rctx *chi.Context, mounts *sync.Map) mountServed {
	n := len(rctx.RoutePatterns)
	if n == 0 || !strings.HasSuffix(rctx.RoutePatterns[n-1], "*") && rctx.RoutePath != "/" {
		return mountServed{}
	}
	// NUL is never in a method or a pattern.
	key := rctx.RouteMethod + "\x00" + strings.Join(rctx.RoutePatterns, "\x00")
	if served, ok := mounts.Load(key); ok {
		return served.(mountServed)
	}
	var served mountServed
	routes := rctx.Routes
	for i, pattern := range rctx.RoutePatterns {
		route, ok := findRoute(routes, pattern)
		if !ok {
			break
		}
		// The route table lists the route of an alias of a mount (/api for
		// /api/*), but not the handlers of the alias: Mount's handler served it,
		// as it left "/" in RoutePath.
		if i == n-1 && (!strings.HasSuffix(pattern, "*") || isMountHandler(route.Handlers[rctx.RouteMethod])) {
			served = mountServed{mount: true, router: route.SubRoutes != nil}
		}
		routes = route.SubRoutes
	}
	mounts.Store(key, served)
	return served
}

// findRoute returns the route of pattern in the route table of routes, which
// leaves out the aliases /api and /api/ that Mount adds for /api/*.
func findRoute(routes chi.Routes, pattern string) (chi.Route, bool) {
	if routes == nil {
		return chi.Route{}, false
	}
	table := routes.Routes()
	for _, want := range [...]string{pattern, strings.TrimSuffix(pattern, "/") + "/*"} {
		for _, route := range table {
			if route.Pattern == want {
				return route, true
			}
		}
	}
	return chi.Route{}, false
}

// mountHandlerCode is the code of the handler that Mount registers, which all
// mounts share.
var mountHandlerCode = sync.OnceValue(func() uintptr {
	router := chi.NewRouter()
	router.Mount("/", http.NotFoundHandler())
	if routes := router.Routes(); len(routes) == 1 {
		return handlerCode(routes[0].Handlers["*"])
	}
	return 0
})

// isMountHandler reports that h is the handler that Mount registers. A router
// with inline middlewares (With, Group) wraps it in a ChainHandler.
func isMountHandler(h http.Handler) bool {
	for {
		chain, ok := h.(*chi.ChainHandler)
		if !ok {
			break
		}
		h = chain.Endpoint
	}
	code := handlerCode(h)
	return code != 0 && code == mountHandlerCode()
}

// handlerCode returns the code of h when it is a function, or 0.
func handlerCode(h http.Handler) uintptr {
	if v := reflect.ValueOf(h); v.Kind() == reflect.Func {
		return v.Pointer()
	}
	return 0
}

// knownParams returns the parameters that chi matched so far, as AppSec read them
// before the route was reported: the first value of a name that is used at several
// levels wins.
func knownParams(rctx *chi.Context) map[string]string {
	keys, values := rctx.URLParams.Keys, rctx.URLParams.Values
	if len(keys) == 0 || len(keys) != len(values) {
		return nil
	}
	params := make(map[string]string, len(keys))
	for i, key := range keys {
		if _, found := params[key]; !found {
			params[key] = values[i]
		}
	}
	return params
}

// expandPattern returns the path that pattern matched with values, the values of
// its parameters in order: each {name} or {name:regexp} is the next value.
func expandPattern(pattern string, values []string) string {
	var path strings.Builder
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '{' {
			path.WriteByte(pattern[i])
			continue
		}
		path.WriteString(values[0])
		values = values[1:]
		i = closingBrace(pattern, i)
	}
	return path.String()
}

// closingBrace returns the index of the "}" that closes the "{" at start in a chi
// pattern. Braces nest in a regexp: {id:[0-9]{3}}.
func closingBrace(pattern string, start int) int {
	depth := 0
	for i := start; i < len(pattern); i++ {
		switch pattern[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return len(pattern)
}
