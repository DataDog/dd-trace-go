// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"context"
	"maps"
	"net/http"
	"slices"
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
// With Use before routing, chi selects the route after this middleware starts.
// So the handler does these steps:
//  1. It wraps the response writer in a commitTracker (see writer_appsec.go).
//  2. Before the handler runs, it asks chi which route matches (lookupRoute) and
//     gives the path parameters of that route to the WAF. A tracker that is not
//     complete gives no newly discovered parameters.
//  3. After the handler returns, it gives the route and the path parameters that
//     chi selected to the WAF (httpsec.RouteMatched), when they are not the ones
//     of step 2. This run never stops the handler. A block or a redirect from it
//     replaces the response only when the response is not sent yet.
func withAppsec(next http.Handler, span trace.TagSetter, cfg *config, cache *sync.Map) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A copy for each request: httpsec.BeforeHandle writes in the config.
		appsecCfg := cfg.appsecConfig

		tw, ct := w, (*commitTracker)(nil)
		if ww, ok := w.(middleware.WrapResponseWriter); ok {
			tw, ct = newCommitTracker(ww, r.Context())
			r = r.WithContext(context.WithValue(r.Context(), trackerCtxKey{}, ct))
		}

		rctx := chi.RouteContext(r.Context())
		if rctx == nil {
			httpsec.WrapHandler(next, span, &appsecCfg).ServeHTTP(tw, r)
			return
		}

		// Without a complete tracker, the WAF gets no newly discovered data.
		complete := ct != nil && ct.complete

		// What chi knows now (for example with r.With(Middleware())): the early input
		// that AppSec had before the lookup existed. The lookup never changes it.
		known := knownParams(rctx)

		route, params, found := "", map[string]string(nil), false
		if complete {
			route, params, found = lookupRoute(rctx, r, cfg)
		}
		if found {
			params, found = addPrediction(known, params)
		}
		if !found {
			route, params = cfg.modifyResourceName(rctx.RoutePattern()), known
		}
		appsecCfg.Route, appsecCfg.RouteParams = route, params
		early := snapshotEarly(route, params)

		late := http.HandlerFunc(func(w http.ResponseWriter, tr *http.Request) {
			next.ServeHTTP(w, tr)

			rc := chi.RouteContext(tr.Context())
			if rc == nil {
				return
			}
			finalRoute, finalParams := reportedRoute(rc, cfg, cache), routeParams(rc)
			if early.sameAs(finalRoute, finalParams) {
				return
			}
			if !complete {
				finalParams = nil
			}
			// The error is ignored: afterHandle applies the stored action, and the
			// commitTracker tells if the response can still be replaced.
			_ = httpsec.RouteMatched(tr.Context(), finalRoute, finalParams)
		})

		httpsec.WrapHandler(late, span, &appsecCfg).ServeHTTP(tw, r)
	})
}

// earlyData is the route and the path parameters that the WAF got before the
// handler ran. It owns a copy of the parameters, so a later change of the map
// that the WAF received does not change the comparison.
type earlyData struct {
	route  string
	params map[string]string
}

func snapshotEarly(route string, params map[string]string) earlyData {
	return earlyData{route: route, params: maps.Clone(params)}
}

// sameAs reports whether the final data of chi is the data that the WAF has. A
// nil map and an empty map are the same.
func (e earlyData) sameAs(route string, params map[string]string) bool {
	return e.route == route && maps.Equal(e.params, params)
}

// knownParams returns the parameters that chi matched so far, as AppSec read them
// before the lookup existed: the first value of a name that is used at several
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

// addPrediction adds the parameters of the lookup to the parameters that chi
// already matched, and never changes one of them: it keeps every block that works
// without the lookup. It reports false when the prediction contradicts chi (a
// name has another value, for example when a name is used at several levels of
// mounts): then only what chi matched is used. The "*" is the exception to the
// contradiction: a tracer that wraps a mount (r.With(Middleware()).Mount) runs
// before chi resets the "*" of the mount, so the known value is kept.
func addPrediction(known, predicted map[string]string) (map[string]string, bool) {
	if len(known) == 0 {
		return predicted, true
	}
	merged := maps.Clone(predicted)
	if merged == nil {
		merged = make(map[string]string, len(known))
	}
	for key, value := range known {
		if other, found := merged[key]; found && other != value && key != "*" {
			return nil, false
		}
		merged[key] = value
	}
	return merged, true
}

