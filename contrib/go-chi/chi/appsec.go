// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
)

// withAppsec returns the handler that runs the AppSec operation of a request.
// The handler expects the chi response writer that [Middleware] creates.
//
// With Orchestrion or otelc, chi's (*Mux).handle wraps each endpoint handler with
// internal/endpointhook.Wrap. Middleware then puts an endpointhook.Marker in the
// request context. A disabled marker means WithIgnoreRequest.
// Otherwise the marker has the routing depth at which Middleware started, and the
// route function. The wrapper reads the nearest marker. When the marker is enabled and
// chi selected an endpoint deeper than that depth, the wrapper gives the WAF the path
// parameters of the endpoint with httpsec.RouteMatched, before the handler: the last
// value of a name. AppSec gets "*" only for a catch-all route registered with a method,
// such as Get("/files/*", h), and it gets the value of that route. It applies a block
// with httpsec.EarlyBlock: the middlewares between Middleware and the
// endpoint see no status and no bytes on their own response writers. The wrapper does
// nothing when there is no marker (AppSec off, no chitrace Middleware), when the marker
// is disabled, or when Middleware started at the endpoint (r.With, or Use in the
// Group that registers the endpoint).
//
// A. Without Orchestrion or otelc
//
//	Placement                         Path parameters before the handler          Spans: match / 404, 405
//	Use at the root                   none                                        1 / 1
//	r.With, or Use in the leaf Group  all, "*" included; repeated name: last     1 / 0
//	Use at the root + r.With          as r.With (inner middleware)                2 / 1
//	Group.Use around Route or Mount   mount parameters and "*" (rest of path)      1 / 1 under the mount, else 0
//
// B. With Orchestrion or otelc (they add the Use at the root)
//
//	Placement in user code            Path parameters before the handler          Spans: match / 404, 405
//	none                              wrapper: last value; "*" for method-specific catch-all only  1 / 1
//	r.With, or Use in the leaf Group  as A (inner middleware); wrapper skips      2 / 1
//	Group.Use around Route or Mount   wrapper fires at endpoint                   2 / 2 under the mount, else 1
//	WithIgnoreRequest that ignores    none (disabled marker)                      1 / 1
//	user sub-router (NewRouter+Mount) wrapper, on the inner operation             2 / 2 under the mount, else 1
//	Mount of a wrapped non-chi.Routes wrapper, 2 times (mount, endpoint)          as its routers
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
// report sends only the route and causes no additional native WAF evaluation. A late
// block replaces the response only if it is not sent yet (see "Late blocks and route
// reports in HTTP contribs" in internal/appsec/README.md).
//
// The response writer is wrapped in a commitTracker (see writer_appsec.go) to know
// if the response is sent.
func withAppsec(next http.Handler, span trace.TagSetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := &httpsec.Config{Framework: "github.com/go-chi/chi"}

		tw := w
		if ww, ok := w.(middleware.WrapResponseWriter); ok {
			tw = newCommitTracker(ww)
		}

		rctx := chi.RouteContext(r.Context())
		if rctx == nil {
			httpsec.WrapHandler(next, span, cfg).ServeHTTP(tw, r)
			return
		}

		// What chi knows now (for example with r.With(Middleware())): the input of
		// AppSec before the route was reported.
		route, params := rctx.RoutePattern(), knownParams(rctx)
		cfg.Route, cfg.RouteParams = route, params

		late := http.HandlerFunc(func(w http.ResponseWriter, tr *http.Request) {
			next.ServeHTTP(w, tr)

			rc := chi.RouteContext(tr.Context())
			if rc == nil {
				return
			}
			if finalRoute := rc.RoutePattern(); finalRoute != route {
				// The error is ignored: afterHandle applies the stored action, and the
				// commitTracker tells if the response can still be replaced.
				_ = httpsec.RouteMatched(tr.Context(), finalRoute, nil)
			}
		})

		httpsec.WrapHandler(late, span, cfg).ServeHTTP(tw, r)
	})
}

// knownParams returns the parameters that chi matched so far, as AppSec read them
// before the route was reported: the last value of a name that is used at several
// levels wins.
func knownParams(rctx *chi.Context) map[string]string {
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
