// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"net/http"
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
// with r.With(Middleware()) in a mounted router, the first "*" is the mount's. A
// late block replaces the response only if it is not sent yet (see "Late blocks and
// route reports in HTTP contribs" in internal/appsec/README.md).
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

		late := http.HandlerFunc(func(w http.ResponseWriter, tr *http.Request) {
			// Deferred, so that the normalized route of a request whose handler
			// panics is not built from the renamed route.
			defer func() {
				if rc := chi.RouteContext(tr.Context()); rc != nil {
					httpsec.ReportMatchedRoute(tr.Context(), httpsec.MatchedRoute{
						Route:  declaredRoute(rc, cache),
						Params: selectedParams(rc),
					})
				}
			}()
			next.ServeHTTP(w, tr)

			rc := chi.RouteContext(tr.Context())
			if rc == nil {
				return
			}
			if finalRoute := reportedRoute(rc, cfg, cache); finalRoute != route {
				// The error is ignored: afterHandle applies the stored action, and the
				// commitTracker tells if the response can still be replaced.
				_ = httpsec.RouteMatched(tr.Context(), finalRoute, nil)
			}
		})

		httpsec.WrapHandler(late, span, &appsecCfg).ServeHTTP(tw, r)
	})
}

// declaredRoute returns the route that chi selected, as its patterns declare it.
// Unlike RoutePattern, it keeps a declared trailing "/": /users/{id}/. The root
// route "/" of a mounted router adds none, as chi serves it for the mount path
// with and without "/". It is empty when routing stopped at a mounted chi router
// that has no route for the request.
func declaredRoute(rctx *chi.Context, cache *sync.Map) string {
	n := len(rctx.RoutePatterns)
	if n == 0 || unmatchedSubrouter(rctx, cache) {
		return ""
	}
	route := strings.Join(rctx.RoutePatterns, "")
	for strings.Contains(route, "/*/") {
		route = strings.ReplaceAll(route, "/*/", "/")
	}
	if n > 1 && rctx.RoutePatterns[n-1] == "/" {
		route = strings.TrimRight(route, "/")
	}
	if route == "" {
		return "/"
	}
	return route
}

// selectedParams returns the parameters of the route that chi selected. The last
// value of a name that is used at several levels wins: the catch-all of a route
// in a mounted router is the last "*".
func selectedParams(rctx *chi.Context) map[string]string {
	keys, values := rctx.URLParams.Keys, rctx.URLParams.Values
	if len(keys) == 0 || len(keys) != len(values) {
		return nil
	}
	params := make(map[string]string, len(keys))
	for i, key := range keys {
		params[key] = values[i]
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