// routeParams returns the URL parameters of rctx as a new map, or nil when there
// are none. The last value of a key wins, like [chi.Context.URLParam].
func routeParams(rctx *chi.Context) map[string]string {
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

// lookupRoute asks chi which route matches the request, like chi does when it
// routes the request. It returns found = false when the request cannot be
// matched (or does not need to be): the caller then uses the values that chi
// knows.
//
// The result is a prediction, and it extends the routes that chi already matched.
// It can differ from the route that chi selects when
// a middleware changes the path, the method or the route path after the tracer
// (or before a tracer in a sub-router), or when a mount hides its child router
// behind a handler wrapper (chi.Routes.Match stops at the mount).
func lookupRoute(rctx *chi.Context, r *http.Request, cfg *config) (route string, params map[string]string, found bool) {
	if rctx.Routes == nil {
		return "", nil, false
	}
	// A route that is already final (r.With(Middleware())) needs no lookup.
	if pattern := rctx.RoutePattern(); pattern != "" && !strings.HasSuffix(pattern, "/*") {
		return "", nil, false
	}

	// The same method and path as Mux.routeHTTP (chi v5.3.0 mux.go:446-461).
	method := rctx.RouteMethod
	if method == "" {
		method = r.Method
	}
	var path string
	if len(rctx.RoutePatterns) == 0 && rctx.RoutePath != "" {
		path = rctx.RoutePath
	} else {
		// In a sub-router, rctx.Routes is the root router and rctx.RoutePath is
		// relative to the child: use the whole path.
		if path = r.URL.RawPath; path == "" {
			path = r.URL.Path
		}
		if path == "" {
			path = "/"
		}
	}

	tmp := chi.NewRouteContext()
	if !rctx.Routes.Match(tmp, method, path) {
		return "", nil, false
	}
	// chi may already have routed part of the request (a parent mount), possibly
	// with a changed route path. A prediction that does not extend what chi
	// matched contradicts chi: drop it, and keep what chi matched.
	if !extendsMatched(rctx, tmp) {
		return "", nil, false
	}
	return cfg.modifyResourceName(tmp.RoutePattern()), lookupParams(tmp), true
}

// extendsMatched reports whether the prediction tmp starts with what chi already
// matched in rctx: the same route patterns, and the same parameters (name and
// value) of those patterns. The "*" values are not compared: chi blanks the "*"
// that connects a mount when it enters the mount, and the lookup does not.
func extendsMatched(rctx, tmp *chi.Context) bool {
	known := rctx.RoutePatterns
	if len(known) > len(tmp.RoutePatterns) || !slices.Equal(known, tmp.RoutePatterns[:len(known)]) {
		return false
	}
	keys, values := rctx.URLParams.Keys, rctx.URLParams.Values
	if len(keys) != len(values) || len(keys) > len(tmp.URLParams.Keys) || len(tmp.URLParams.Keys) != len(tmp.URLParams.Values) {
		return false
	}
	for i, key := range keys {
		if key != tmp.URLParams.Keys[i] || (key != "*" && values[i] != tmp.URLParams.Values[i]) {
			return false
		}
	}
	return true
}

// lookupParams returns the URL parameters that a successful Match left in tmp.
//
// Match does not run the mount handlers, which reset the "*" parameter that
// connects a sub-router when chi enters the mount. So all "*" values are reset
// here, except the value of the last "*" when the matched route itself ends with
// a wildcard (an endpoint such as "/files/*", or a mount that Match could not
// follow).
func lookupParams(tmp *chi.Context) map[string]string {
	keys, values := tmp.URLParams.Keys, tmp.URLParams.Values
	if len(keys) == 0 || len(keys) != len(values) {
		return nil
	}
	keep := -1
	if n := len(tmp.RoutePatterns); n > 0 && strings.HasSuffix(tmp.RoutePatterns[n-1], "*") {
		for i, key := range slices.Backward(keys) {
			if key == "*" {
				keep = i
				break
			}
		}
	}
	params := make(map[string]string, len(keys))
	for i, key := range keys {
		value := values[i]
		if key == "*" && i != keep {
			value = ""
		}
		params[key] = value
	}
	return params
}
