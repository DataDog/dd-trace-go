// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"context"
	"net/http"
	"net/url"
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
func withAppsec(next http.Handler, span trace.TagSetter, cfg *config, cache *sync.Map) http.Handler {
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
		path := routingPath(r.URL)
		reportDeclaredRoute(tr.Context(), rctx, path)
		if handled {
			return
		}
		// Deferred, so that the normalized route of a request whose handler panics
		// is the route that chi selected.
		defer reportDeclaredRoute(tr.Context(), rctx, path)
		next.ServeHTTP(tw, tr)

		if finalRoute := reportedRoute(rctx, cfg, cache); finalRoute != route {
			// The error is ignored: afterHandle applies the stored action, and the
			// commitTracker tells if the response can still be replaced.
			_ = httpsec.RouteMatched(tr.Context(), finalRoute, nil)
		}
	})
}

// reportDeclaredRoute reports the route that chi selected so far, as its patterns
// declare it, with its parameters, to API Security only.
func reportDeclaredRoute(ctx context.Context, rctx *chi.Context, path string) {
	route := declaredRoute(rctx)
	httpsec.ReportMatchedRoute(ctx, httpsec.MatchedRoute{Route: route, Params: selectedParams(rctx, route, path)})
}

// routingPath returns the path that chi routes: URL.RawPath when net/http sets it,
// otherwise URL.Path.
func routingPath(u *url.URL) string {
	if u.RawPath != "" {
		return u.RawPath
	}
	return u.Path
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
// for path, the path that chi routed. The last value of a name that is used at
// several levels wins: the catch-all of a route in a mounted router is the last
// "*". chi clears the "*" of a mount before it calls the mounted handler, which
// is also the "*" of route when that handler is not a chi router: the value of
// the "*" of route is the rest of path after what route matched before it.
func selectedParams(rctx *chi.Context, route, path string) map[string]string {
	keys, values := rctx.URLParams.Keys, rctx.URLParams.Values
	if len(keys) == 0 || len(keys) != len(values) {
		return nil
	}
	params := make(map[string]string, len(keys))
	for i, key := range keys {
		params[key] = values[i]
	}
	if prefix, ok := strings.CutSuffix(route, "*"); ok {
		if matched, ok := expandPattern(prefix, rctx.URLParams); ok && strings.HasPrefix(path, matched) {
			params["*"] = path[len(matched):]
		}
	}
	return params
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

// expandPattern returns the path that pattern matched with params, the values
// that chi bound in order at each routing level: each {name} or {name:regexp}
// is the value of the next parameter that is not a "*", the "*" of a mount. It
// returns false when params has fewer values.
func expandPattern(pattern string, params chi.RouteParams) (string, bool) {
	var path strings.Builder
	next := 0
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '{' {
			path.WriteByte(pattern[i])
			continue
		}
		depth, end := 0, i
		for ; end < len(pattern); end++ {
			if pattern[end] == '{' {
				depth++
			} else if pattern[end] == '}' {
				if depth--; depth == 0 {
					break
				}
			}
		}
		for next < len(params.Keys) && params.Keys[next] == "*" {
			next++
		}
		if next == len(params.Keys) {
			return "", false
		}
		path.WriteString(params.Values[next])
		next++
		i = end
	}
	return path.String(), true
}
